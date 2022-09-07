package main

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	stats "github.com/montanaflynn/stats"
)

var NetReg = regexp.MustCompile(`\s+\d+\s+\d+\s+(\d+|\S+)\s+(\S+|\d+)\s+(\S+)+\s+(\S+)?`)

func average(vals []float64) (float64, error) {
	return stats.Median(vals)
}

func percentile(vals []float64, ptile float64) (float64, error) {
	return stats.Percentile(vals, ptile)
}

func checkResults(s ScenarioResults, check string) bool {
	for t := range s.results {
		if strings.Contains(s.results[t].Profile, check) {
			return true
		}
	}
	return false
}

// ShowStreamResults accepts NetPerfResults to display to the user via stdout
func ShowStreamResult(s ScenarioResults) {
	if checkResults(s, "STREAM") {
		fmt.Printf("%s Stream Results %s\r\n", strings.Repeat("-", 59), strings.Repeat("-", 59))
		fmt.Printf("%-18s | %-15s | %-15s | %-15s | %-15s | %-15s | %-15s\r\n", "Scenario", "Service", "Message Size", "Same node", "Duration", "Samples", "Avg value")
		fmt.Printf("%s\r\n", strings.Repeat("-", 136))
		for _, r := range s.results {
			if strings.Contains(r.Profile, "STREAM") {
				avg, _ := average(r.Summary)
				fmt.Printf("📊 %-15s | %-15t | %-15d | %-15t | %-15d | %-15d | %-15f (%s) \r\n", r.Profile, r.Service, r.MessageSize, r.SameNode, r.Duration, r.Samples, avg, r.Metric)
			}
		}
		fmt.Printf("%s\r\n", strings.Repeat("-", 136))
	}
}

func ShowRRResult(s ScenarioResults) {
	if checkResults(s, "RR") {
		fmt.Printf("%s RR Results %s\r\n", strings.Repeat("-", 62), strings.Repeat("-", 62))
		fmt.Printf("%-18s | %-15s | %-15s | %-15s | %-15s | %-15s | %-15s\r\n", "Scenario", "Service", "Message Size", "Same node", "Duration", "Samples", "99%tile value")
		fmt.Printf("%s\r\n", strings.Repeat("-", 136))
		for _, r := range s.results {
			if strings.Contains(r.Profile, "RR") {
				pct, _ := percentile(r.Summary, 99)
				fmt.Printf("📊 %-15s | %-15t | %-15d | %-15t | %-15d | %-15d | %-15f (%s) \r\n", r.Profile, r.Service, r.MessageSize, r.SameNode, r.Duration, r.Samples, pct, r.Metric)
			}
		}
		fmt.Printf("%s\r\n", strings.Repeat("-", 136))
	}
}

// ParseResults accepts the stdout from the execution of the benchmark. It also needs
// The NetPerfConfig to determine aspects of the workload the user provided.
// It will return a NetPerfResults struct or error
func ParseResults(stdout *bytes.Buffer, nc NetPerfConfig) (float64, error) {
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
