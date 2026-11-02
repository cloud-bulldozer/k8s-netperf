package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/sample"
)

func TestRerunSample(t *testing.T) {
	runErr := errors.New("connection refused")
	parseErr := errors.New("no results")
	testCases := []struct {
		name      string
		runErrs   []error // per attempt; a nil entry runs successfully
		parseErrs []error // per successful run
		wantRuns  int
		wantErr   error
	}{
		{name: "run keeps failing", runErrs: []error{runErr, runErr, runErr, runErr}, wantRuns: retry, wantErr: runErr},
		{name: "parse keeps failing", runErrs: []error{nil, nil, nil, nil}, parseErrs: []error{parseErr, parseErr, parseErr, parseErr}, wantRuns: retry, wantErr: parseErr},
		{name: "recovers after a failed run", runErrs: []error{runErr, nil}, parseErrs: []error{nil}, wantRuns: 2},
		{name: "recovers after a failed parse", runErrs: []error{nil, nil}, parseErrs: []error{parseErr, nil}, wantRuns: 2},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runs, parses := 0, 0
			run := func() (bytes.Buffer, error) {
				runs++
				if runs > len(tc.runErrs) {
					t.Fatalf("run called %d times, want at most %d: the retries do not stop", runs, retry)
				}
				return *bytes.NewBufferString("output"), tc.runErrs[runs-1]
			}
			parse := func(*bytes.Buffer) (sample.Sample, error) {
				parses++
				if err := tc.parseErrs[parses-1]; err != nil {
					return sample.Sample{}, err
				}
				return sample.Sample{Throughput: 42}, nil
			}
			got, err := rerunSample(retry, run, parse)
			if runs != tc.wantRuns {
				t.Errorf("run called %d times, want %d", runs, tc.wantRuns)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("rerunSample() error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && got.Throughput != 42 {
				t.Errorf("rerunSample() = %+v, want the parsed sample", got)
			}
		})
	}
}
