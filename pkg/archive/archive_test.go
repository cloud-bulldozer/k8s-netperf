package archive

import (
	"testing"

	result "github.com/cloud-bulldozer/k8s-netperf/pkg/results"
)

func TestBuildDocsMapsCPUCollectionFlags(t *testing.T) {
	docs, err := BuildDocs(result.ScenarioResults{
		Results: []result.Data{
			{
				Driver:             "netperf",
				ClientCPUCollected: false,
				ServerCPUCollected: true,
				ThroughputSummary:  []float64{1},
				LatencySummary:     []float64{2},
				LossSummary:        []float64{0},
				RetransmitSummary:  []float64{0},
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
	if doc.ClientCPUCollected {
		t.Fatal("Doc.ClientCPUCollected = true, want false")
	}
	if !doc.ServerCPUCollected {
		t.Fatal("Doc.ServerCPUCollected = false, want true")
	}
}
