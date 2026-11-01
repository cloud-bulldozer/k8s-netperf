package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

var pinnedTestImage = "quay.io/cloud-bulldozer/k8s-netperf@" + testDigest

func reuseParams(image string) DeploymentParams {
	return DeploymentParams{
		Name:      "server",
		Namespace: namespace,
		Replicas:  1,
		Image:     image,
		Labels:    map[string]string{"role": "server"},
		Commands:  [][]string{{"netserver", "-D"}},
	}
}

// rolledOut is the Deployment newDeployment builds for dp, with a finished rollout.
func rolledOut(dp DeploymentParams) *appsv1.Deployment {
	d := newDeployment(dp)
	d.TypeMeta = metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"}
	d.Namespace = dp.Namespace
	d.Generation = 2
	d.Status = appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1}
	return d
}

func readyPod(name, image string, extra ...corev1.Container) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{"role": "server"}},
		Spec:       corev1.PodSpec{Containers: append([]corev1.Container{{Name: "server-0", Image: image, ImagePullPolicy: imagePullPolicy(image)}}, extra...)},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			PodIP:      "10.0.0.2",
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
}

func TestDeploymentPodsCurrent(t *testing.T) {
	dp := reuseParams(pinnedTestImage)
	terminating := readyPod("server-terminating", pinnedTestImage)
	now := metav1.Now()
	terminating.DeletionTimestamp = &now
	otherRole := readyPod("client-old", DefaultImage)
	otherRole.Labels = map[string]string{"role": "client-local"}

	testCases := []struct {
		name    string
		mutate  func(*appsv1.Deployment)
		pods    []corev1.Pod
		wantErr string
	}{
		{name: "finished rollout with current pods", pods: []corev1.Pod{readyPod("server-new", pinnedTestImage)}},
		{name: "sidecar with its own image is ignored", pods: []corev1.Pod{readyPod("server-new", pinnedTestImage, corev1.Container{Name: "istio-proxy", Image: "docker.io/istio/proxyv2:1.24", ImagePullPolicy: corev1.PullAlways})}},
		{name: "pods of other roles are ignored", pods: []corev1.Pod{readyPod("server-new", pinnedTestImage), otherRole}},
		{
			name:    "paused rollout with an old-image ready pod",
			mutate:  func(d *appsv1.Deployment) { d.Spec.Paused = true; d.Status.UpdatedReplicas = 0 },
			pods:    []corev1.Pod{readyPod("server-old", DefaultImage)},
			wantErr: "paused or unfinished rollout",
		},
		{
			name:    "template change not yet observed",
			mutate:  func(d *appsv1.Deployment) { d.Status.ObservedGeneration = 1 },
			pods:    []corev1.Pod{readyPod("server-old", DefaultImage)},
			wantErr: "paused or unfinished rollout",
		},
		{
			name:    "old replica still running beside the new one",
			mutate:  func(d *appsv1.Deployment) { d.Status.Replicas = 2 },
			pods:    []corev1.Pod{readyPod("server-new", pinnedTestImage), readyPod("server-old", DefaultImage)},
			wantErr: "paused or unfinished rollout",
		},
		{
			name:    "new replica not available yet",
			mutate:  func(d *appsv1.Deployment) { d.Status.AvailableReplicas = 0 },
			pods:    []corev1.Pod{readyPod("server-new", pinnedTestImage)},
			wantErr: "paused or unfinished rollout",
		},
		{name: "old-image pod left over", pods: []corev1.Pod{readyPod("server-new", pinnedTestImage), readyPod("server-old", DefaultImage)}, wantErr: "runs image"},
		{name: "terminating pod", pods: []corev1.Pod{readyPod("server-new", pinnedTestImage), terminating}, wantErr: "terminating pod"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			d := rolledOut(dp)
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
}

// readOnlyAPI serves one Deployment, its watch and a pod list to a real Clientset and fails any write.
type readOnlyAPI struct {
	deployment *appsv1.Deployment
	pods       *corev1.PodList
}

func (a *readOnlyAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	var obj any
	switch {
	case r.Method != http.MethodGet:
		return nil, fmt.Errorf("unexpected write: %s %s", r.Method, r.URL.Path)
	case strings.HasSuffix(r.URL.Path, "/deployments/"+a.deployment.Name):
		obj = a.deployment
	case strings.HasSuffix(r.URL.Path, "/deployments") && r.URL.Query().Get("watch") == "true":
		obj = map[string]any{"type": "ADDED", "object": a.deployment}
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

func readOnlyClientset(t *testing.T, d *appsv1.Deployment, pods ...corev1.Pod) *kubernetes.Clientset {
	t.Helper()
	list := &corev1.PodList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"}, Items: pods}
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: "http://k8s-netperf.invalid", Transport: &readOnlyAPI{deployment: d, pods: list}})
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

// A paused rollout: the template asks for the pinned digest, the only ready pod still runs the old image.
func TestDeployDeploymentRejectsOldReplicaOfPinnedTemplate(t *testing.T) {
	dp := reuseParams(pinnedTestImage)
	d := rolledOut(dp)
	d.Spec.Paused = true
	d.Status.UpdatedReplicas = 0
	pods, err := deployDeployment(readOnlyClientset(t, d, readyPod("server-old", DefaultImage)), dp)
	if err == nil {
		t.Fatalf("deployDeployment() reused the deployment and returned %d pods, want an error", len(pods.Items))
	}
	if !strings.Contains(err.Error(), "paused or unfinished rollout") {
		t.Errorf("deployDeployment() error = %v, want the rollout reason", err)
	}
}

func TestPullPolicyOverrides(t *testing.T) {
	dp := reuseParams(pinnedTestImage)
	forced := readyPod("server-forced", pinnedTestImage)
	forced.Spec.Containers[0].ImagePullPolicy = corev1.PullAlways
	sidecar := corev1.Container{Name: "istio-proxy", Image: "docker.io/istio/proxyv2:1.24", ImagePullPolicy: corev1.PullAlways}

	if got := pullPolicyOverrides(corev1.PodList{Items: []corev1.Pod{readyPod("server-new", pinnedTestImage, sidecar)}}, dp); len(got) != 0 {
		t.Errorf("matching benchmark container with an Always sidecar reported %v, want none", got)
	}
	got := pullPolicyOverrides(corev1.PodList{Items: []corev1.Pod{forced}}, dp)
	if len(got) != 1 || !strings.Contains(got[0], "server-forced container server-0 has imagePullPolicy Always instead of IfNotPresent") {
		t.Errorf("admission-forced Always reported %v, want one override for server-0", got)
	}
	if !strings.Contains(got[0], "nodes pull the image on every pod start") {
		t.Errorf("forced Always override %q does not say the image is pulled on every start", got[0])
	}
	if got := pullPolicyOverrides(corev1.PodList{Items: []corev1.Pod{readyPod("server-latest", DefaultImage)}}, reuseParams(DefaultImage)); len(got) != 0 {
		t.Errorf("default image with Always reported %v, want none", got)
	}

	never := readyPod("server-never", pinnedTestImage)
	never.Spec.Containers[0].ImagePullPolicy = corev1.PullNever
	if got := pullPolicyOverrides(corev1.PodList{Items: []corev1.Pod{never}}, dp); len(got) != 1 || !strings.Contains(got[0], "never pull") || strings.Contains(got[0], "every pod start") {
		t.Errorf("forced Never reported %v, want one override describing Never", got)
	}
	ifNotPresent := readyPod("server-cached", DefaultImage)
	ifNotPresent.Spec.Containers[0].ImagePullPolicy = corev1.PullIfNotPresent
	if got := pullPolicyOverrides(corev1.PodList{Items: []corev1.Pod{ifNotPresent}}, reuseParams(DefaultImage)); len(got) != 1 || !strings.Contains(got[0], "only when it is not present") {
		t.Errorf("forced IfNotPresent reported %v, want one override describing IfNotPresent", got)
	}
}

// captureWarnings replaces warnf for the rest of the test and returns the warnings logged.
func captureWarnings(t *testing.T) *[]string {
	t.Helper()
	var warnings []string
	saved := warnf
	warnf = func(format string, args ...interface{}) { warnings = append(warnings, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { warnf = saved })
	return &warnings
}

// Admission forced Always on a pod whose template asks IfNotPresent: reused, and the override only logged.
func TestDeployDeploymentReportsForcedPullPolicy(t *testing.T) {
	warnings := captureWarnings(t)
	dp := reuseParams(pinnedTestImage)
	forced := readyPod("server-forced", pinnedTestImage, corev1.Container{Name: "istio-proxy", Image: "docker.io/istio/proxyv2:1.24", ImagePullPolicy: corev1.PullAlways})
	forced.Spec.Containers[0].ImagePullPolicy = corev1.PullAlways
	pods, err := deployDeployment(readOnlyClientset(t, rolledOut(dp), forced), dp)
	if err != nil {
		t.Fatalf("deployDeployment() error = %v, want reuse", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("deployDeployment() returned %d pods, want 1", len(pods.Items))
	}
	if len(*warnings) != 1 || !strings.Contains((*warnings)[0], "pod server-forced container server-0 has imagePullPolicy Always instead of IfNotPresent") {
		t.Errorf("warnings = %q, want exactly one for server-0 and none for the sidecar", *warnings)
	}
}

func TestDeployDeploymentWarnsNothingForMatchingPolicy(t *testing.T) {
	warnings := captureWarnings(t)
	dp := reuseParams(pinnedTestImage)
	if _, err := deployDeployment(readOnlyClientset(t, rolledOut(dp), readyPod("server-new", pinnedTestImage)), dp); err != nil {
		t.Fatalf("deployDeployment() error = %v, want reuse", err)
	}
	if len(*warnings) != 0 {
		t.Errorf("warnings = %q, want none", *warnings)
	}
}

func TestDeployDeploymentReusesFinishedRollout(t *testing.T) {
	dp := reuseParams(pinnedTestImage)
	pods, err := deployDeployment(readOnlyClientset(t, rolledOut(dp), readyPod("server-new", pinnedTestImage)), dp)
	if err != nil {
		t.Fatalf("deployDeployment() error = %v, want reuse", err)
	}
	if len(pods.Items) != 1 || pods.Items[0].Spec.Containers[0].Image != pinnedTestImage {
		t.Errorf("deployDeployment() pods = %+v, want the current pod", pods.Items)
	}
}
