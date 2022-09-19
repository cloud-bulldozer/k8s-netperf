package netperf

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	stats "github.com/montanaflynn/stats"
)

// Data describes the result data
type Data struct {
	Config
	Metric   string
	SameNode bool
	Sample   float64
	Summary  []float64
}

// ScenarioResults each scenario could have multiple results
type ScenarioResults struct {
	Results []Data
}

// NetReg Regex for netperf
var NetReg = regexp.MustCompile(`\s+\d+\s+\d+\s+(\d+|\S+)\s+(\S+|\d+)\s+(\S+)+\s+(\S+)?`)

func average(vals []float64) (float64, error) {
	return stats.Median(vals)
}

func percentile(vals []float64, ptile float64) (float64, error) {
	return stats.Percentile(vals, ptile)
}

func checkResults(s ScenarioResults, check string) bool {
	for t := range s.Results {
		if strings.Contains(s.Results[t].Profile, check) {
			return true
		}
	}
	return false
}

// ShowStreamResult accepts NetPerfResults to display to the user via stdout
func ShowStreamResult(s ScenarioResults) {
	if checkResults(s, "STREAM") {
		fmt.Printf("%s Stream Results %s\r\n", strings.Repeat("-", 59), strings.Repeat("-", 59))
		fmt.Printf("%-18s | %-15s | %-15s | %-15s | %-15s | %-15s | %-15s\r\n", "Scenario", "Service", "Message Size", "Same node", "Duration", "Samples", "Avg value")
		fmt.Printf("%s\r\n", strings.Repeat("-", 136))
		for _, r := range s.Results {
			if strings.Contains(r.Profile, "STREAM") {
				avg, _ := average(r.Summary)
				fmt.Printf("📊 %-15s | %-15t | %-15d | %-15t | %-15d | %-15d | %-15f (%s) \r\n", r.Profile, r.Service, r.MessageSize, r.SameNode, r.Duration, r.Samples, avg, r.Metric)
			}
		}
		fmt.Printf("%s\r\n", strings.Repeat("-", 136))
	}
}

// ShowRRResult will display the RR performance results
// Currently showing the Avg Value.
// TODO: Capture latency values
func ShowRRResult(s ScenarioResults) {
	if checkResults(s, "RR") {
		fmt.Printf("%s RR Results %s\r\n", strings.Repeat("-", 62), strings.Repeat("-", 62))
		fmt.Printf("%-18s | %-15s | %-15s | %-15s | %-15s | %-15s | %-15s\r\n", "Scenario", "Service", "Message Size", "Same node", "Duration", "Samples", "Avg value")
		fmt.Printf("%s\r\n", strings.Repeat("-", 136))
		for _, r := range s.Results {
			if strings.Contains(r.Profile, "RR") {
				avg, _ := average(r.Summary)
				fmt.Printf("📊 %-15s | %-15t | %-15d | %-15t | %-15d | %-15d | %-15f (%s) \r\n", r.Profile, r.Service, r.MessageSize, r.SameNode, r.Duration, r.Samples, avg, r.Metric)
			}
		}
		fmt.Printf("%s\r\n", strings.Repeat("-", 136))
	}
}

// ParseResults accepts the stdout from the execution of the benchmark. It also needs
// The NetPerfConfig to determine aspects of the workload the user provided.
// It will return a NetPerfResults struct or error
func ParseResults(stdout *bytes.Buffer) (float64, error) {
	d := NetReg.FindStringSubmatch(stdout.String())
	if len(d) < 5 {
		return 0, fmt.Errorf("❌ Unable to process results")
	}
	val := ""
	if len(d[len(d)-1]) > 0 {
		val = d[len(d)-1]
	} else {
		val = d[len(d)-2]
	}
	sample, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0, fmt.Errorf("❌ Unable to parse netperf result")
	}
	return sample, nil
}
