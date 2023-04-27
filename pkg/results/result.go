package result

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jtaleric/k8s-netperf/pkg/config"
	"github.com/jtaleric/k8s-netperf/pkg/logging"
	"github.com/jtaleric/k8s-netperf/pkg/metrics"
	"github.com/jtaleric/k8s-netperf/pkg/sample"
	stats "github.com/montanaflynn/stats"
	"github.com/olekukonko/tablewriter"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	math "github.com/aclements/go-moremath/stats"
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
	ThroughputSummary []float64
	LatencySummary    []float64
	ClientMetrics     metrics.NodeCPU
	ServerMetrics     metrics.NodeCPU
	ClientPodCPU      metrics.PodValues
	ServerPodCPU      metrics.PodValues
}

// ScenarioResults each scenario could have multiple results
type ScenarioResults struct {
	Results []Data
	Metadata
}

// Metadata for the run
type Metadata struct {
	Platform   string `json:"platform"`
	Kernel     string `json:"kernel"`
	Kubelet    string `json:"kubelet"`
	OCPVersion string `json:"ocpVersion"`
	IPsec      bool   `json:"ipsec"`
	MTU        int    `json:"mtu"`
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
func confidenceInterval(vals []float64, ci float64) (float64, float64, float64) {
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

// TCPThroughputDiff accepts the Scenario Results and calculates the %diff.
// returns
func TCPThroughputDiff(s ScenarioResults) (float64, error) {
	// We will focus on TCP STREAM
	hostPerf := 0.0
	podPerf := 0.0
	for t := range s.Results {
		if !s.Results[t].Service {
			if s.Results[t].HostNetwork {
				if s.Results[t].Profile == "TCP_STREAM" {
					hostPerf, _ = Average(s.Results[t].ThroughputSummary)
				}
			} else {
				if s.Results[t].Profile == "TCP_STREAM" {
					podPerf, _ = Average(s.Results[t].ThroughputSummary)
				}
			}
		}
	}
	return calDiff(hostPerf, podPerf), nil
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
	table := initTable([]string{"Result Type", "Driver", "Role", "Scenario", "Parallelism", "Host Network", "Service", "Message Size", "Same node", "Pod", "Utilization"})
	for _, r := range s.Results {
		for _, pod := range r.ClientPodCPU.Results {
			table.Append([]string{"Pod CPU Utilization", r.Driver, "Client", r.Profile, fmt.Sprintf("%d", r.Parallelism), fmt.Sprintf("%t", r.HostNetwork), fmt.Sprintf("%t", r.Service), fmt.Sprintf("%d", r.MessageSize), fmt.Sprintf("%t", r.SameNode), fmt.Sprintf("%.20s", pod.Name), fmt.Sprintf("%f", pod.Value)})
		}
		for _, pod := range r.ServerPodCPU.Results {
			table.Append([]string{"Pod CPU Utilization", r.Driver, "Server", r.Profile, fmt.Sprintf("%d", r.Parallelism), fmt.Sprintf("%t", r.HostNetwork), fmt.Sprintf("%t", r.Service), fmt.Sprintf("%d", r.MessageSize), fmt.Sprintf("%t", r.SameNode), fmt.Sprintf("%.20s", pod.Name), fmt.Sprintf("%f", pod.Value)})
		}
	}
	table.Render()
}

// ShowNodeCPU accepts ScenarioResults and presents to the user via stdout the NodeCPU info
func ShowNodeCPU(s ScenarioResults) {
	table := initTable([]string{"Result Type", "Driver", "Role", "Scenario", "Parallelism", "Host Network", "Service", "Message Size", "Same node", "Idle CPU", "User CPU", "System CPU", "Steal CPU", "IOWait CPU", "Nice CPU", "SoftIRQ CPU", "IRQ CPU"})
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
			"Node CPU Utilization", r.Driver, "Client", r.Profile, fmt.Sprintf("%d", r.Parallelism), fmt.Sprintf("%t", r.HostNetwork), fmt.Sprintf("%t", r.Service), fmt.Sprintf("%d", r.MessageSize), fmt.Sprintf("%t", r.SameNode),
			fmt.Sprintf("%f", ccpu.Idle), fmt.Sprintf("%f", ccpu.User), fmt.Sprintf("%f", ccpu.System), fmt.Sprintf("%f", ccpu.Steal), fmt.Sprintf("%f", ccpu.Iowait), fmt.Sprintf("%f", ccpu.Nice), fmt.Sprintf("%f", ccpu.Softirq), fmt.Sprintf("%f", ccpu.Irq),
		})
		table.Append([]string{
			"Node CPU Utilization", r.Driver, "Server", r.Profile, fmt.Sprintf("%d", r.Parallelism), fmt.Sprintf("%t", r.HostNetwork), fmt.Sprintf("%t", r.Service), fmt.Sprintf("%d", r.MessageSize), fmt.Sprintf("%t", r.SameNode),
			fmt.Sprintf("%f", scpu.Idle), fmt.Sprintf("%f", scpu.User), fmt.Sprintf("%f", scpu.System), fmt.Sprintf("%f", scpu.Steal), fmt.Sprintf("%f", scpu.Iowait), fmt.Sprintf("%f", scpu.Nice), fmt.Sprintf("%f", scpu.Softirq), fmt.Sprintf("%f", scpu.Irq),
		})
	}
	table.Render()
}

// Abstracts out the common code for results
func renderResults(s ScenarioResults, testType string) {
	table := initTable([]string{"Result Type", "Driver", "Scenario", "Parallelism", "Host Network", "Service", "Message Size", "Same node", "Duration", "Samples", "Avg value", "95% Confidence Interval"})
	for _, r := range s.Results {
		if strings.Contains(r.Profile, testType) {
			if len(r.Driver) > 0 {
				avg, _ := Average(r.ThroughputSummary)
				var lo, hi float64
				if r.Samples > 1 {
					_, lo, hi = confidenceInterval(r.ThroughputSummary, 0.95)
				}
				table.Append([]string{fmt.Sprintf("📊 %s Results", caser.String(strings.ToLower(testType))), r.Driver, r.Profile, strconv.Itoa(r.Parallelism), strconv.FormatBool(r.HostNetwork), strconv.FormatBool(r.Service), strconv.Itoa(r.MessageSize), strconv.FormatBool(r.SameNode), strconv.Itoa(r.Duration), strconv.Itoa(r.Samples), fmt.Sprintf("%f (%s)", avg, r.Metric), fmt.Sprintf("%f-%f (%s)", lo, hi, r.Metric)})
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
		table := initTable([]string{"Result Type", "Scenario", "Parallelism", "Host Network", "Service", "Message Size", "Same node", "Duration", "Samples", "Avg 99%tile value"})
		for _, r := range s.Results {
			if strings.Contains(r.Profile, "RR") {
				p99, _ := Average(r.LatencySummary)
				table.Append([]string{"RR Latency Results", r.Profile, strconv.Itoa(r.Parallelism), strconv.FormatBool(r.HostNetwork), strconv.FormatBool(r.Service), strconv.Itoa(r.MessageSize), strconv.FormatBool(r.SameNode), strconv.Itoa(r.Duration), strconv.Itoa(r.Samples), fmt.Sprintf("%f (%s)", p99, "usec")})
			}
		}
		table.Render()
	}
}
