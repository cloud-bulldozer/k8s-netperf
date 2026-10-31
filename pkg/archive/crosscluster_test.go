package archive

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/metrics"
	result "github.com/cloud-bulldozer/k8s-netperf/pkg/results"
)

func xcRow(target string) result.Data {
	return result.Data{
		Config:             config.Config{Profile: "TCP_STREAM", Duration: 5, Samples: 1, MessageSize: 1024, Parallelism: 1, Service: target == result.CrossClusterTargetServiceDNS},
		Driver:             "netperf",
		Service:            target == result.CrossClusterTargetServiceDNS,
		Metric:             "Mb/s",
		ThroughputSummary:  []float64{100},
		LatencySummary:     []float64{10},
		LossSummary:        []float64{0},
		RetransmitSummary:  []float64{0},
		CrossCluster:       target != "",
		CrossClusterTarget: target,
		ClientPodCPU:       metrics.PodValues{Results: []metrics.PodCPU{{Name: "client-pod", Value: 1}}},
		ServerPodCPU:       metrics.PodValues{Results: []metrics.PodCPU{{Name: "server-pod", Value: 2}}},
	}
}

func xcResults(rows ...result.Data) result.ScenarioResults {
	return result.ScenarioResults{
		Results:       rows,
		ServerCluster: &result.ServerCluster{APIServer: "https://api.b:6443", Domain: "b.remote"},
	}
}

func docMaps(t *testing.T, sr result.ScenarioResults) []map[string]any {
	t.Helper()
	docs, err := BuildDocs(sr, "u")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(docs)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBuildDocsCrossClusterFields(t *testing.T) {
	same := docMaps(t, result.ScenarioResults{Results: []result.Data{xcRow("")}})[0]
	for key, want := range map[string]any{"crossCluster": false, "crossClusterTarget": ""} {
		got, ok := same[key]
		if !ok || got != want {
			t.Errorf("same-cluster %s = %v (present %t), want %v", key, got, ok, want)
		}
	}
	if v, ok := same["serverCluster"]; !ok || v != nil {
		t.Errorf("same-cluster serverCluster = %v (present %t), want null", v, ok)
	}

	cross := docMaps(t, xcResults(xcRow(result.CrossClusterTargetPodIP), xcRow(result.CrossClusterTargetServiceDNS)))
	for i, target := range []string{"podIP", "serviceDNS"} {
		d := cross[i]
		if d["crossCluster"] != true || d["crossClusterTarget"] != target || d["service"] != (target == "serviceDNS") {
			t.Errorf("doc %d: crossCluster=%v target=%v service=%v", i, d["crossCluster"], d["crossClusterTarget"], d["service"])
		}
		sc, _ := d["serverCluster"].(map[string]any)
		if sc["apiServer"] != "https://api.b:6443" || sc["domain"] != "b.remote" {
			t.Errorf("doc %d serverCluster = %v", i, d["serverCluster"])
		}
	}
}

func readCSVs(t *testing.T, dir, pattern string) [][]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil || len(files) != 1 {
		t.Fatalf("files for %s: %v %v", pattern, files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatalf("%s: %v", files[0], err)
	}
	return rows
}

func column(header []string, name string) int {
	for i, h := range header {
		if h == name {
			return i
		}
	}
	return -1
}

func TestCSVWritersCrossClusterColumns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sr     result.ScenarioResults
		want   []string // Cross Cluster, Cross Cluster Target, Server Cluster of the first row
		prefix int      // leading columns before the common fields
	}{
		{name: "same cluster", sr: result.ScenarioResults{Results: []result.Data{xcRow("")}, ClientPromEligible: true, ServerPromEligible: true}, want: []string{"false", "", ""}},
		{name: "cross cluster", sr: func() result.ScenarioResults {
			sr := xcResults(xcRow(result.CrossClusterTargetServiceDNS))
			sr.ClientPromEligible, sr.ServerPromEligible = true, true
			sr.Results[0].ClientCPUCollected, sr.Results[0].ServerCPUCollected = true, true
			sr.Results[0].ClientVSwitchCollected, sr.Results[0].ServerVSwitchCollected = true, true
			return sr
		}(), want: []string{"true", "serviceDNS", "https://api.b:6443"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if err := WriteCSVResult(tc.sr); err != nil {
				t.Fatal(err)
			}
			if err := WriteSpecificCSV(tc.sr); err != nil {
				t.Fatal(err)
			}
			if err := WritePromCSVResult(tc.sr); err != nil {
				t.Fatal(err)
			}
			for _, pattern := range []string{"result-*.csv", "loss-rt-result-*.csv", "cpu-result-*.csv", "podcpu-result-*.csv", "podmem-result-*.csv", "vswitch-result-*.csv"} {
				rows := readCSVs(t, dir, pattern)
				header := rows[0]
				ext := column(header, "External Server")
				if ext < 0 || column(header, "Cross Cluster") != ext+1 || column(header, "Cross Cluster Target") != ext+2 || column(header, "Server Cluster") != ext+3 || column(header, "UDN Info") != ext+4 {
					t.Fatalf("%s header = %v", pattern, header)
				}
				for _, row := range rows[1:] {
					if len(row) != len(header) {
						t.Fatalf("%s row has %d fields, header %d: %v", pattern, len(row), len(header), row)
					}
					got := []string{row[ext+1], row[ext+2], row[ext+3]}
					if got[0] != tc.want[0] || got[1] != tc.want[1] || got[2] != tc.want[2] {
						t.Errorf("%s cross-cluster fields = %v, want %v", pattern, got, tc.want)
					}
				}
			}
		})
	}
}

func roles(t *testing.T, dir, pattern string) []string {
	t.Helper()
	var out []string
	for _, row := range readCSVs(t, dir, pattern)[1:] {
		out = append(out, row[0])
	}
	return out
}

func TestPromCSVRowsOmitUncollectedSides(t *testing.T) {
	join := func(s []string) string {
		b, _ := json.Marshal(s)
		return string(b)
	}
	testCases := []struct {
		name                             string
		sr                               result.ScenarioResults
		wantCPU, wantVSwitch, wantPodCPU string
	}{
		{
			name:    "same cluster keeps every row even when not collected",
			sr:      result.ScenarioResults{Results: []result.Data{xcRow("")}},
			wantCPU: `["Client","Server"]`, wantVSwitch: `["Client","Server"]`, wantPodCPU: `["Client","Server"]`,
		},
		{
			name: "server side not eligible",
			sr: func() result.ScenarioResults {
				sr := xcResults(xcRow(result.CrossClusterTargetPodIP))
				sr.ClientPromEligible = true
				sr.Results[0].ClientCPUCollected, sr.Results[0].ClientVSwitchCollected = true, true
				return sr
			}(),
			wantCPU: `["Client"]`, wantVSwitch: `["Client"]`, wantPodCPU: `["Client"]`,
		},
		{
			name: "attribution guard rejected both sides",
			sr: func() result.ScenarioResults {
				sr := xcResults(xcRow(result.CrossClusterTargetPodIP))
				sr.Results[0].ClientCPUCollected, sr.Results[0].ServerCPUCollected = true, true
				return sr
			}(),
			wantCPU: `null`, wantVSwitch: `null`, wantPodCPU: `null`,
		},
		{
			name: "failed node CPU and empty vSwitch queries",
			sr: func() result.ScenarioResults {
				sr := xcResults(xcRow(result.CrossClusterTargetPodIP))
				sr.ClientPromEligible, sr.ServerPromEligible = true, true
				sr.Results[0].ServerCPUCollected = true
				sr.Results[0].ClientVSwitchCollected = true
				return sr
			}(),
			wantCPU: `["Server"]`, wantVSwitch: `["Client"]`, wantPodCPU: `["Client","Server"]`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if err := WritePromCSVResult(tc.sr); err != nil {
				t.Fatal(err)
			}
			if got := join(roles(t, dir, "cpu-result-*.csv")); got != tc.wantCPU {
				t.Errorf("cpu rows = %s, want %s", got, tc.wantCPU)
			}
			if got := join(roles(t, dir, "vswitch-result-*.csv")); got != tc.wantVSwitch {
				t.Errorf("vswitch rows = %s, want %s", got, tc.wantVSwitch)
			}
			if got := join(roles(t, dir, "podcpu-result-*.csv")); got != tc.wantPodCPU {
				t.Errorf("podcpu rows = %s, want %s", got, tc.wantPodCPU)
			}
		})
	}
}
