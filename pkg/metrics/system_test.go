package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/common/model"
)

func TestExtractMTUReturnsLatestSample(t *testing.T) {
	value := model.Matrix{
		&model.SampleStream{
			Values: []model.SamplePair{
				{Value: model.SampleValue(1400)},
				{Value: model.SampleValue(1500)},
			},
		},
	}

	mtu, err := extractMTU(value)
	if err != nil {
		t.Fatalf("extractMTU returned unexpected error: %v", err)
	}
	if mtu != 1500 {
		t.Fatalf("extractMTU returned %d, expected 1500", mtu)
	}
}

func TestExtractMTUSkipsEmptyStreams(t *testing.T) {
	value := model.Matrix{
		nil,
		&model.SampleStream{},
		&model.SampleStream{
			Values: []model.SamplePair{
				{Value: model.SampleValue(9000)},
			},
		},
	}

	mtu, err := extractMTU(value)
	if err != nil {
		t.Fatalf("extractMTU returned unexpected error: %v", err)
	}
	if mtu != 9000 {
		t.Fatalf("extractMTU returned %d, expected 9000", mtu)
	}
}

func TestExtractMTURejectsEmptyResponse(t *testing.T) {
	testCases := []struct {
		name  string
		value model.Value
	}{
		{
			name:  "empty matrix",
			value: model.Matrix{},
		},
		{
			name: "streams without samples",
			value: model.Matrix{
				&model.SampleStream{},
				&model.SampleStream{Values: []model.SamplePair{}},
			},
		},
		{
			name:  "unexpected type",
			value: model.Vector{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := extractMTU(tc.value); err == nil {
				t.Fatal("extractMTU succeeded, expected an error")
			}
		})
	}
}

func TestDiscoverRejectsNilMetadataAgent(t *testing.T) {
	conn, ok := Discover(nil)
	if ok {
		t.Fatal("Discover(nil) returned ok=true, want false")
	}
	if conn != (PromConnect{}) {
		t.Fatalf("Discover(nil) returned %#v, want zero PromConnect", conn)
	}
}

func TestMicroShiftNodeDetailsUsesUnameQuery(t *testing.T) {
	query, ok := nodeDetailsQuery(PromConnect{MicroShift: true})
	if !ok {
		t.Fatal("nodeDetailsQuery returned ok=false, want true")
	}
	if query != "node_uname_info" {
		t.Fatalf("nodeDetailsQuery = %q, want node_uname_info", query)
	}
}

func TestMicroShiftNodeCPUQueryUsesNodeNameInstance(t *testing.T) {
	node := NodeInfo{IP: "192.0.2.10", NodeName: "microshift-node"}
	query := nodeCPUQuery(node, PromConnect{MicroShift: true})
	if !strings.Contains(query, `instance=~"microshift-node"`) {
		t.Fatalf("nodeCPUQuery = %q, want node name instance matcher", query)
	}
	if strings.Contains(query, node.IP) {
		t.Fatalf("nodeCPUQuery = %q, should not use node IP on MicroShift", query)
	}
}

func TestMicroShiftVSwitchQueriesUseNamedprocessMetrics(t *testing.T) {
	node := NodeInfo{NodeName: "microshift-node"}
	pcon := PromConnect{MicroShift: true}

	cpuQuery := vSwitchCPUQuery(node, pcon)
	if !strings.Contains(cpuQuery, "namedprocess_namegroup_cpu_seconds_total") {
		t.Fatalf("vSwitchCPUQuery = %q, want namedprocess cpu metric", cpuQuery)
	}
	if !strings.Contains(cpuQuery, `groupname=~"ovs-vswitchd"`) {
		t.Fatalf("vSwitchCPUQuery = %q, want ovs-vswitchd group matcher", cpuQuery)
	}

	memQuery := vSwitchMemQuery(node, pcon)
	if !strings.Contains(memQuery, "namedprocess_namegroup_memory_bytes") {
		t.Fatalf("vSwitchMemQuery = %q, want namedprocess memory metric", memQuery)
	}
	if !strings.Contains(memQuery, `memtype=~"resident"`) {
		t.Fatalf("vSwitchMemQuery = %q, want resident memory matcher", memQuery)
	}
}
