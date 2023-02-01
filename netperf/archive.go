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

// WritePromCSVResult writes the prom data in CSV format
func WritePromCSVResult(r ScenarioResults) error {
	d := time.Now().Unix()
	podfp, err := os.Create(fmt.Sprintf("podcpu-result-%d.csv", d))
	defer podfp.Close()
	if err != nil {
		return fmt.Errorf("Failed to open pod cpu archive file")
	}
	cpufp, err := os.Create(fmt.Sprintf("cpu-result-%d.csv", d))
	defer cpufp.Close()
	if err != nil {
		return fmt.Errorf("Failed to open cpu archive file")
	}
	cpuarchive := csv.NewWriter(cpufp)
	defer cpuarchive.Flush()
	podarchive := csv.NewWriter(podfp)
	defer podarchive.Flush()
	cpudata := []string{
		"Role",
		"Profile",
		"Same node",
		"Host Network",
		"Service",
		"Duration",
		"Parallelism",
		"# of Samples",
		"Message Size",
		"Idle CPU",
		"User CPU",
		"System CPU",
		"IOWait CPU",
		"Steal CPU",
		"SoftIRQ CPU",
		"IRQ CPU"}
	poddata := []string{
		"Role",
		"Profile",
		"Same node",
		"Host Network",
		"Service",
		"Duration",
		"Parallelism",
		"# of Samples",
		"Message Size",
		"Pod Name",
		"Utilization"}
	if err := cpuarchive.Write(cpudata); err != nil {
		return fmt.Errorf("Failed to write cpu archive to file")
	}
	if err := podarchive.Write(poddata); err != nil {
		return fmt.Errorf("Failed to write pod archive to file")
	}
	for _, row := range r.Results {
		ccpu := row.ClientMetrics
		if err := cpuarchive.Write([]string{"Client",
			fmt.Sprint(row.Profile),
			fmt.Sprint(row.SameNode),
			fmt.Sprint(row.HostNetwork),
			fmt.Sprint(row.Service),
			strconv.Itoa(row.Duration),
			strconv.Itoa(row.Parallelism),
			strconv.Itoa(row.Samples),
			strconv.Itoa(row.MessageSize),
			fmt.Sprintf("%f", ccpu.Idle),
			fmt.Sprintf("%f", ccpu.User),
			fmt.Sprintf("%f", ccpu.System),
			fmt.Sprintf("%f", ccpu.Iowait),
			fmt.Sprintf("%f", ccpu.Steal),
			fmt.Sprintf("%f", ccpu.Softirq),
			fmt.Sprintf("%f", ccpu.Irq),
		}); err != nil {
			return fmt.Errorf("Failed to write archive to file")
		}
		scpu := row.ServerMetrics
		if err := cpuarchive.Write([]string{"Server",
			fmt.Sprint(row.Profile),
			fmt.Sprint(row.SameNode),
			fmt.Sprint(row.HostNetwork),
			fmt.Sprint(row.Service),
			strconv.Itoa(row.Duration),
			strconv.Itoa(row.Parallelism),
			strconv.Itoa(row.Samples),
			strconv.Itoa(row.MessageSize),
			fmt.Sprintf("%f", scpu.Idle),
			fmt.Sprintf("%f", scpu.User),
			fmt.Sprintf("%f", scpu.System),
			fmt.Sprintf("%f", scpu.Iowait),
			fmt.Sprintf("%f", scpu.Steal),
			fmt.Sprintf("%f", scpu.Softirq),
			fmt.Sprintf("%f", scpu.Irq),
		}); err != nil {
			return fmt.Errorf("Failed to write archive to file")
		}
		for _, pod := range row.ClientPodCPU.Results {
			if err := podarchive.Write([]string{"Server",
				fmt.Sprint(row.Profile),
				fmt.Sprint(row.SameNode),
				fmt.Sprint(row.HostNetwork),
				fmt.Sprint(row.Service),
				strconv.Itoa(row.Duration),
				strconv.Itoa(row.Parallelism),
				strconv.Itoa(row.Samples),
				strconv.Itoa(row.MessageSize),
				fmt.Sprintf("%s", pod.Name),
				fmt.Sprintf("%f", pod.Value),
			}); err != nil {
				return fmt.Errorf("Failed to write archive to file")
			}
		}
		for _, pod := range row.ServerPodCPU.Results {
			if err := podarchive.Write([]string{"Server",
				fmt.Sprint(row.Profile),
				fmt.Sprint(row.SameNode),
				fmt.Sprint(row.HostNetwork),
				fmt.Sprint(row.Service),
				strconv.Itoa(row.Duration),
				strconv.Itoa(row.Parallelism),
				strconv.Itoa(row.Samples),
				strconv.Itoa(row.MessageSize),
				fmt.Sprintf("%s", pod.Name),
				fmt.Sprintf("%f", pod.Value),
			}); err != nil {
				return fmt.Errorf("Failed to write archive to file")
			}
		}

	}
	return nil
}

// WriteCSVResult will write the throughput result to the local filesystem
func WriteCSVResult(r ScenarioResults) error {
	d := time.Now().Unix()
	fp, err := os.Create(fmt.Sprintf("result-%d.csv", d))
	defer fp.Close()
	if err != nil {
		return fmt.Errorf("Failed to open archive file")
	}
	archive := csv.NewWriter(fp)
	defer archive.Flush()

	data := []string{
		"Profile",
		"Same node",
		"Host Network",
		"Service",
		"Duration",
		"Parallelism",
		"# of Samples",
		"Message Size",
		"Avg Throughput",
		"Throughput Metric",
		"99%tile Observed Latency",
		"Latency Metric"}

	if err := archive.Write(data); err != nil {
		return fmt.Errorf("Failed to write result archive to file")
	}
	for _, row := range r.Results {
		avg, _ := average(row.ThroughputSummary)
		lavg, _ := average(row.LatencySummary)
		data := []string{row.Profile,
			fmt.Sprint(row.SameNode),
			fmt.Sprint(row.HostNetwork),
			fmt.Sprint(row.Service),
			strconv.Itoa(row.Duration),
			strconv.Itoa(row.Parallelism),
			strconv.Itoa(row.Samples),
			strconv.Itoa(row.MessageSize),
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
