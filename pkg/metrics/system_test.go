package metrics

import (
	"errors"
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

func TestExtractNodeCPUReturnsModeAverages(t *testing.T) {
	value := model.Matrix{
		&model.SampleStream{
			Metric: model.Metric{"mode": "idle"},
			Values: []model.SamplePair{
				{Value: model.SampleValue(10)},
				{Value: model.SampleValue(30)},
			},
		},
		&model.SampleStream{
			Metric: model.Metric{"mode": "user"},
			Values: []model.SamplePair{
				{Value: model.SampleValue(4)},
				{Value: model.SampleValue(6)},
			},
		},
	}

	cpu, err := extractNodeCPU(value, "node-a")
	if err != nil {
		t.Fatalf("extractNodeCPU returned unexpected error: %v", err)
	}
	if cpu.Idle != 20 {
		t.Fatalf("extractNodeCPU idle = %f, expected 20", cpu.Idle)
	}
	if cpu.User != 5 {
		t.Fatalf("extractNodeCPU user = %f, expected 5", cpu.User)
	}
}

func TestExtractNodeCPUUsesExactModeLabels(t *testing.T) {
	value := model.Matrix{
		&model.SampleStream{
			Metric: model.Metric{"mode": "nice"},
			Values: []model.SamplePair{
				{Value: model.SampleValue(5)},
			},
		},
		&model.SampleStream{
			Metric: model.Metric{"mode": "guest_nice"},
			Values: []model.SamplePair{
				{Value: model.SampleValue(99)},
			},
		},
		&model.SampleStream{
			Metric: model.Metric{"mode": "irq"},
			Values: []model.SamplePair{
				{Value: model.SampleValue(7)},
			},
		},
	}

	cpu, err := extractNodeCPU(value, "node-a")
	if err != nil {
		t.Fatalf("extractNodeCPU returned unexpected error: %v", err)
	}
	if cpu.Nice != 5 {
		t.Fatalf("extractNodeCPU nice = %f, expected 5", cpu.Nice)
	}
	if cpu.Irq != 7 {
		t.Fatalf("extractNodeCPU irq = %f, expected 7", cpu.Irq)
	}
}

func TestExtractNodeCPURejectsMissingUsableSamples(t *testing.T) {
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
			name: "series without a known cpu mode",
			value: model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{"mode": "guest"},
					Values: []model.SamplePair{
						{Value: model.SampleValue(1)},
					},
				},
			},
		},
		{
			name: "series without an exact known cpu mode",
			value: model.Matrix{
				&model.SampleStream{
					Metric: model.Metric{"mode": "guest_nice"},
					Values: []model.SamplePair{
						{Value: model.SampleValue(1)},
					},
				},
			},
		},
		{
			name:  "unexpected type",
			value: model.Vector{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := extractNodeCPU(tc.value, "node-a"); err == nil {
				t.Fatal("extractNodeCPU succeeded, expected an error")
			}
		})
	}
}

func TestQueryPrometheusWithRetrySucceedsAfterTransientErrors(t *testing.T) {
	attempts := 0
	expected := NodeCPU{Idle: 42}

	cpu, err := queryPrometheusWithRetry("test query", 3, 0, func() (NodeCPU, error) {
		attempts++
		if attempts < 3 {
			return NodeCPU{}, errors.New("transient failure")
		}
		return expected, nil
	})
	if err != nil {
		t.Fatalf("queryPrometheusWithRetry returned unexpected error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("queryPrometheusWithRetry attempted %d times, want 3", attempts)
	}
	if cpu != expected {
		t.Fatalf("queryPrometheusWithRetry returned %#v, want %#v", cpu, expected)
	}
}

func TestQueryPrometheusWithRetryReturnsLastError(t *testing.T) {
	attempts := 0
	queryErrs := []error{
		errors.New("first failure"),
		errors.New("second failure"),
		errors.New("last failure"),
	}

	cpu, err := queryPrometheusWithRetry("test query", 3, 0, func() (NodeCPU, error) {
		err := queryErrs[attempts]
		attempts++
		return NodeCPU{}, err
	})
	if err != queryErrs[2] {
		t.Fatalf("queryPrometheusWithRetry returned error %v, want %v", err, queryErrs[2])
	}
	if attempts != 3 {
		t.Fatalf("queryPrometheusWithRetry attempted %d times, want 3", attempts)
	}
	if cpu != (NodeCPU{}) {
		t.Fatalf("queryPrometheusWithRetry returned %#v, want zero NodeCPU", cpu)
	}
}
