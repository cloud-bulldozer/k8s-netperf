package archive

import (
	"testing"

	ocpmetadata "github.com/cloud-bulldozer/go-commons/v2/ocp-metadata"
	result "github.com/cloud-bulldozer/k8s-netperf/pkg/results"
)

func TestBuildDocsArchivesFullClusterMetadata(t *testing.T) {
	metadata := ocpmetadata.ClusterMetadata{
		Distribution:           ocpmetadata.DistributionMicroShift,
		MicroShift:             true,
		MicroShiftVersion:      "4.19.0",
		MicroShiftMajorVersion: "4.19",
		K8SVersion:             "v1.31.0",
		ClusterName:            "edge-cluster",
		SDNType:                "ovn-kubernetes",
		TotalNodes:             1,
	}

	docs, err := BuildDocs(result.ScenarioResults{
		Metadata: result.Metadata{ClusterMetadata: metadata},
		Results: []result.Data{
			{
				Driver:            "netperf",
				ThroughputSummary: []float64{1},
				LatencySummary:    []float64{2},
				LossSummary:       []float64{0},
				RetransmitSummary: []float64{0},
			},
		},
	}, "test-uuid")
	if err != nil {
		t.Fatalf("BuildDocs returned unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("BuildDocs returned %d docs, want 1", len(docs))
	}

	doc, ok := docs[0].(Doc)
	if !ok {
		t.Fatalf("BuildDocs returned %T, want archive.Doc", docs[0])
	}
	if doc.Metadata.ClusterMetadata != metadata {
		t.Fatalf("Metadata = %#v, want %#v", doc.Metadata.ClusterMetadata, metadata)
	}
}

func TestBuildDocsArchivesResultBooleans(t *testing.T) {
	tests := []struct {
		name        string
		result      result.Data
		wantLocal   bool
		wantAcross  bool
		wantService bool
		wantHostNet bool
	}{
		{
			name: "all true",
			result: result.Data{
				Driver:            "netperf",
				SameNode:          true,
				AcrossAZ:          true,
				Service:           true,
				HostNetwork:       true,
				ThroughputSummary: []float64{1},
				LatencySummary:    []float64{2},
				LossSummary:       []float64{0},
				RetransmitSummary: []float64{0},
			},
			wantLocal:   true,
			wantAcross:  true,
			wantService: true,
			wantHostNet: true,
		},
		{
			name: "all false",
			result: result.Data{
				Driver:            "netperf",
				ThroughputSummary: []float64{1},
				LatencySummary:    []float64{2},
				LossSummary:       []float64{0},
				RetransmitSummary: []float64{0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docs, err := BuildDocs(result.ScenarioResults{
				Results: []result.Data{tt.result},
			}, "test-uuid")
			if err != nil {
				t.Fatalf("BuildDocs returned unexpected error: %v", err)
			}
			if len(docs) != 1 {
				t.Fatalf("BuildDocs returned %d docs, want 1", len(docs))
			}

			doc, ok := docs[0].(Doc)
			if !ok {
				t.Fatalf("BuildDocs returned %T, want archive.Doc", docs[0])
			}
			if doc.Local != tt.wantLocal {
				t.Fatalf("Local = %t, want %t", doc.Local, tt.wantLocal)
			}
			if doc.AcrossAZ != tt.wantAcross {
				t.Fatalf("AcrossAZ = %t, want %t", doc.AcrossAZ, tt.wantAcross)
			}
			if doc.Service != tt.wantService {
				t.Fatalf("Service = %t, want %t", doc.Service, tt.wantService)
			}
			if doc.HostNetwork != tt.wantHostNet {
				t.Fatalf("HostNetwork = %t, want %t", doc.HostNetwork, tt.wantHostNet)
			}
		})
	}
}
