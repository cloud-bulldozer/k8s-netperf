package netperf

import (
	"context"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jtaleric/k8s-netperf/pkg/logging"
	"github.com/jtaleric/k8s-netperf/pkg/metrics"
	"github.com/opensearch-project/opensearch-go"
	"github.com/opensearch-project/opensearch-go/opensearchapi"
)

const index = "k8s-netperf"

// Doc struct of the JSON document to be indexed
type Doc struct {
	UUID          string           `json:"uuid"`
	Timestamp     time.Time        `json:"timestamp"`
	HostNetwork   bool             `json:"hostNetwork"`
	Parallelism   int              `json:"parallelism"`
	Profile       string           `json:"profile"`
	Duration      int              `json:"duration"`
	Samples       int              `json:"samples"`
	Messagesize   int              `json:"messageSize"`
	Result        float64          `json:"result"`
	Metric        string           `json:"metric"`
	Metadata      Metadata         `json:"metadata"`
	ServerNodeCPU metrics.NodeCPU  `json:"serverCPU"`
	ServerPodCPU  []metrics.PodCPU `json:"serverPods"`
	ClientNodeCPU metrics.NodeCPU  `json:"clientCPU"`
	ClientPodCPU  []metrics.PodCPU `json:"clientPods"`
}

// Connect returns a client connected to the desired cluster.
func Connect(url string, skip bool) (*opensearch.Client, error) {
	config := opensearch.Config{
		Addresses: []string{url},
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: skip},
		},
	}
	client, err := opensearch.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("Unable to connect OpenSearch")
	}
	logging.Infof("Connected to : %s\n", config.Addresses)
	return client, nil
}

// BuildDocs returns the documents that need to be indexed or an error.
func BuildDocs(sr ScenarioResults) ([]Doc, error) {
	u := uuid.New()
	uuid := fmt.Sprintf("%s", u.String())
	time := time.Now().UTC()

	var docs []Doc
	if len(sr.Results) < 1 {
		return nil, fmt.Errorf("No result documents")
	}
	for _, r := range sr.Results {
		var d Doc
		d.UUID = uuid
		d.Timestamp = time
		d.HostNetwork = r.HostNetwork
		d.Parallelism = r.Parallelism
		d.Profile = r.Profile
		d.Duration = r.Duration
		d.Samples = r.Samples
		d.Messagesize = r.MessageSize
		d.Metric = r.Metric
		if strings.Contains(d.Profile, "STREAM") {
			d.Result, _ = average(r.ThroughputSummary)
		} else {
			d.Result, _ = percentile(r.LatencySummary, 95)
		}
		d.ServerNodeCPU = r.ServerMetrics
		d.ClientNodeCPU = r.ClientMetrics
		d.ServerPodCPU = r.ServerPodCPU.Results
		d.ClientPodCPU = r.ClientPodCPU.Results
		d.Metadata = sr.Metadata
		docs = append(docs, d)
	}
	return docs, nil
}

// IndexDocs indexes results from k8s-netperf returns failures if any happen.
func IndexDocs(client *opensearch.Client, docs []Doc) error {
	logging.Infof("Attempting to index %d documents", len(docs))
	for _, doc := range docs {
		jdoc, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		body := strings.NewReader(string(jdoc))
		logging.Debug(body)
		r := opensearchapi.IndexRequest{
			Index: index,
			Body:  body,
		}
		resp, err := r.Do(context.Background(), client)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
	}
	return nil
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
