package drivers

import (
	"bytes"
	"testing"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
)

// superNetperfOutput mimics the lines super-netperf prints for one sample.
func superNetperfOutput(mssLine string) *bytes.Buffer {
	out := "MIGRATED TCP STREAM TEST from 0.0.0.0 (0.0.0.0) port 0 AF_INET to 10.0.0.2 () port 42424 AF_INET\n" +
		"RT_LATENCY=-1.000\nP99_LATENCY=0\nTHROUGHPUT=9400.5\nLOCAL_TRANSPORT_RETRANS=3\n" +
		"REMOTE_RECV_CALLS=100\nLOCAL_SEND_CALLS=100\n" + mssLine + "THROUGHPUT_UNITS=10^6bits/s\n"
	return bytes.NewBufferString(out)
}

func TestNetperfParseResultsTransportMSS(t *testing.T) {
	testCases := []struct {
		name    string
		mssLine string
		want    int
	}{
		{name: "tcp", mssLine: "TRANSPORT_MSS=1448\n", want: 1448},
		{name: "udp reports -1", mssLine: "TRANSPORT_MSS=-1\n", want: 0},
		{name: "older super-netperf without the key", mssLine: "", want: 0},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			driver, err := NewDriver("netperf", config.Config{Profile: "TCP_STREAM"})
			if err != nil {
				t.Fatal(err)
			}
			got, err := driver.ParseResults(superNetperfOutput(tc.mssLine), config.Config{})
			if err != nil {
				t.Fatalf("ParseResults() error = %v", err)
			}
			if got.TransportMSS != tc.want {
				t.Errorf("TransportMSS = %d, want %d", got.TransportMSS, tc.want)
			}
			if got.Throughput != 9400.5 || got.Retransmits != 3 || got.Metric != "10^6bits/s" {
				t.Errorf("other fields changed: %+v", got)
			}
		})
	}
}
