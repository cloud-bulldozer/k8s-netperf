package k8s

import (
	"testing"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testCompute(cpu, memory string) corev1.ResourceList {
	return corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(memory)}
}

func computeParams(compute, extended corev1.ResourceList) DeploymentParams {
	return DeploymentParams{
		Name:             "server",
		Replicas:         1,
		Image:            "example.com/image",
		Labels:           map[string]string{"role": "server"},
		Commands:         [][]string{{"netserver", "-D"}, {"iperf3", "-s"}},
		Compute:          compute,
		ResourceRequests: extended,
	}
}

func TestNewDeploymentCompute(t *testing.T) {
	vf := corev1.ResourceList{"openshift.io/ens1f0": resource.MustParse("1")}
	testCases := []struct {
		name     string
		compute  corev1.ResourceList
		extended corev1.ResourceList
		want     corev1.ResourceList
	}{
		{name: "unset leaves resources empty"},
		{name: "compute only", compute: testCompute("2", "2Gi"), want: testCompute("2", "2Gi")},
		{name: "extended only", extended: vf, want: vf},
		{
			name:     "compute and extended merged",
			compute:  testCompute("500m", "512Mi"),
			extended: vf,
			want: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("512Mi"),
				"openshift.io/ens1f0": resource.MustParse("1"),
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			containers := newDeployment(computeParams(tc.compute, tc.extended)).Spec.Template.Spec.Containers
			if len(containers) != 2 {
				t.Fatalf("got %d containers, want 2", len(containers))
			}
			for _, c := range containers {
				for label, got := range map[string]corev1.ResourceList{"requests": c.Resources.Requests, "limits": c.Resources.Limits} {
					if len(got) != len(tc.want) {
						t.Errorf("container %s %s = %v, want %v", c.Name, label, got, tc.want)
						continue
					}
					for name, want := range tc.want {
						if q, ok := got[name]; !ok || q.Cmp(want) != 0 {
							t.Errorf("container %s %s[%s] = %v, want %v", c.Name, label, name, got[name], want)
						}
					}
				}
			}
		})
	}
}

func TestNewDeploymentComputeDoesNotAliasInputs(t *testing.T) {
	compute := testCompute("2", "2Gi")
	params := computeParams(compute, nil)
	container := newDeployment(params).Spec.Template.Spec.Containers[0]
	container.Resources.Limits[corev1.ResourceCPU] = resource.MustParse("8")
	if got := container.Resources.Requests[corev1.ResourceCPU]; got.Cmp(resource.MustParse("2")) != 0 {
		t.Errorf("changing limits changed requests to %v", got)
	}
	if got := compute[corev1.ResourceCPU]; got.Cmp(resource.MustParse("2")) != 0 {
		t.Errorf("changing limits changed the caller's compute list to %v", got)
	}
}

func TestDeploymentComputeMatches(t *testing.T) {
	vf := corev1.ResourceList{"openshift.io/ens1f0": resource.MustParse("1")}
	guaranteed := testCompute("2", "2Gi")
	testCases := []struct {
		name     string
		existing DeploymentParams
		dp       DeploymentParams
		match    bool
	}{
		{name: "both unset", existing: computeParams(nil, nil), dp: computeParams(nil, nil), match: true},
		{name: "same compute", existing: computeParams(guaranteed, nil), dp: computeParams(guaranteed, nil), match: true},
		{name: "equal quantities written differently", existing: computeParams(testCompute("2000m", "2048Mi"), nil), dp: computeParams(guaranteed, nil), match: true},
		{name: "extended resources do not affect the comparison", existing: computeParams(guaranteed, vf), dp: computeParams(guaranteed, nil), match: true},
		{name: "existing guaranteed, now unset", existing: computeParams(guaranteed, nil), dp: computeParams(nil, nil)},
		{name: "existing unset, now guaranteed", existing: computeParams(nil, nil), dp: computeParams(guaranteed, nil)},
		{name: "different cpu", existing: computeParams(testCompute("4", "2Gi"), nil), dp: computeParams(guaranteed, nil)},
		{name: "different memory", existing: computeParams(testCompute("2", "4Gi"), nil), dp: computeParams(guaranteed, nil)},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deploymentComputeMatches(newDeployment(tc.existing), tc.dp); got != tc.match {
				t.Errorf("deploymentComputeMatches() = %t, want %t", got, tc.match)
			}
		})
	}

	limitOnly := newDeployment(computeParams(guaranteed, nil))
	delete(limitOnly.Spec.Template.Spec.Containers[1].Resources.Requests, corev1.ResourceMemory)
	if deploymentComputeMatches(limitOnly, computeParams(guaranteed, nil)) {
		t.Error("deployment missing a memory request on one container matches")
	}
}

func TestVerifyGuaranteedQoS(t *testing.T) {
	pods := func(classes ...corev1.PodQOSClass) corev1.PodList {
		list := corev1.PodList{}
		for i, class := range classes {
			list.Items = append(list.Items, corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-" + string(rune('a'+i)), Namespace: "netperf"},
				Status:     corev1.PodStatus{QOSClass: class},
			})
		}
		return list
	}
	guaranteed := testCompute("2", "2Gi")

	if err := VerifyGuaranteedQoS(&config.PerfScenarios{Client: pods(corev1.PodQOSBestEffort)}); err != nil {
		t.Errorf("unset compute: error = %v, want nil", err)
	}
	all := &config.PerfScenarios{
		Compute:      guaranteed,
		Client:       pods(corev1.PodQOSGuaranteed),
		ClientAcross: pods(corev1.PodQOSGuaranteed),
		ClientHost:   pods(corev1.PodQOSGuaranteed),
		Server:       pods(corev1.PodQOSGuaranteed, corev1.PodQOSGuaranteed),
		ServerHost:   pods(corev1.PodQOSGuaranteed),
	}
	if err := VerifyGuaranteedQoS(all); err != nil {
		t.Errorf("all Guaranteed: error = %v, want nil", err)
	}
	for _, mutate := range []func(*config.PerfScenarios){
		func(s *config.PerfScenarios) { s.Client = pods(corev1.PodQOSBurstable) },
		func(s *config.PerfScenarios) { s.ClientAcross = pods(corev1.PodQOSBurstable) },
		func(s *config.PerfScenarios) { s.ClientHost = pods(corev1.PodQOSBurstable) },
		func(s *config.PerfScenarios) { s.Server = pods(corev1.PodQOSGuaranteed, corev1.PodQOSBurstable) },
		func(s *config.PerfScenarios) { s.ServerHost = pods(corev1.PodQOSBestEffort) },
	} {
		s := *all
		mutate(&s)
		if err := VerifyGuaranteedQoS(&s); err == nil {
			t.Errorf("non-Guaranteed pod accepted: %+v", s)
		}
	}
}
