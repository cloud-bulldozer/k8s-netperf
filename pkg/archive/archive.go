package archive

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

	"github.com/jtaleric/k8s-netperf/pkg/logging"
	"github.com/jtaleric/k8s-netperf/pkg/metrics"
	result "github.com/jtaleric/k8s-netperf/pkg/results"
	"github.com/opensearch-project/opensearch-go"
	"github.com/opensearch-project/opensearch-go/opensearchapi"
)

const index = "k8s-netperf"
const ltcyMetric = "usec"

// Doc struct of the JSON document to be indexed
type Doc struct {
	UUID          string           `json:"uuid"`
	Timestamp     time.Time        `json:"timestamp"`
	HostNetwork   bool             `json:"hostNetwork"`
	Driver        string           `json:"driver"`
	Parallelism   int              `json:"parallelism"`
	Profile       string           `json:"profile"`
	Duration      int              `json:"duration"`
	Samples       int              `json:"samples"`
	Messagesize   int              `json:"messageSize"`
	Throughput    float64          `json:"throughput"`
	Latency       float64          `json:"latency"`
	TputMetric    string           `json:"tputMetric"`
	LtcyMetric    string           `json:"ltcyMetric"`
	Metadata      result.Metadata  `json:"metadata"`
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
func BuildDocs(sr result.ScenarioResults, uuid string) ([]Doc, error) {
	time := time.Now().UTC()

	var docs []Doc
	if len(sr.Results) < 1 {
		return nil, fmt.Errorf("No result documents")
	}
	for _, r := range sr.Results {
		if len(r.Driver) < 1 {
			continue
		}
		var d Doc
		d.UUID = uuid
		d.Timestamp = time
		d.Driver = r.Driver
		d.HostNetwork = r.HostNetwork
		d.Parallelism = r.Parallelism
		d.Profile = r.Profile
		d.Duration = r.Duration
		d.Samples = r.Samples
		d.Messagesize = r.MessageSize
		d.Throughput, _ = result.Average(r.ThroughputSummary)
		d.Latency, _ = result.Average(r.LatencySummary)
		d.TputMetric = r.Metric
		d.LtcyMetric = ltcyMetric
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

// Common csv header fields.
func commonCsvHeaderFields() []string {
	return []string{
		"Profile",
		"Same node",
		"Host Network",
		"Service",
		"Duration",
		"Parallelism",
		"# of Samples",
		"Message Size",
	}
}

// Common csv data fields.
func commonCsvDataFeilds(row result.Data) []string{
	return []string{
		fmt.Sprint(row.Profile),
		fmt.Sprint(row.SameNode),
		fmt.Sprint(row.HostNetwork),
		fmt.Sprint(row.Service),
		strconv.Itoa(row.Duration),
		strconv.Itoa(row.Parallelism),
		strconv.Itoa(row.Samples),
		strconv.Itoa(row.MessageSize),
	}
}

// Writes all the mertic feilds to the archive.
func writeArchive(cpuarchive, podarchive *csv.Writer, role string, row result.Data, podResults []metrics.PodCPU) error {
	roleFieldData := []string{role}
	for _, pod := range podResults {
		if err := podarchive.Write(append(append(roleFieldData, 
			commonCsvDataFeilds(row)...), 
			fmt.Sprintf("%s", pod.Name),
			fmt.Sprintf("%f", pod.Value),
		)); err != nil {
			return fmt.Errorf("Failed to write archive to file")
		}
	}

	cpu := row.ClientMetrics
	if role == "Server" {
		cpu = row.ServerMetrics
	}
	if err := cpuarchive.Write(append(append(roleFieldData, 
		commonCsvDataFeilds(row)...),
		fmt.Sprintf("%f", cpu.Idle),
		fmt.Sprintf("%f", cpu.User),
		fmt.Sprintf("%f", cpu.System),
		fmt.Sprintf("%f", cpu.Iowait),
		fmt.Sprintf("%f", cpu.Steal),
		fmt.Sprintf("%f", cpu.Softirq),
		fmt.Sprintf("%f", cpu.Irq),
	)); err != nil {
		return fmt.Errorf("Failed to write archive to file")
	}
	return nil
}

// WritePromCSVResult writes the prom data in CSV format
func WritePromCSVResult(r result.ScenarioResults) error {
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
	roleField := []string{"Role"}
	cpudata := append(append(roleField, 
		commonCsvHeaderFields()...), 
		"Idle CPU",
		"User CPU",
		"System CPU",
		"IOWait CPU",
		"Steal CPU",
		"SoftIRQ CPU",
		"IRQ CPU",
	)
	poddata := append(append(roleField,
		commonCsvHeaderFields()...),
		"Pod Name",
		"Utilization",
	)
	if err := cpuarchive.Write(cpudata); err != nil {
		return fmt.Errorf("Failed to write cpu archive to file")
	}
	if err := podarchive.Write(poddata); err != nil {
		return fmt.Errorf("Failed to write pod archive to file")
	}
	for _, row := range r.Results {
		if err := writeArchive(cpuarchive, podarchive, "Client", row, row.ClientPodCPU.Results); err != nil {
			return err
		}
		if err := writeArchive(cpuarchive, podarchive, "Server", row, row.ServerPodCPU.Results); err != nil {
			return err
		}
	}
	return nil
}

// WriteCSVResult will write the throughput result to the local filesystem
func WriteCSVResult(r result.ScenarioResults) error {
	d := time.Now().Unix()
	fp, err := os.Create(fmt.Sprintf("result-%d.csv", d))
	defer fp.Close()
	if err != nil {
		return fmt.Errorf("Failed to open archive file")
	}
	archive := csv.NewWriter(fp)
	defer archive.Flush()

	data := append(commonCsvHeaderFields(),
		"Avg Throughput",
		"Throughput Metric",
		"99%tile Observed Latency",
		"Latency Metric",
	)

	if err := archive.Write(data); err != nil {
		return fmt.Errorf("Failed to write result archive to file")
	}
	for _, row := range r.Results {
		avg, _ := result.Average(row.ThroughputSummary)
		lavg, _ := result.Average(row.LatencySummary)
		data := append(commonCsvDataFeilds(row),
			fmt.Sprintf("%f", avg),
			row.Metric,
			fmt.Sprint(lavg),
			"usec",
		)
		if err := archive.Write(data); err != nil {
			return fmt.Errorf("Failed to write archive to file")
		}
	}
	return nil
}
