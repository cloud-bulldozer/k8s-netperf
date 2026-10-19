package main

import (
	"testing"

	ocpmetadata "github.com/cloud-bulldozer/go-commons/v2/ocp-metadata"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/metrics"
)

func TestParseAnnotations(t *testing.T) {
	testCases := []struct {
		name    string
		values  []string
		want    map[string]string
		wantErr bool
	}{
		{name: "multiple annotations", values: []string{"example.com/one=value", "example.com/two=value=with=equals"}, want: map[string]string{"example.com/one": "value", "example.com/two": "value=with=equals"}},
		{name: "missing separator", values: []string{"example.com/key"}, wantErr: true},
		{name: "empty key", values: []string{"=value"}, wantErr: true},
		{name: "empty value", values: []string{"example.com/key="}, wantErr: true},
		{name: "whitespace-only key", values: []string{"   =value"}, wantErr: true},
		{name: "whitespace-only value", values: []string{"example.com/key=   "}, wantErr: true},
		{name: "malformed key", values: []string{"not a key=value"}, wantErr: true},
		{name: "duplicate key", values: []string{"example.com/key=one", "example.com/key=two"}, wantErr: true},
		{name: "managed istio key", values: []string{"sidecar.istio.io/inject=false"}, wantErr: true},
		{name: "managed network key", values: []string{"k8s.v1.cni.cncf.io/networks=netperf/network"}, wantErr: true},
		{name: "managed generated network status key", values: []string{"k8s.v1.cni.cncf.io/network-status=netperf/network"}, wantErr: true},
		{name: "managed ovn network key", values: []string{"k8s.ovn.org/pod-networks=netperf/network"}, wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAnnotations(tc.values)
			if tc.wantErr {
				if err == nil {
					t.Fatal("parseAnnotations() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAnnotations() error = %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("parseAnnotations() = %#v, want %#v", got, tc.want)
			}
			for key, want := range tc.want {
				if got[key] != want {
					t.Errorf("parseAnnotations()[%q] = %q, want %q", key, got[key], want)
				}
			}
		})
	}
}

func TestParseLabels(t *testing.T) {
	testCases := []struct {
		name    string
		values  []string
		want    map[string]string
		wantErr bool
	}{
		{name: "multiple labels", values: []string{"example.com/team=networking", "environment=test"}, want: map[string]string{"example.com/team": "networking", "environment": "test"}},
		{name: "missing separator", values: []string{"example.com/team"}, wantErr: true},
		{name: "empty key", values: []string{"=value"}, wantErr: true},
		{name: "empty value", values: []string{"example.com/team="}, wantErr: true},
		{name: "invalid key", values: []string{"not a key=value"}, wantErr: true},
		{name: "invalid value", values: []string{"example.com/team=not a value"}, wantErr: true},
		{name: "duplicate key", values: []string{"example.com/team=one", "example.com/team=two"}, wantErr: true},
		{name: "managed role selector", values: []string{"role=server"}, wantErr: true},
		{name: "managed app selector", values: []string{"app=server"}, wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLabels(tc.values)
			if tc.wantErr {
				if err == nil {
					t.Fatal("parseLabels() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLabels() error = %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("parseLabels() = %#v, want %#v", got, tc.want)
			}
			for key, want := range tc.want {
				if got[key] != want {
					t.Errorf("parseLabels()[%q] = %q, want %q", key, got[key], want)
				}
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
