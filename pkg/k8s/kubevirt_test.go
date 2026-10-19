package k8s

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestNewVMIWorkloadOptions(t *testing.T) {
	testCases := []struct {
		name        string
		annotations map[string]string
		labels      map[string]string
	}{
		{name: "defaults preserved"},
		{name: "custom annotations and labels", annotations: map[string]string{"example.com/isolation": "enabled"}, labels: map[string]string{"example.com/team": "networking"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			vmi := newVMI("workload", mergeLabels(map[string]string{"app": "workload"}, tc.labels), "", corev1.PodAntiAffinity{}, corev1.NodeAffinity{}, "example.com/image", nil, nil, "", "", 2, 1, 1, tc.annotations)
			if got := vmi.Annotations["example.com/isolation"]; got != tc.annotations["example.com/isolation"] {
				t.Errorf("annotation = %q, want %q", got, tc.annotations["example.com/isolation"])
			}
			if got := vmi.Labels["example.com/team"]; got != tc.labels["example.com/team"] {
				t.Errorf("label = %q, want %q", got, tc.labels["example.com/team"])
			}
			if got := vmi.Labels["app"]; got != "workload" {
				t.Errorf("managed label app = %q, want workload", got)
			}
			if got := vmi.Spec.Domain.CPU.Sockets; got != 2 {
				t.Errorf("CPU sockets = %d, want 2", got)
			}
		})
	}
}
