package result

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	math "github.com/aclements/go-moremath/stats"
	ocpmeta "github.com/cloud-bulldozer/go-commons/ocp-metadata"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/metrics"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/sample"
	stats "github.com/montanaflynn/stats"
	"github.com/olekukonko/tablewriter"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// Specify Language specific case wrapper as global variable
var caser = cases.Title(language.English)

// Data describes the result data
type Data struct {
	config.Config
	Driver            string
	Metric            string
	SameNode          bool
	HostNetwork       bool
	ClientNodeInfo    metrics.NodeInfo
	ServerNodeInfo    metrics.NodeInfo
	Sample            sample.Sample
	StartTime         time.Time
	EndTime           time.Time
	Service           bool
	AcrossAZ          bool
	ThroughputSummary []float64
	LatencySummary    []float64
	LossSummary       []float64
	RetransmitSummary []float64
	ClientMetrics     metrics.NodeCPU
	ServerMetrics     metrics.NodeCPU
	ClientPodCPU      metrics.PodValues
	ClientPodMem      metrics.PodValues
	ServerPodCPU      metrics.PodValues
	ServerPodMem      metrics.PodValues
}

// ScenarioResults each scenario could have multiple results
type ScenarioResults struct {
	Results   []Data
	Virt      bool
	Version   string
	GitCommit string
	Metadata
}

// Metadata for the run
type Metadata struct {
	ocpmeta.ClusterMetadata
	Kernel          string `json:"kernel"`
	OCPShortVersion string `json:"ocpShortVersion"`
	MTU             int    `json:"mtu"`
}

// Average accepts array of floats to calculate average
func Average(vals []float64) (float64, error) {
	return stats.Mean(vals)
}

// Percentile accepts array of floats and the desired %tile to calculate
func Percentile(vals []float64, ptile float64) (float64, error) {
	return stats.Percentile(vals, ptile)
}

// Confidence accepts array of floats to calculate average
func ConfidenceInterval(vals []float64, ci float64) (float64, float64, float64) {
	return math.MeanCI(vals, ci)
}

// CheckResults will check to see if there are results with a specific Profile like TCP_STREAM
// returns true if there are results with provided string
func checkResults(s ScenarioResults, check string) bool {
	for t := range s.Results {
		if strings.Contains(s.Results[t].Profile, check) {
			return true
		}
	}
	return false
}

// CheckHostResults will check to see if there are hostNet results
// returns true if there are results with hostNetwork
func CheckHostResults(s ScenarioResults) bool {
	for t := range s.Results {
		if s.Results[t].HostNetwork {
			return true
		}
	}
	return false
}

type DiffData struct {
	MessageSize int
	Streams     int
	HostPerf    float64
	PodPerf     float64
}

type Diff struct {
	MessageSize int
	Result      float64
	Streams     int
}

// TCPThroughputDiff accepts the Scenario Results and calculates the %diff.
// returns
func TCPThroughputDiff(s *ScenarioResults) ([]Diff, error) {
	// We will focus on TCP STREAM
	diffRes := []DiffData{}
	for _, t := range s.Results {
		if t.Profile == "TCP_STREAM" {
			hostPerf := 0.0
			podPerf := 0.0
			diff := DiffData{}
			if !t.Service {
				if t.HostNetwork {
					hostPerf, _ = Average(t.ThroughputSummary)
					diff.MessageSize = t.MessageSize
					diff.HostPerf = hostPerf
					diff.Streams = t.Parallelism
				} else {
					podPerf, _ = Average(t.ThroughputSummary)
					diff.MessageSize = t.MessageSize
					diff.PodPerf = podPerf
					diff.Streams = t.Parallelism
				}
				diffRes = append(diffRes, diff)
			}
		}
	}
	res := []Diff{}
	for _, msg := range s.Results {
		if !msg.Service && msg.Parallelism == 1 && msg.HostNetwork && msg.Profile == "TCP_STREAM" {
			r := Diff{
				Result:      doPerfDiff(&diffRes, msg.Config.MessageSize, 1),
				MessageSize: msg.Config.MessageSize,
				Streams:     msg.Parallelism,
			}
			res = append(res, r)
		}
	}
	return res, nil
}

func doPerfDiff(diff *[]DiffData, msg int, streams int) float64 {
	host := 0.0
	pod := 0.0
	for _, d := range *diff {
		if d.MessageSize == msg && d.Streams == streams {
			if d.HostPerf > 0.0 {
				host = d.HostPerf
			}
			if d.PodPerf > 0.0 {
				pod = d.PodPerf
			}
		}
	}
	logging.Debugf("Message Size %d : PodNetwork throughput %f, HostNetwork throughput %f for %d streams", msg, pod, host, streams)
	return calDiff(host, pod)
}

// Method to init common table structure.
func initTable(header []string) *tablewriter.Table {
	// Create a new table writer with the appropriate header and alignment options
	table := tablewriter.NewWriter(os.Stdout)
	// Add a header to the table
	table.SetHeader(header)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetAutoWrapText(false)
	return table
}

// calDiff will determine the %diff between two values.
// returns a float64 which is the %diff
func calDiff(a float64, b float64) float64 {
	return (a - b) / ((a + b) / 2) * 100
}

// ShowPodCPU accepts ScenarioResults and presents to the user via stdout the PodCPU info
func ShowPodCPU(s ScenarioResults) {
	table := initTable([]string{"Result Type", "Driver", "Role", "Scenario", "Parallelism", "Host Network", "Service", "Message Size", "Burst", "Same node", "Pod", "Utilization"})
	for _, r := range s.Results {
		for _, pod := range r.ClientPodCPU.Results {
			table.Append([]string{"Pod CPU Utilization", r.Driver, "Client", r.Profile, fmt.Sprintf("%d", r.Parallelism), fmt.Sprintf("%t", r.HostNetwork), fmt.Sprintf("%t", r.Service), fmt.Sprintf("%d", r.MessageSize), fmt.Sprintf("%d", r.Burst), fmt.Sprintf("%t", r.SameNode), fmt.Sprintf("%.20s", pod.Name), fmt.Sprintf("%f", pod.Value)})
		}
		for _, pod := range r.ServerPodCPU.Results {
			table.Append([]string{"Pod CPU Utilization", r.Driver, "Server", r.Profile, fmt.Sprintf("%d", r.Parallelism), fmt.Sprintf("%t", r.HostNetwork), fmt.Sprintf("%t", r.Service), fmt.Sprintf("%d", r.MessageSize), fmt.Sprintf("%d", r.Burst), fmt.Sprintf("%t", r.SameNode), fmt.Sprintf("%.20s", pod.Name), fmt.Sprintf("%f", pod.Value)})
		}
	}
	table.Render()
}

// ShowPodMem accepts ScenarioResults and presents to the user via stdout the Podmem info
func ShowPodMem(s ScenarioResults) {
	table := initTable([]string{"Result Type", "Driver", "Role", "Scenario", "Parallelism", "Host Network", "Service", "Message Size", "Burst", "Same node", "Pod", "Utilization"})
	for _, r := range s.Results {
		for _, pod := range r.ClientPodMem.MemResults {
			table.Append([]string{"Pod Mem RSS Utilization", r.Driver, "Client", r.Profile, fmt.Sprintf("%d", r.Parallelism), fmt.Sprintf("%t", r.HostNetwork), fmt.Sprintf("%t", r.Service), fmt.Sprintf("%d", r.MessageSize), fmt.Sprintf("%d", r.Burst), fmt.Sprintf("%t", r.SameNode), fmt.Sprintf("%.20s", pod.Name), fmt.Sprintf("%f", pod.Value)})
		}
		for _, pod := range r.ServerPodMem.MemResults {
			table.Append([]string{"Pod Mem RSS Utilization", r.Driver, "Server", r.Profile, fmt.Sprintf("%d", r.Parallelism), fmt.Sprintf("%t", r.HostNetwork), fmt.Sprintf("%t", r.Service), fmt.Sprintf("%d", r.MessageSize), fmt.Sprintf("%d", r.Burst), fmt.Sprintf("%t", r.SameNode), fmt.Sprintf("%.20s", pod.Name), fmt.Sprintf("%f", pod.Value)})
		}
	}
	table.Render()
}

// ShowNodeCPU accepts ScenarioResults and presents to the user via stdout the NodeCPU info
func ShowNodeCPU(s ScenarioResults) {
	table := initTable([]string{"Result Type", "Driver", "Role", "Scenario", "Parallelism", "Host Network", "Service", "Message Size", "Burst", "Same node", "Idle CPU", "User CPU", "System CPU", "Steal CPU", "IOWait CPU", "Nice CPU", "SoftIRQ CPU", "IRQ CPU"})
	for _, r := range s.Results {
		// Skip RR/CRR iperf3 Results
		if strings.Contains(r.Profile, "RR") {
			if r.Driver != "netperf" {
				continue
			}
		}
		ccpu := r.ClientMetrics
		scpu := r.ServerMetrics
		table.Append([]string{
			"Node CPU Utilization", r.Driver, "Client", r.Profile, fmt.Sprintf("%d", r.Parallelism), fmt.Sprintf("%t", r.HostNetwork), fmt.Sprintf("%t", r.Service), fmt.Sprintf("%d", r.MessageSize), fmt.Sprintf("%d", r.Burst), fmt.Sprintf("%t", r.SameNode),
			fmt.Sprintf("%f", ccpu.Idle), fmt.Sprintf("%f", ccpu.User), fmt.Sprintf("%f", ccpu.System), fmt.Sprintf("%f", ccpu.Steal), fmt.Sprintf("%f", ccpu.Iowait), fmt.Sprintf("%f", ccpu.Nice), fmt.Sprintf("%f", ccpu.Softirq), fmt.Sprintf("%f", ccpu.Irq),
		})
		table.Append([]string{
			"Node CPU Utilization", r.Driver, "Server", r.Profile, fmt.Sprintf("%d", r.Parallelism), fmt.Sprintf("%t", r.HostNetwork), fmt.Sprintf("%t", r.Service), fmt.Sprintf("%d", r.MessageSize), fmt.Sprintf("%d", r.Burst), fmt.Sprintf("%t", r.SameNode),
			fmt.Sprintf("%f", scpu.Idle), fmt.Sprintf("%f", scpu.User), fmt.Sprintf("%f", scpu.System), fmt.Sprintf("%f", scpu.Steal), fmt.Sprintf("%f", scpu.Iowait), fmt.Sprintf("%f", scpu.Nice), fmt.Sprintf("%f", scpu.Softirq), fmt.Sprintf("%f", scpu.Irq),
		})
	}
	table.Render()
}

// ShowSpecificResults
func ShowSpecificResults(s ScenarioResults) {
	table := initTable([]string{"Type", "Driver", "Scenario", "Parallelism", "Host Network", "Service", "Message Size", "Burst", "Same node", "Duration", "Samples", "Avg value"})
	for _, r := range s.Results {
		if strings.Contains(r.Profile, "TCP_STREAM") {
			rt, _ := Average(r.RetransmitSummary)
			table.Append([]string{"TCP Retransmissions", r.Driver, r.Profile, strconv.Itoa(r.Parallelism), strconv.FormatBool(r.HostNetwork), strconv.FormatBool(r.Service), strconv.Itoa(r.MessageSize), strconv.Itoa(r.Burst), strconv.FormatBool(r.SameNode), strconv.Itoa(r.Duration), strconv.Itoa(r.Samples), fmt.Sprintf("%f", (rt))})
		}
		if strings.Contains(r.Profile, "UDP_STREAM") {
			loss, _ := Average(r.LossSummary)
			table.Append([]string{"UDP Loss Percent", r.Driver, r.Profile, strconv.Itoa(r.Parallelism), strconv.FormatBool(r.HostNetwork), strconv.FormatBool(r.Service), strconv.Itoa(r.MessageSize), strconv.Itoa(r.Burst), strconv.FormatBool(r.SameNode), strconv.Itoa(r.Duration), strconv.Itoa(r.Samples), fmt.Sprintf("%f", (loss))})
		}
	}
	table.Render()
}

// Abstracts out the common code for results
func renderResults(s ScenarioResults, testType string) {
	table := initTable([]string{"Result Type", "Driver", "Scenario", "Parallelism", "Host Network", "Service", "Message Size", "Burst", "Same node", "Duration", "Samples", "Avg value", "95% Confidence Interval"})
	for _, r := range s.Results {
		if strings.Contains(r.Profile, testType) {
			if len(r.Driver) > 0 {
				avg, _ := Average(r.ThroughputSummary)
				var lo, hi float64
				if r.Samples > 1 {
					_, lo, hi = ConfidenceInterval(r.ThroughputSummary, 0.95)
				}
				table.Append([]string{fmt.Sprintf("📊 %s Results", caser.String(strings.ToLower(testType))), r.Driver, r.Profile, strconv.Itoa(r.Parallelism), strconv.FormatBool(r.HostNetwork), strconv.FormatBool(r.Service), strconv.Itoa(r.MessageSize), strconv.Itoa(r.Burst), strconv.FormatBool(r.SameNode), strconv.Itoa(r.Duration), strconv.Itoa(r.Samples), fmt.Sprintf("%f (%s)", avg, r.Metric), fmt.Sprintf("%f-%f (%s)", lo, hi, r.Metric)})
			}
		}
	}
	table.Render()
}

// ShowStreamResult will display the throughput results
// Currently sharing Avg value
func ShowStreamResult(s ScenarioResults) {
	if checkResults(s, "STREAM") {
		logging.Debug("Rendering Stream results")
		renderResults(s, "STREAM")
	}
}

// ShowRRResult will display the RR transaction results
// Currently showing the Avg Value.
func ShowRRResult(s ScenarioResults) {
	if checkResults(s, "RR") {
		logging.Debug("Rendering RR Transaction results")
		renderResults(s, "RR")
	}
}

// ShowLatencyResult accepts NetPerfResults to display to the user via stdout
func ShowLatencyResult(s ScenarioResults) {
	if checkResults(s, "RR") {
		logging.Debug("Rendering RR P99 Latency results")
		table := initTable([]string{"Result Type", "Driver", "Scenario", "Parallelism", "Host Network", "Service", "Message Size", "Burst", "Same node", "Duration", "Samples", "Avg 99%tile value"})
		for _, r := range s.Results {
			if strings.Contains(r.Profile, "RR") {
				p99, _ := Average(r.LatencySummary)
				table.Append([]string{"RR Latency Results", r.Driver, r.Profile, strconv.Itoa(r.Parallelism), strconv.FormatBool(r.HostNetwork), strconv.FormatBool(r.Service), strconv.Itoa(r.MessageSize), strconv.Itoa(r.Burst), strconv.FormatBool(r.SameNode), strconv.Itoa(r.Duration), strconv.Itoa(r.Samples), fmt.Sprintf("%f (%s)", p99, "usec")})
			}
		}
		table.Render()
	}
}
