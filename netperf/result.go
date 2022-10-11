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
	Metric      string
	SameNode    bool
	HostNetwork bool
	Sample      float64
	Summary     []float64
}

// ScenarioResults each scenario could have multiple results
type ScenarioResults struct {
	Results []Data
}

// NetReg Regex for Netperf
var NetReg = regexp.MustCompile(`\s+\d+\s+\d+\s+(\d+|\S+)\s+(\S+|\d+)\s+(\S+)+\s+(\S+)?`)

// NetUDPReg, with UDP we need to capture what the server saw versus what the client sent
var NetUDPReg = regexp.MustCompile(`(?m)^[0-9]+\s+\S+\s+\d+\s+(\S+)`)

func average(vals []float64) (float64, error) {
	return stats.Median(vals)
}

func percentile(vals []float64, ptile float64) (float64, error) {
	return stats.Percentile(vals, ptile)
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

// checkHostResults will check to see if there are hostNet results
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
					hostPerf, _ = average(s.Results[t].Summary)
				}
			} else {
				if s.Results[t].Profile == "TCP_STREAM" {
					podPerf, _ = average(s.Results[t].Summary)
				}
			}
		}
	}
	return calDiff(hostPerf, podPerf), nil
}

// calDiff will determine the %diff between two values.
// returns a float64 which is the %diff
func calDiff(a float64, b float64) float64 {
	return (a - b) / ((a + b) / 2) * 100
}

// ShowStreamResult accepts NetPerfResults to display to the user via stdout
func ShowStreamResult(s ScenarioResults) {
	if checkResults(s, "STREAM") {
		fmt.Printf("%s Stream Results %s\r\n", strings.Repeat("-", 69), strings.Repeat("-", 69))
		fmt.Printf("%-18s | %-15s |%-15s | %-15s | %-15s | %-15s | %-15s | %-15s\r\n", "Scenario", "Host Network", "Service", "Message Size", "Same node", "Duration", "Samples", "Avg value")
		fmt.Printf("%s\r\n", strings.Repeat("-", 155))
		for _, r := range s.Results {
			if strings.Contains(r.Profile, "STREAM") {
				avg, _ := average(r.Summary)
				fmt.Printf("📊 %-15s | %-15t |%-15t | %-15d | %-15t | %-15d | %-15d | %-15f (%s) \r\n", r.Profile, r.HostNetwork, r.Service, r.MessageSize, r.SameNode, r.Duration, r.Samples, avg, r.Metric)
			}
		}
		fmt.Printf("%s\r\n", strings.Repeat("-", 155))
	}
}

// ShowRRResult will display the RR performance results
// Currently showing the Avg Value.
// TODO: Capture latency values
func ShowRRResult(s ScenarioResults) {
	if checkResults(s, "RR") {
		fmt.Printf("%s RR Results %s\r\n", strings.Repeat("-", 72), strings.Repeat("-", 72))
		fmt.Printf("%-18s | %-15s |%-15s | %-15s | %-15s | %-15s | %-15s | %-15s\r\n", "Scenario", "Host Network", "Service", "Message Size", "Same node", "Duration", "Samples", "Avg value")
		fmt.Printf("%s\r\n", strings.Repeat("-", 155))
		for _, r := range s.Results {
			if strings.Contains(r.Profile, "RR") {
				avg, _ := average(r.Summary)
				fmt.Printf("📊 %-15s | %-15t |%-15t | %-15d | %-15t | %-15d | %-15d | %-15f (%s) \r\n", r.Profile, r.HostNetwork, r.Service, r.MessageSize, r.SameNode, r.Duration, r.Samples, avg, r.Metric)
			}
		}
		fmt.Printf("%s\r\n", strings.Repeat("-", 155))
	}
}

// ParseResults accepts the stdout from the execution of the benchmark. It also needs
// The NetPerfConfig to determine aspects of the workload the user provided.
// It will return a NetPerfResults struct or error
func ParseResults(stdout *bytes.Buffer, nc Config) (float64, error) {
	d := NetReg.FindStringSubmatch(stdout.String())
	val := ""
	if strings.Contains(nc.Profile, "UDP_STREAM") {
		d = NetUDPReg.FindStringSubmatch(stdout.String())
		if len(d) == 0 {
			return 0, fmt.Errorf("❌ Unable to process results")
		}
		val = d[1]
	} else {
		if len(d) < 5 {
			return 0, fmt.Errorf("❌ Unable to process results")
		}
		if len(d[len(d)-1]) > 0 {
			val = d[len(d)-1]
		} else {
			val = d[len(d)-2]
		}
	}
	sample, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0, fmt.Errorf("❌ Unable to parse netperf result")
	}
	return sample, nil
}
