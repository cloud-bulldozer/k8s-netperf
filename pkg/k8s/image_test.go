package k8s

import (
	"testing"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	corev1 "k8s.io/api/core/v1"
)

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestImagePullPolicy(t *testing.T) {
	testCases := []struct {
		image string
		want  corev1.PullPolicy
	}{
		{image: DefaultImage, want: corev1.PullAlways},
		{image: "quay.io/cloud-bulldozer/k8s-netperf", want: corev1.PullAlways},
		{image: "registry.example.com:5000/bench/k8s-netperf:v1", want: corev1.PullAlways},
		{image: "quay.io/cloud-bulldozer/k8s-netperf@" + testDigest, want: corev1.PullIfNotPresent},
		{image: "quay.io/cloud-bulldozer/k8s-netperf:latest@" + testDigest, want: corev1.PullIfNotPresent},
	}
	for _, tc := range testCases {
		if got := imagePullPolicy(tc.image); got != tc.want {
			t.Errorf("imagePullPolicy(%q) = %s, want %s", tc.image, got, tc.want)
		}
	}
}

func TestBenchmarkImage(t *testing.T) {
	if got := benchmarkImage(&config.PerfScenarios{}); got != DefaultImage {
		t.Errorf("benchmarkImage(unset) = %q, want %q", got, DefaultImage)
	}
	pinned := "quay.io/cloud-bulldozer/k8s-netperf@" + testDigest
	if got := benchmarkImage(&config.PerfScenarios{Image: pinned}); got != pinned {
		t.Errorf("benchmarkImage(%q) = %q", pinned, got)
	}
}

func TestNewDeploymentImage(t *testing.T) {
	testCases := []struct {
		name       string
		image      string
		wantPolicy corev1.PullPolicy
	}{
		{name: "default tag keeps pulling", image: DefaultImage, wantPolicy: corev1.PullAlways},
		{name: "digest pulls only if missing", image: "quay.io/cloud-bulldozer/k8s-netperf@" + testDigest, wantPolicy: corev1.PullIfNotPresent},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			deployment := newDeployment(DeploymentParams{
				Name:     "server",
				Replicas: 1,
				Image:    tc.image,
				Labels:   map[string]string{"role": "server"},
				Commands: [][]string{{"netserver", "-D"}, {"iperf3", "-s"}},
			})
			containers := deployment.Spec.Template.Spec.Containers
			if len(containers) != 2 {
				t.Fatalf("got %d containers, want 2", len(containers))
			}
			for _, c := range containers {
				if c.Image != tc.image || c.ImagePullPolicy != tc.wantPolicy {
					t.Errorf("container %s: image %q policy %s, want %q %s", c.Name, c.Image, c.ImagePullPolicy, tc.image, tc.wantPolicy)
				}
			}
		})
	}
}

func TestDeploymentTemplateImageMatches(t *testing.T) {
	pinned := "quay.io/cloud-bulldozer/k8s-netperf@" + testDigest
	params := func(image string) DeploymentParams {
		return DeploymentParams{
			Name:     "server",
			Replicas: 1,
			Image:    image,
			Labels:   map[string]string{"role": "server"},
			Commands: [][]string{{"netserver", "-D"}, {"iperf3", "-s"}},
		}
	}

	if !deploymentTemplateImageMatches(newDeployment(params(pinned)), params(pinned)) {
		t.Error("deployment built for the requested image does not match it")
	}
	if !deploymentTemplateImageMatches(newDeployment(params(DefaultImage)), params(DefaultImage)) {
		t.Error("deployment built for the default image does not match it")
	}
	if deploymentTemplateImageMatches(newDeployment(params(DefaultImage)), params(pinned)) {
		t.Error("deployment running the default image matches a pinned --image")
	}
	if deploymentTemplateImageMatches(newDeployment(params(pinned)), params(DefaultImage)) {
		t.Error("deployment running a pinned image matches the default image")
	}

	stalePolicy := newDeployment(params(pinned))
	stalePolicy.Spec.Template.Spec.Containers[1].ImagePullPolicy = corev1.PullAlways
	if deploymentTemplateImageMatches(stalePolicy, params(pinned)) {
		t.Error("deployment with a different pull policy on one container matches")
	}

	mixed := newDeployment(params(pinned))
	mixed.Spec.Template.Spec.Containers[1].Image = DefaultImage
	if deploymentTemplateImageMatches(mixed, params(pinned)) {
		t.Error("deployment with a different image on one container matches")
	}

	empty := newDeployment(params(pinned))
	empty.Spec.Template.Spec.Containers = nil
	if deploymentTemplateImageMatches(empty, params(pinned)) {
		t.Error("deployment without containers matches")
	}
}
