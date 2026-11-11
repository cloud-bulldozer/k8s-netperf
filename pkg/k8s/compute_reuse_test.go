package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func computeReuseParams(compute corev1.ResourceList) DeploymentParams {
	dp := computeParams(compute, nil)
	dp.Namespace = namespace
	dp.Commands = [][]string{{"netserver", "-D"}}
	return dp
}

// computeRolledOut is the Deployment newDeployment builds for dp, with a finished rollout.
func computeRolledOut(dp DeploymentParams) *appsv1.Deployment {
	d := newDeployment(dp)
	d.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}
	d.Namespace = dp.Namespace
	d.Generation = 2
	d.Status = appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}
	return d
}

func computeReadyPod(name string, compute corev1.ResourceList, extra ...corev1.Container) corev1.Pod {
	container := corev1.Container{Name: "server-0", Image: "example.com/image"}
	if len(compute) > 0 {
		container.Resources = corev1.ResourceRequirements{Requests: compute.DeepCopy(), Limits: compute.DeepCopy()}
	}
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{"role": "server"}},
		Spec:       corev1.PodSpec{Containers: append([]corev1.Container{container}, extra...)},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			PodIP:      "10.0.0.2",
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
}

func TestDeploymentPodsCurrentCompute(t *testing.T) {
	guaranteed := testCompute("2", "2Gi")
	dp := computeReuseParams(guaranteed)
	terminating := computeReadyPod("server-terminating", guaranteed)
	now := metav1.Now()
	terminating.DeletionTimestamp = &now
	otherRole := computeReadyPod("client-old", nil)
	otherRole.Labels = map[string]string{"role": "client-local"}
	sidecar := corev1.Container{Name: "istio-proxy", Image: "docker.io/istio/proxyv2:1.24", Resources: corev1.ResourceRequirements{Requests: testCompute("100m", "128Mi")}}

	testCases := []struct {
		name    string
		mutate  func(*appsv1.Deployment)
		pods    []corev1.Pod
		wantErr string
	}{
		{name: "finished rollout with current pods", pods: []corev1.Pod{computeReadyPod("server-new", guaranteed)}},
		{name: "sidecar with its own resources is ignored", pods: []corev1.Pod{computeReadyPod("server-new", guaranteed, sidecar)}},
		{name: "pods of other roles are ignored", pods: []corev1.Pod{computeReadyPod("server-new", guaranteed), otherRole}},
		{
			name:    "paused rollout with an old-size ready pod",
			mutate:  func(d *appsv1.Deployment) { d.Spec.Paused = true; d.Status.UpdatedReplicas = 0 },
			pods:    []corev1.Pod{computeReadyPod("server-old", testCompute("1", "1Gi"))},
			wantErr: "paused or unfinished rollout",
		},
		{
			name:    "template change not yet observed",
			mutate:  func(d *appsv1.Deployment) { d.Status.ObservedGeneration = 1 },
			pods:    []corev1.Pod{computeReadyPod("server-old", nil)},
			wantErr: "paused or unfinished rollout",
		},
		{
			name:    "old replica still running beside the new one",
			mutate:  func(d *appsv1.Deployment) { d.Status.Replicas = 2 },
			pods:    []corev1.Pod{computeReadyPod("server-new", guaranteed), computeReadyPod("server-old", nil)},
			wantErr: "paused or unfinished rollout",
		},
		{
			name:    "new replica not available yet",
			mutate:  func(d *appsv1.Deployment) { d.Status.AvailableReplicas = 0 },
			pods:    []corev1.Pod{computeReadyPod("server-new", guaranteed)},
			wantErr: "paused or unfinished rollout",
		},
		{name: "old-size pod left over", pods: []corev1.Pod{computeReadyPod("server-new", guaranteed), computeReadyPod("server-old", testCompute("1", "1Gi"))}, wantErr: "other than requested"},
		{name: "best-effort pod left over", pods: []corev1.Pod{computeReadyPod("server-new", guaranteed), computeReadyPod("server-old", nil)}, wantErr: "other than requested"},
		{name: "terminating pod", pods: []corev1.Pod{computeReadyPod("server-new", guaranteed), terminating}, wantErr: "terminating pod"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			d := computeRolledOut(dp)
			if tc.mutate != nil {
				tc.mutate(d)
			}
			objects := make([]runtime.Object, 0, len(tc.pods))
			for i := range tc.pods {
				objects = append(objects, &tc.pods[i])
			}
			err := deploymentPodsCurrent(context.Background(), fake.NewClientset(objects...), d, dp)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("deploymentPodsCurrent() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("deploymentPodsCurrent() error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}

	// Without --cpu/--memory, LimitRange defaults on pods of the current template still allow reuse.
	unsetDP := computeReuseParams(nil)
	limitRangeDefaults := computeReadyPod("server-defaults", testCompute("500m", "512Mi"))
	if err := deploymentPodsCurrent(context.Background(), fake.NewClientset(&limitRangeDefaults), computeRolledOut(unsetDP), unsetDP); err != nil {
		t.Errorf("pod with admission defaults refused for a run without --cpu/--memory: %v", err)
	}
}

func TestVerifyPodCompute(t *testing.T) {
	guaranteed := testCompute("2", "2Gi")
	shrunk := computeReadyPod("server-shrunk", testCompute("1", "1Gi"))
	limitless := computeReadyPod("server-limitless", nil)
	limitless.Spec.Containers[0].Resources.Requests = guaranteed.DeepCopy()
	sidecar := corev1.Container{Name: "istio-proxy", Image: "docker.io/istio/proxyv2:1.24", Resources: corev1.ResourceRequirements{Requests: testCompute("100m", "128Mi")}}

	testCases := []struct {
		name    string
		compute corev1.ResourceList
		pods    []corev1.Pod
		wantErr string
	}{
		{name: "requested size", compute: guaranteed, pods: []corev1.Pod{computeReadyPod("server-ok", guaranteed, sidecar)}},
		{name: "unset ignores admission defaults", pods: []corev1.Pod{computeReadyPod("server-defaults", testCompute("500m", "512Mi"))}},
		{name: "admission shrank requests and limits equally", compute: guaranteed, pods: []corev1.Pod{shrunk}, wantErr: "pod server-shrunk container server-0 requests cpu=1 memory=1Gi and is limited to cpu=1 memory=1Gi instead of cpu=2 memory=2Gi"},
		{name: "limits removed", compute: guaranteed, pods: []corev1.Pod{limitless}, wantErr: "is limited to cpu unset memory unset"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyPodCompute(corev1.PodList{Items: tc.pods}, computeReuseParams(tc.compute))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("verifyPodCompute() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("verifyPodCompute() error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// computeFreshAPI lets a real Clientset create one Deployment and returns the given pods, as admission left them.
type computeFreshAPI struct {
	created *appsv1.Deployment
	pods    *corev1.PodList
}

func (a *computeFreshAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	respond := func(code int, obj any) (*http.Response, error) {
		body, err := json.Marshal(obj)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body) + "\n")), Request: r}, nil
	}
	switch {
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/deployments/") && a.created != nil:
		return respond(http.StatusOK, a.created)
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/deployments/"):
		return respond(http.StatusNotFound, metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: metav1.StatusFailure, Reason: metav1.StatusReasonNotFound, Code: http.StatusNotFound})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/deployments"):
		d := &appsv1.Deployment{}
		if err := json.NewDecoder(r.Body).Decode(d); err != nil {
			return nil, err
		}
		d.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}
		d.Status = appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}
		a.created = d
		return respond(http.StatusCreated, d)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pods"):
		return respond(http.StatusOK, a.pods)
	}
	return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
}

func computeFreshClientset(t *testing.T, pods ...corev1.Pod) (*kubernetes.Clientset, *computeFreshAPI) {
	t.Helper()
	api := &computeFreshAPI{pods: &corev1.PodList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"}, Items: pods}}
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: "http://k8s-netperf.invalid", Transport: api, ContentConfig: rest.ContentConfig{ContentType: "application/json", AcceptContentTypes: "application/json"}})
	if err != nil {
		t.Fatal(err)
	}
	return cs, api
}

// Admission resized the pod to an equally Guaranteed cpu 1 / 1Gi, which the QoS check alone would accept.
func TestDeployDeploymentRejectsAdmissionResizedPod(t *testing.T) {
	dp := computeReuseParams(testCompute("2", "2Gi"))
	cs, api := computeFreshClientset(t, computeReadyPod("server-resized", testCompute("1", "1Gi")))
	_, err := deployDeployment(cs, dp, time.Minute)
	if api.created == nil {
		t.Fatal("deployDeployment() did not create the deployment")
	}
	if err == nil || !strings.Contains(err.Error(), "admission (for example a mutating webhook) changed them") {
		t.Fatalf("deployDeployment() error = %v, want the resized pod rejected", err)
	}
}

func TestDeployDeploymentAcceptsFreshPodOfRequestedSize(t *testing.T) {
	guaranteed := testCompute("2", "2Gi")
	dp := computeReuseParams(guaranteed)
	cs, api := computeFreshClientset(t, computeReadyPod("server-new", guaranteed))
	pods, err := deployDeployment(cs, dp, time.Minute)
	if err != nil {
		t.Fatalf("deployDeployment() error = %v, want success", err)
	}
	if api.created == nil || len(pods.Items) != 1 {
		t.Fatalf("deployDeployment() created %v and returned %d pods, want a created deployment and its pod", api.created != nil, len(pods.Items))
	}
	if c := api.created.Spec.Template.Spec.Containers[0]; !containerComputeMatches(c, guaranteed) {
		t.Errorf("created template container resources = %+v, want cpu 2 / 2Gi requests and limits", c.Resources)
	}
}

// computeReadOnlyAPI serves one Deployment and a pod list to a real Clientset and fails any write.
type computeReadOnlyAPI struct {
	deployment *appsv1.Deployment
	pods       *corev1.PodList
}

func (a *computeReadOnlyAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	var obj any
	switch {
	case r.Method != http.MethodGet:
		return nil, fmt.Errorf("unexpected write: %s %s", r.Method, r.URL.Path)
	case strings.HasSuffix(r.URL.Path, "/deployments/"+a.deployment.Name):
		obj = a.deployment
	case strings.HasSuffix(r.URL.Path, "/pods"):
		obj = a.pods
	default:
		return nil, fmt.Errorf("unexpected read: %s", r.URL.String())
	}
	body, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(body) + "\n")),
		Request:    r,
	}, nil
}

func computeReadOnlyClientset(t *testing.T, d *appsv1.Deployment, pods ...corev1.Pod) *kubernetes.Clientset {
	t.Helper()
	list := &corev1.PodList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"}, Items: pods}
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: "http://k8s-netperf.invalid", Transport: &computeReadOnlyAPI{deployment: d, pods: list}})
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

// A paused rollout: the template asks for cpu 2 / 2Gi, the only ready pod still has the old size.
func TestDeployDeploymentRejectsOldSizeReplica(t *testing.T) {
	dp := computeReuseParams(testCompute("2", "2Gi"))
	d := computeRolledOut(dp)
	d.Spec.Paused = true
	d.Status.UpdatedReplicas = 0
	pods, err := deployDeployment(computeReadOnlyClientset(t, d, computeReadyPod("server-old", testCompute("1", "1Gi"))), dp, time.Minute)
	if err == nil {
		t.Fatalf("deployDeployment() reused the deployment and returned %d pods, want an error", len(pods.Items))
	}
	if !strings.Contains(err.Error(), "paused or unfinished rollout") {
		t.Errorf("deployDeployment() error = %v, want the rollout reason", err)
	}
}

func TestDeployDeploymentReusesFinishedComputeRollout(t *testing.T) {
	guaranteed := testCompute("2", "2Gi")
	dp := computeReuseParams(guaranteed)
	pods, err := deployDeployment(computeReadOnlyClientset(t, computeRolledOut(dp), computeReadyPod("server-new", guaranteed)), dp, time.Minute)
	if err != nil {
		t.Fatalf("deployDeployment() error = %v, want reuse", err)
	}
	if len(pods.Items) != 1 || pods.Items[0].Name != "server-new" {
		t.Errorf("deployDeployment() pods = %+v, want the current pod", pods.Items)
	}
}
