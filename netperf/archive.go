package netperf

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
)

type elasticParams struct {
	url      string
	user     string
	password string
	index    string
}

// Connect accepts ElasticParams which describe how to connect to ES.
// Returns a client connected to the desired ES Cluster.
func Connect(es elasticParams) (*elasticsearch.Client, error) {
	fmt.Printf("Connecting to ES - %s\r\n", es.url)
	esc := elasticsearch.Config{
		Username:  es.user,
		Password:  es.password,
		Addresses: []string{es.url},
	}
	ec, err := elasticsearch.NewClient(esc)
	if err != nil {
		return nil, fmt.Errorf("Error connecting to ES")
	}
	return ec, nil
}

// WriteCSVResult will write the throughput result to the local filesystem
func WriteCSVResult(r ScenarioResults) error {
	fp, err := os.Create(fmt.Sprintf("result-%d.csv", time.Now().Unix()))
	defer fp.Close()
	if err != nil {
		return fmt.Errorf("Failed to open archive file")
	}
	archive := csv.NewWriter(fp)
	defer archive.Flush()
	data := []string{"Profile",
		"Same node",
		"Host Network",
		"Service", "Duration",
		"# of Samples",
		"Avg Throughput",
		"Throughput Metric",
		"99%tile Observed Latency",
		"Latency Metric"}
	if err := archive.Write(data); err != nil {
		return fmt.Errorf("Failed to write archive to file")
	}
	for _, row := range r.Results {
		avg, _ := average(row.ThroughputSummary)
		lavg, _ := average(row.LatencySummary)
		data := []string{row.Profile,
			fmt.Sprint(row.SameNode),
			fmt.Sprint(row.HostNetwork),
			fmt.Sprint(row.Service),
			strconv.Itoa(row.Duration),
			strconv.Itoa(row.Samples),
			fmt.Sprintf("%f", avg),
			row.Metric,
			fmt.Sprint(lavg),
			"usec"}
		if err := archive.Write(data); err != nil {
			return fmt.Errorf("Failed to write archive to file")
		}
	}
	return nil
}
