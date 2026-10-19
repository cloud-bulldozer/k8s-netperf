package main

import (
	"testing"

	ocpmetadata "github.com/cloud-bulldozer/go-commons/v2/ocp-metadata"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/metrics"
)

func TestValidateAirGappedOptions(t *testing.T) {
	tests := []struct {
		name, image, vmImage, offline, wantNamespace, wantName string
		imageSet, vmImageSet, vm                               bool
		wantErr                                                bool
	}{
		{name: "image override is verbatim", image: "mirror.local/path/netperf:tag", imageSet: true, vm: true},
		{name: "offline DataVolume", offline: "operator-images/prepared", vm: true, wantNamespace: "operator-images", wantName: "prepared"},
		{name: "offline DataVolume requires VM", offline: "operator-images/prepared", wantErr: true},
		{name: "offline DataVolume conflicts with explicit VM image", vmImage: "mirror.local/vm:tag", vmImageSet: true, offline: "operator-images/prepared", vm: true, wantErr: true},
		{name: "malformed DataVolume", offline: "prepared", vm: true, wantErr: true},
		{name: "DataVolume has empty namespace", offline: "/prepared", vm: true, wantErr: true},
		{name: "DataVolume has empty name", offline: "operator-images/", vm: true, wantErr: true},
		{name: "DataVolume has extra path separator", offline: "operator-images/prepared/extra", vm: true, wantErr: true},
		{name: "benchmark namespace source allowed", offline: "netperf/prepared", vm: true, wantNamespace: "netperf", wantName: "prepared"},
		{name: "empty explicit image", imageSet: true, vm: true, wantErr: true},
		{name: "empty explicit VM image", vmImageSet: true, vm: true, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ns, name, err := validateAirGappedOptions(tc.image, tc.imageSet, tc.vmImage, tc.vmImageSet, tc.offline, tc.vm)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateAirGappedOptions() error = %v, wantErr %t", err, tc.wantErr)
			}
			if ns != tc.wantNamespace || name != tc.wantName {
				t.Errorf("DataVolume = %s/%s, want %s/%s", ns, name, tc.wantNamespace, tc.wantName)
			}
		})
	}
}

func TestApplyClusterDistributionSetsPrometheusFlags(t *testing.T) {
	testCases := []struct {
		name       string
		dist       string
		openShift  bool
		microShift bool
	}{
		{
			name:      "openshift",
			dist:      ocpmetadata.DistributionOpenShift,
			openShift: true,
		},
		{
			name:       "microshift",
			dist:       ocpmetadata.DistributionMicroShift,
			microShift: true,
		},
		{
			name: "kubernetes",
			dist: ocpmetadata.DistributionKubernetes,
		},
		{
			name: "unknown",
			dist: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pcon := metrics.PromConnect{}
			applyClusterDistribution(&pcon, tc.dist)
			if pcon.OpenShift != tc.openShift {
				t.Fatalf("OpenShift = %t, want %t", pcon.OpenShift, tc.openShift)
			}
			if pcon.MicroShift != tc.microShift {
				t.Fatalf("MicroShift = %t, want %t", pcon.MicroShift, tc.microShift)
			}
		})
	}
}

func TestShouldDiscoverPrometheus(t *testing.T) {
	testCases := []struct {
		name                   string
		dist                   string
		url                    string
		metadataAgentAvailable bool
		clusterInfoDegraded    bool
		want                   bool
	}{
		{
			name:                   "discover openshift without explicit prometheus url",
			dist:                   ocpmetadata.DistributionOpenShift,
			metadataAgentAvailable: true,
			want:                   true,
		},
		{
			name:                   "keep openshift discovery with explicit url for token",
			dist:                   ocpmetadata.DistributionOpenShift,
			url:                    "http://127.0.0.1:9090",
			metadataAgentAvailable: true,
			want:                   true,
		},
		{
			name:                   "skip microshift discovery without explicit url",
			dist:                   ocpmetadata.DistributionMicroShift,
			metadataAgentAvailable: true,
		},
		{
			name:                   "skip microshift discovery with explicit url",
			dist:                   ocpmetadata.DistributionMicroShift,
			url:                    "http://127.0.0.1:9090",
			metadataAgentAvailable: true,
		},
		{
			name:                   "skip kubernetes discovery without explicit url",
			dist:                   ocpmetadata.DistributionKubernetes,
			metadataAgentAvailable: true,
		},
		{
			name:                   "skip kubernetes discovery with explicit url",
			dist:                   ocpmetadata.DistributionKubernetes,
			url:                    "http://127.0.0.1:9090",
			metadataAgentAvailable: true,
		},
		{
			name:                   "skip discovery when metadata agent is unavailable",
			dist:                   ocpmetadata.DistributionOpenShift,
			metadataAgentAvailable: false,
		},
		{
			name:                   "discover when cluster info degraded despite explicit url",
			url:                    "http://127.0.0.1:9090",
			metadataAgentAvailable: true,
			clusterInfoDegraded:    true,
			want:                   true,
		},
		{
			name:                   "skip discovery with explicit url when distribution is unknown",
			url:                    "http://127.0.0.1:9090",
			metadataAgentAvailable: true,
		},
		{
			name:                   "discover without explicit url when distribution is unknown",
			metadataAgentAvailable: true,
			want:                   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldDiscoverPrometheus(tc.dist, tc.url, tc.metadataAgentAvailable, tc.clusterInfoDegraded)
			if got != tc.want {
				t.Fatalf("shouldDiscoverPrometheus(%q, %q, %t, %t) = %t, want %t", tc.dist, tc.url, tc.metadataAgentAvailable, tc.clusterInfoDegraded, got, tc.want)
			}
		})
	}
}
