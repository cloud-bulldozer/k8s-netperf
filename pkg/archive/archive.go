package archive

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloud-bulldozer/go-commons/indexers"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/metrics"
	result "github.com/cloud-bulldozer/k8s-netperf/pkg/results"
)

const ltcyMetric = "usec"

// Doc struct of the JSON document to be indexed
type Doc struct {
	UUID              string           `json:"uuid"`
	Timestamp         time.Time        `json:"timestamp"`
	HostNetwork       bool             `json:"hostNetwork"`
	Driver            string           `json:"driver"`
	Parallelism       int              `json:"parallelism"`
	Profile           string           `json:"profile"`
	Duration          int              `json:"duration"`
	Service           bool             `json:"service"`
	Local             bool             `json:"local"`
	Virt              bool             `json:"virt"`
	AcrossAZ          bool             `json:"acrossAZ"`
	Samples           int              `json:"samples"`
	Messagesize       int              `json:"messageSize"`
	Burst             int              `json:"burst"`
	Throughput        float64          `json:"throughput"`
	Latency           float64          `json:"latency"`
	TputMetric        string           `json:"tputMetric"`
	LtcyMetric        string           `json:"ltcyMetric"`
	TCPRetransmit     float64          `json:"tcpRetransmits"`
	UDPLossPercent    float64          `json:"udpLossPercent"`
	ToolVersion       string           `json:"toolVersion"`
	ToolGitCommit     string           `json:"toolGitCommit"`
	Metadata          result.Metadata  `json:"metadata"`
	ServerNodeCPU     metrics.NodeCPU  `json:"serverCPU"`
	ServerPodCPU      []metrics.PodCPU `json:"serverPods"`
	ServerPodMem      []metrics.PodMem `json:"serverPodsMem"`
	ClientNodeCPU     metrics.NodeCPU  `json:"clientCPU"`
	ClientPodCPU      []metrics.PodCPU `json:"clientPods"`
	ClientPodMem      []metrics.PodMem `json:"clientPodsMem"`
	Confidence        []float64        `json:"confidence"`
	ServerNodeInfo    metrics.NodeInfo `json:"serverNodeInfo"`
	ClientNodeInfo    metrics.NodeInfo `json:"clientNodeInfo"`
	ServerVSwitchCpu  float64          `json:"serverVswtichCpu"`
	ServerVSwitchMem  float64          `json:"serverVswitchMem"`
	ClientVSwitchCpu  float64          `json:"clientVswtichCpu"`
	ClientVSwiitchMem float64          `json:"clientVswitchMem"`
}

// Connect returns a client connected to the desired cluster.
func Connect(url, index string, skip bool) (*indexers.Indexer, error) {
	var err error
	var indexer *indexers.Indexer
	indexerConfig := indexers.IndexerConfig{
		Type:               "opensearch",
		Servers:            []string{url},
		Index:              index,
		InsecureSkipVerify: true,
	}
	logging.Infof("📁 Creating indexer: %s", indexerConfig.Type)
	indexer, err = indexers.NewIndexer(indexerConfig)
	if err != nil {
		logging.Errorf("%v indexer: %v", indexerConfig.Type, err.Error())
		return nil, fmt.Errorf("Failure while connnecting to Opensearch")
	}
	logging.Infof("Connected to : %s ", url)
	return indexer, nil
}

// BuildDocs returns the documents that need to be indexed or an error.
func BuildDocs(sr result.ScenarioResults, uuid string) ([]interface{}, error) {
	time := time.Now().UTC()

	var docs []interface{}
	if len(sr.Results) < 1 {
		return nil, fmt.Errorf("no result documents")
	}
	for _, r := range sr.Results {
		if len(r.Driver) < 1 {
			continue
		}
		var lo, hi float64
		if r.Samples > 1 {
			_, lo, hi = result.ConfidenceInterval(r.ThroughputSummary, 0.95)
		}
		c := []float64{lo, hi}
		d := Doc{
			UUID:              uuid,
			Timestamp:         time,
			ToolVersion:       sr.Version,
			ToolGitCommit:     sr.GitCommit,
			Driver:            r.Driver,
			HostNetwork:       r.HostNetwork,
			Parallelism:       r.Parallelism,
			Profile:           r.Profile,
			Duration:          r.Duration,
			Virt:              sr.Virt,
			Samples:           r.Samples,
			Service:           r.Service,
			Messagesize:       r.MessageSize,
			Burst:             r.Burst,
			TputMetric:        r.Metric,
			LtcyMetric:        ltcyMetric,
			ServerNodeCPU:     r.ServerMetrics,
			ClientNodeCPU:     r.ClientMetrics,
			ServerPodCPU:      r.ServerPodCPU.Results,
			ServerPodMem:      r.ServerPodMem.MemResults,
			ClientPodMem:      r.ClientPodMem.MemResults,
			ClientPodCPU:      r.ClientPodCPU.Results,
			ClientVSwitchCpu:  r.ClientMetrics.VSwitchCPU,
			ClientVSwiitchMem: r.ClientMetrics.VSwitchMem,
			ServerVSwitchCpu:  r.ServerMetrics.VSwitchCPU,
			ServerVSwitchMem:  r.ServerMetrics.VSwitchMem,
			Metadata:          sr.Metadata,
			AcrossAZ:          r.AcrossAZ,
			Confidence:        c,
			ClientNodeInfo:    r.ClientNodeInfo,
			ServerNodeInfo:    r.ServerNodeInfo,
		}
		UDPLossPercent, e := result.Average(r.LossSummary)
		if e != nil {
			logging.Warn("Unable to process udp loss, setting value to zero")
			d.UDPLossPercent = 0
		} else {
			d.UDPLossPercent = UDPLossPercent
		}
		TCPRetransmit, e := result.Average(r.RetransmitSummary)
		if e != nil {
			logging.Warn("Unable to process tcp retransmits, setting value to zero")
			d.TCPRetransmit = 0
		} else {
			d.TCPRetransmit = TCPRetransmit
		}
		Throughput, e := result.Average(r.ThroughputSummary)
		if e != nil {
			logging.Warn("Unable to process throughput, setting value to zero")
			d.Throughput = 0
		} else {
			d.Throughput = Throughput
		}
		Latency, e := result.Average(r.LatencySummary)
		if e != nil {
			logging.Warn("Unable to process latency, setting value to zero")
			d.Latency = 0
		} else {
			d.Latency = Latency
		}
		docs = append(docs, d)
	}
	return docs, nil
}

// Common csv header fields.
func commonCsvHeaderFields() []string {
	return []string{
		"Driver",
		"Profile",
		"Same node",
		"Host Network",
		"Service",
		"Duration",
		"Parallelism",
		"# of Samples",
		"Message Size",
		"Burst",
		"Confidence metric - low",
		"Confidence metric - high",
	}
}

// Common csv data fields.
func commonCsvDataFields(row result.Data) []string {
	var lo, hi float64
	if row.Samples > 1 {
		_, lo, hi = result.ConfidenceInterval(row.ThroughputSummary, 0.95)
	}
	return []string{
		fmt.Sprint(row.Driver),
		fmt.Sprint(row.Profile),
		fmt.Sprint(row.SameNode),
		fmt.Sprint(row.HostNetwork),
		fmt.Sprint(row.Service),
		strconv.Itoa(row.Duration),
		strconv.Itoa(row.Parallelism),
		strconv.Itoa(row.Samples),
		strconv.Itoa(row.MessageSize),
		strconv.Itoa(row.Burst),
		strconv.FormatFloat(lo, 'f', -1, 64),
		strconv.FormatFloat(hi, 'f', -1, 64),
	}
}

// Writes all the mertics to the archive.
func writeArchive(vswitch, cpuarchive, podarchive, podmemarchive *csv.Writer, role string, row result.Data, podResults []metrics.PodCPU, podMem []metrics.PodMem) error {
	roleFieldData := []string{role}
	for _, pod := range podResults {
		if err := podarchive.Write(append(append(roleFieldData,
			commonCsvDataFields(row)...),
			pod.Name,
			fmt.Sprintf("%f", pod.Value),
		)); err != nil {
			return fmt.Errorf("failed to write archive to file")
		}
	}
	for _, pod := range podMem {
		if err := podmemarchive.Write(append(append(roleFieldData,
			commonCsvDataFields(row)...),
			pod.Name,
			fmt.Sprintf("%f", pod.Value),
		)); err != nil {
			return fmt.Errorf("failed to write archive to file")
		}
	}

	cpu := row.ClientMetrics
	if role == "Server" {
		cpu = row.ServerMetrics
	}
	if err := vswitch.Write(append(append(roleFieldData,
		commonCsvDataFields(row)...),
		fmt.Sprintf("%f", cpu.VSwitchCPU),
		fmt.Sprintf("%f", cpu.VSwitchMem))); err != nil {
		return fmt.Errorf("failed to write archive to file")
	}
	if err := cpuarchive.Write(append(append(roleFieldData,
		commonCsvDataFields(row)...),
		fmt.Sprintf("%f", cpu.Idle),
		fmt.Sprintf("%f", cpu.User),
		fmt.Sprintf("%f", cpu.System),
		fmt.Sprintf("%f", cpu.Iowait),
		fmt.Sprintf("%f", cpu.Steal),
		fmt.Sprintf("%f", cpu.Softirq),
		fmt.Sprintf("%f", cpu.Irq),
	)); err != nil {
		return fmt.Errorf("failed to write archive to file")
	}
	return nil
}

// WritePromCSVResult writes the prom data in CSV format
func WritePromCSVResult(r result.ScenarioResults) error {
	d := time.Now().Unix()

	vswitchfp, err := os.Create(fmt.Sprintf("vswitch-result-%d.csv", d))
	if err != nil {
		return fmt.Errorf("failed to open vswitch archive file")
	}
	defer vswitchfp.Close()
	podmemfp, err := os.Create(fmt.Sprintf("podmem-result-%d.csv", d))
	if err != nil {
		return fmt.Errorf("failed to open pod mem archive file")
	}
	defer podmemfp.Close()
	podfp, err := os.Create(fmt.Sprintf("podcpu-result-%d.csv", d))
	if err != nil {
		return fmt.Errorf("failed to open pod cpu archive file")
	}
	defer podfp.Close()
	cpufp, err := os.Create(fmt.Sprintf("cpu-result-%d.csv", d))
	if err != nil {
		return fmt.Errorf("failed to open cpu archive file")
	}
	defer cpufp.Close()
	vswitch := csv.NewWriter(vswitchfp)
	defer vswitch.Flush()
	cpuarchive := csv.NewWriter(cpufp)
	defer cpuarchive.Flush()
	podarchive := csv.NewWriter(podfp)
	defer podarchive.Flush()
	podmemarchive := csv.NewWriter(podmemfp)
	defer podmemarchive.Flush()

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
	vswtichdata := append(append(roleField,
		commonCsvHeaderFields()...),
		"CPU Utilization",
		"Memory Utilization",
	)
	if err := cpuarchive.Write(cpudata); err != nil {
		return fmt.Errorf("failed to write cpu archive to file")
	}
	if err := podarchive.Write(poddata); err != nil {
		return fmt.Errorf("failed to write pod archive to file")
	}
	if err := podmemarchive.Write(poddata); err != nil {
		return fmt.Errorf("failed to write pod archive to file")
	}
	if err := vswitch.Write(vswtichdata); err != nil {
		return fmt.Errorf("failed to write vswitch archive to file")
	}
	for _, row := range r.Results {
		if err := writeArchive(vswitch, cpuarchive, podarchive, podmemarchive, "Client", row, row.ClientPodCPU.Results, row.ClientPodMem.MemResults); err != nil {
			return err
		}
		if err := writeArchive(vswitch, cpuarchive, podarchive, podmemarchive, "Server", row, row.ServerPodCPU.Results, row.ServerPodMem.MemResults); err != nil {
			return err
		}
	}

	return nil
}

// WriteiPerfSpecificCSV
func WriteSpecificCSV(r result.ScenarioResults) error {
	d := time.Now().Unix()
	fp, err := os.Create(fmt.Sprintf("loss-rt-result-%d.csv", d))
	if err != nil {
		return fmt.Errorf("failed to open archive file")
	}
	defer fp.Close()
	archive := csv.NewWriter(fp)
	defer archive.Flush()
	iperfdata := append(append([]string{"Type"}, commonCsvHeaderFields()...), "Value")
	if err := archive.Write(iperfdata); err != nil {
		return fmt.Errorf("failed to write result archive to file")
	}
	for _, row := range r.Results {
		if strings.Contains(row.Profile, "UDP_STREAM") {
			loss, _ := result.Average(row.LossSummary)
			header := []string{"UDP Percent Loss"}
			data := append(header, commonCsvDataFields(row)...)
			iperfdata = append(data, fmt.Sprintf("%f", loss))
			if err := archive.Write(iperfdata); err != nil {
				return fmt.Errorf("failed to write result archive to file")
			}
		}
		if strings.Contains(row.Profile, "TCP_STREAM") {
			rt, _ := result.Average(row.RetransmitSummary)
			header := []string{"TCP Retransmissions"}
			data := append(header, commonCsvDataFields(row)...)
			iperfdata = append(data, fmt.Sprintf("%f", rt))
			if err := archive.Write(iperfdata); err != nil {
				return fmt.Errorf("failed to write result archive to file")
			}
		}
	}
	return nil
}

// WriteJSONResult sends the results as JSON to stdout
func WriteJSONResult(r result.ScenarioResults) error {
	docs, err := BuildDocs(r, "k8s-netperf")
	if err != nil {
		return err
	}
	p, err := json.MarshalIndent(docs, " ", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(p))
	return nil
}

// WriteCSVResult will write the throughput result to the local filesystem
func WriteCSVResult(r result.ScenarioResults) error {
	d := time.Now().Unix()
	fp, err := os.Create(fmt.Sprintf("result-%d.csv", d))
	if err != nil {
		return fmt.Errorf("failed to open archive file")
	}
	defer fp.Close()
	archive := csv.NewWriter(fp)
	defer archive.Flush()

	data := append(commonCsvHeaderFields(),
		"Avg Throughput",
		"Throughput Metric",
		"99%tile Observed Latency",
		"Latency Metric",
	)

	if err := archive.Write(data); err != nil {
		return fmt.Errorf("failed to write result archive to file")
	}
	for _, row := range r.Results {
		avg, _ := result.Average(row.ThroughputSummary)
		lavg, _ := result.Average(row.LatencySummary)
		data := append(commonCsvDataFields(row),
			fmt.Sprintf("%f", avg),
			row.Metric,
			fmt.Sprint(lavg),
			"usec",
		)
		if err := archive.Write(data); err != nil {
			return fmt.Errorf("failed to write archive to file")
		}
	}
	return nil
}
