package archive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestWriteJSONResultArchivesCompletePayload(t *testing.T) {
	outputDir := t.TempDir()
	var stdout bytes.Buffer
	results := result.ScenarioResults{
		Results: []result.Data{
			{
				Driver:            "netperf",
				ThroughputSummary: []float64{100, 200},
				LatencySummary:    []float64{10, 20},
				LossSummary:       []float64{1, 3},
				RetransmitSummary: []float64{4, 8},
			},
		},
	}

	if err := writeJSONResult(results, &stdout, outputDir); err != nil {
		t.Fatalf("writeJSONResult returned unexpected error: %v", err)
	}

	files, err := filepath.Glob(filepath.Join(outputDir, "result-*.json"))
	if err != nil {
		t.Fatalf("glob JSON result archive: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("found %d JSON result archives, want 1", len(files))
	}
	archived, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read JSON result archive: %v", err)
	}
	if !bytes.Equal(archived, stdout.Bytes()) {
		t.Fatal("archived JSON does not match stdout")
	}

	var docs []Doc
	if err := json.Unmarshal(archived, &docs); err != nil {
		t.Fatalf("unmarshal JSON result archive: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("decoded %d docs, want 1", len(docs))
	}
	if docs[0].TCPRetransmit != 6 {
		t.Fatalf("TCPRetransmit = %v, want 6", docs[0].TCPRetransmit)
	}
	if docs[0].UDPLossPercent != 2 {
		t.Fatalf("UDPLossPercent = %v, want 2", docs[0].UDPLossPercent)
	}
	if docs[0].Throughput != 150 {
		t.Fatalf("Throughput = %v, want 150", docs[0].Throughput)
	}
	if docs[0].Latency != 15 {
		t.Fatalf("Latency = %v, want 15", docs[0].Latency)
	}
}
