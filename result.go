package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// ShowResults accepts NetPerfResults to display to the user via stdout
func ShowResult(s ScenarioResults) {
	fmt.Printf("%s\r\n", strings.Repeat("-", (18+25+55)))
	fmt.Printf("%-18s | %-15s | %-15s | %-15s | %-15s\r\n", "Scenario", "Message Size", "Setup", "Duration", "Value")
	fmt.Printf("%s\r\n", strings.Repeat("-", (18+25+55)))
	for _, r := range s.results {
		setup := "Across node"
		if r.SameNode {
			setup = "Same Node"
		}
		fmt.Printf("📊 %-15s | %-15d | %-15s | %-15d | %-15f (%s) \r\n", r.Profile, r.MessageSize, setup, r.Duration, r.Values, r.Metric)
	}
	fmt.Printf("%s\r\n", strings.Repeat("-", (18+25+55)))
}

// ParseResults accepts the stdout from the execution of the benchmark. It also needs
// The NetPerfConfig to determine aspects of the workload the user provided.
// It will return a NetPerfResults struct or error
func ParseResults(stdout *bytes.Buffer, nc NetPerfConfig) (NetPerfResults, error) {
	metric := string("OP/s")
	if strings.Contains(nc.Profile, "STREAM") {
		metric = "Mb/s"
	}
	r := NetPerfResults{
		Metric: metric,
	}
	d := NetReg.FindStringSubmatch(stdout.String())
	if len(d) < 5 {
		return r, fmt.Errorf("❌ Unable to process results")
	}
	val := ""
	if len(d[len(d)-1]) > 0 {
		val = d[len(d)-1]
	} else {
		val = d[len(d)-2]
	}
	v, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return r, fmt.Errorf("❌ Unable to parse netperf result")
	}
	r.Values = v
	r.Duration = nc.Duration
	r.Profile = nc.Profile
	r.MessageSize = nc.MessageSize
	return r, nil
}
