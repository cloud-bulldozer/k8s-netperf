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
