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

// ShowResults accepts NetPerfResults to display to the user via stdout
func ShowResult(s ScenarioResults) {
	fmt.Printf("%s\r\n", strings.Repeat("-", (18+25+75)))
	fmt.Printf("%-18s | %-15s | %-15s | %-15s | %-15s | %-15s\r\n", "Scenario", "Message Size", "Same node", "Duration", "Samples", "Avg value")
	fmt.Printf("%s\r\n", strings.Repeat("-", (18+25+75)))
	for _, r := range s.results {
		samples := len(r.Summary)
		avg, _ := average(r.Summary)
		fmt.Printf("📊 %-15s | %-15d | %-15t | %-15d | %-15d | %-15f (%s) \r\n", r.Profile, r.MessageSize, r.SameNode, r.Duration, samples, avg, r.Metric)
	}
	fmt.Printf("%s\r\n", strings.Repeat("-", (18+25+75)))
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
