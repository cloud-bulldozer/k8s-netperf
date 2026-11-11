package k8s

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func readyTestParams() DeploymentParams {
	return DeploymentParams{Name: "server", Namespace: "netperf", Labels: map[string]string{"role": "server"}}
}

func readyTestDeployment(ready int32, conditions ...appsv1.DeploymentCondition) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "netperf"},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: ready, Conditions: conditions},
	}
}

func pullingPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "server-abc", Namespace: "netperf", Labels: map[string]string{"role": "server"}},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "server-0",
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "Back-off pulling image"}},
			}},
		},
	}
}

func shortPolls(t *testing.T) {
	t.Helper()
	saved := readyPollInterval
	readyPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { readyPollInterval = saved })
}

func TestWaitForReadyReturnsOnceReady(t *testing.T) {
	shortPolls(t)
	client := fake.NewClientset(readyTestDeployment(0))
	gets := 0
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		gets++
		if gets < 3 {
			return true, readyTestDeployment(0), nil
		}
		return true, readyTestDeployment(1), nil
	})
	if err := WaitForReady(client, readyTestParams(), time.Minute); err != nil {
		t.Fatalf("WaitForReady() error = %v, want nil", err)
	}
	if gets != 3 {
		t.Fatalf("deployment fetched %d times, want 3", gets)
	}
}

func TestWaitForReadyDefaultsAnUnsetTimeout(t *testing.T) {
	shortPolls(t)
	client := fake.NewClientset()
	gets := 0
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		gets++
		return true, readyTestDeployment(int32(min(gets-1, 1))), nil
	})
	if err := WaitForReady(client, readyTestParams(), 0); err != nil {
		t.Fatalf("WaitForReady() with no timeout error = %v, want the default timeout, not an immediate failure", err)
	}
}

func TestWaitForReadyReportsFailedPodList(t *testing.T) {
	shortPolls(t)
	client := fake.NewClientset(readyTestDeployment(0))
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})
	err := WaitForReady(client, readyTestParams(), 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "unable to list the pods for diagnostics: connection refused") {
		t.Fatalf("WaitForReady() error = %v, want the failed pod list", err)
	}
}

func TestWaitForReadyTimesOutWithWaitingReasons(t *testing.T) {
	shortPolls(t)
	client := fake.NewClientset(readyTestDeployment(0), pullingPod())
	start := time.Now()
	err := WaitForReady(client, readyTestParams(), 50*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForReady() error = nil, want a timeout")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("WaitForReady() returned after %s, want about 50ms", elapsed)
	}
	for _, want := range []string{"not ready after 50ms", "--ready-timeout", "server-abc Pending", "ImagePullBackOff: Back-off pulling image"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("WaitForReady() error = %q, want it to contain %q", err, want)
		}
	}
}

func TestWaitForReadyFailsAtOnceOnReplicaFailure(t *testing.T) {
	shortPolls(t)
	failure := appsv1.DeploymentCondition{Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue, Reason: "FailedCreate", Message: "exceeded quota"}
	client := fake.NewClientset(readyTestDeployment(0, failure))
	start := time.Now()
	err := WaitForReady(client, readyTestParams(), time.Minute)
	if err == nil || !strings.Contains(err.Error(), "cannot create pods (FailedCreate): exceeded quota") {
		t.Fatalf("WaitForReady() error = %v, want the replica failure", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("WaitForReady() returned after %s, want at once", elapsed)
	}
}

func TestWaitForReadyKeepsWaitingPastProgressDeadline(t *testing.T) {
	shortPolls(t)
	stalled := appsv1.DeploymentCondition{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}
	client := fake.NewClientset(readyTestDeployment(0, stalled))
	gets := 0
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		gets++
		if gets < 3 {
			return true, readyTestDeployment(0, stalled), nil
		}
		return true, readyTestDeployment(1), nil
	})
	if err := WaitForReady(client, readyTestParams(), time.Minute); err != nil {
		t.Fatalf("WaitForReady() error = %v, want nil: a slow image pull can outlast the progress deadline", err)
	}
}

func TestWaitForReadyTimeoutReportsLastAPIError(t *testing.T) {
	shortPolls(t)
	client := fake.NewClientset()
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "server", errors.New("RBAC denied"))
	})
	err := WaitForReady(client, readyTestParams(), 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "not ready after 50ms") || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("WaitForReady() error = %v, want the timeout with the forbidden API error", err)
	}
}

// slowGets answers the first n reads with a pending Deployment, then stalls past the deadline and fails.
func slowGets(client *fake.Clientset, n int, stall time.Duration) {
	gets := 0
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		gets++
		if gets <= n {
			return true, readyTestDeployment(0), nil
		}
		time.Sleep(stall)
		return true, nil, context.DeadlineExceeded
	})
}

func TestWaitForReadyReportsFirstReadCutOffByDeadline(t *testing.T) {
	shortPolls(t)
	client := fake.NewClientset(pullingPod())
	slowGets(client, 0, 80*time.Millisecond)
	err := WaitForReady(client, readyTestParams(), 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("WaitForReady() error = %v, want the failed read when no read succeeded", err)
	}
}

func TestWaitForReadyKeepsEarlierReadErrorWhenLastReadIsCutOff(t *testing.T) {
	shortPolls(t)
	client := fake.NewClientset(pullingPod())
	gets := 0
	client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		gets++
		if gets == 1 {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "server", errors.New("RBAC denied"))
		}
		time.Sleep(80 * time.Millisecond)
		return true, nil, context.DeadlineExceeded
	})
	err := WaitForReady(client, readyTestParams(), 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "forbidden") || strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("WaitForReady() error = %v, want the earlier forbidden read, not the cut-off one", err)
	}
}

func TestWaitForReadyKeepsWaitingReasonsWhenLastReadIsCutOff(t *testing.T) {
	shortPolls(t)
	client := fake.NewClientset(pullingPod())
	slowGets(client, 1, 80*time.Millisecond)
	err := WaitForReady(client, readyTestParams(), 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "ImagePullBackOff") || strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("WaitForReady() error = %v, want the pods' waiting reasons, not the cut-off read", err)
	}
}

func TestWaitForReadyReportsInitContainerWaitingReason(t *testing.T) {
	shortPolls(t)
	pod := pullingPod()
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "server-0", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "PodInitializing"}}}}
	pod.Status.InitContainerStatuses = []corev1.ContainerStatus{{Name: "istio-init", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull", Message: "pull access denied"}}}}
	client := fake.NewClientset(readyTestDeployment(0), pod)
	err := WaitForReady(client, readyTestParams(), 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "ErrImagePull: pull access denied") {
		t.Fatalf("WaitForReady() error = %v, want the init container's waiting reason", err)
	}
}
