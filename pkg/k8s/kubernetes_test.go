package k8s

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNewDeploymentRuntimeClass(t *testing.T) {
	testCases := []struct {
		name             string
		runtimeClass     string
		hostNetwork      bool
		wantRuntimeClass string
	}{
		{name: "defaults preserved"},
		{name: "pod runtime class", runtimeClass: "kata", wantRuntimeClass: "kata"},
		{name: "host network omits runtime class", runtimeClass: "kata", hostNetwork: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			params := DeploymentParams{
				Name:         "workload",
				Namespace:    "netperf",
				Replicas:     1,
				Image:        "example.com/image",
				Labels:       map[string]string{"app": "workload"},
				Commands:     [][]string{{"sleep", "3600"}},
				RuntimeClass: tc.runtimeClass,
				HostNetwork:  tc.hostNetwork,
			}
			deployment := newDeployment(params)
			if tc.wantRuntimeClass == "" {
				if deployment.Spec.Template.Spec.RuntimeClassName != nil {
					t.Errorf("RuntimeClassName = %q, want nil", *deployment.Spec.Template.Spec.RuntimeClassName)
				}
			} else if deployment.Spec.Template.Spec.RuntimeClassName == nil || *deployment.Spec.Template.Spec.RuntimeClassName != tc.wantRuntimeClass {
				t.Errorf("RuntimeClassName = %v, want %q", deployment.Spec.Template.Spec.RuntimeClassName, tc.wantRuntimeClass)
			}
		})
	}
}

func TestDeploymentFailureError(t *testing.T) {
	testCases := []struct {
		name       string
		dp         DeploymentParams
		conditions []appsv1.DeploymentCondition
		want       string
	}{
		{
			name: "unavailable runtime class",
			dp:   DeploymentParams{Name: "server", RuntimeClass: "kata"},
			conditions: []appsv1.DeploymentCondition{{
				Type:    appsv1.DeploymentReplicaFailure,
				Status:  corev1.ConditionTrue,
				Reason:  "FailedCreate",
				Message: `pods "server" is forbidden: runtimeclass "kata" not found`,
			}},
			want: `pod workload with runtime class "kata"`,
		},
		{
			name: "unschedulable workload",
			dp:   DeploymentParams{Name: "client"},
			conditions: []appsv1.DeploymentCondition{{
				Type:    appsv1.DeploymentProgressing,
				Status:  corev1.ConditionFalse,
				Reason:  "ProgressDeadlineExceeded",
				Message: "ReplicaSet has timed out progressing.",
			}},
			want: "workload may be unschedulable",
		},
		{
			name: "no failure",
			dp:   DeploymentParams{Name: "server"},
			conditions: []appsv1.DeploymentCondition{{
				Type:   appsv1.DeploymentAvailable,
				Status: corev1.ConditionTrue,
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			d := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: tc.dp.Name},
				Status:     appsv1.DeploymentStatus{Conditions: tc.conditions},
			}
			err := deploymentFailureError(d, tc.dp)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("deploymentFailureError() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("deploymentFailureError() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestWaitForReadyReturnsAlreadyReadyDeployment(t *testing.T) {
	client := fake.NewClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "ready", Namespace: "netperf"},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 1},
	})

	ready, err := WaitForReady(client, DeploymentParams{Name: "ready", Namespace: "netperf"})
	if err != nil {
		t.Fatalf("WaitForReady() error = %v", err)
	}
	if !ready {
		t.Fatal("WaitForReady() = false, want true")
	}
}

func TestDeploymentReadyTimeoutErrorIncludesImageReference(t *testing.T) {
	err := deploymentReadyTimeoutError(DeploymentParams{
		Name:      "server",
		Namespace: "netperf",
		Image:     "mirror.example.local/bench/netperf:v1",
	})
	if got := err.Error(); !strings.Contains(got, "mirror.example.local/bench/netperf:v1") || !strings.Contains(got, "image pull failures") {
		t.Errorf("deploymentReadyTimeoutError() = %q, want image reference and actionable guidance", got)
	}
}
