package metrics

import (
	"encoding/json"
	"fmt"
	"time"

	ocpmetadata "github.com/cloud-bulldozer/go-commons/v2/ocp-metadata"
	"github.com/cloud-bulldozer/go-commons/v2/prometheus"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
)

// NodeInfo stores the node metadata like IP and Hostname
type NodeInfo struct {
	IP       string `json:"ip"`
	NodeName string `json:"nodeName"`
	corev1.NodeSystemInfo
}

// NodeCPU stores CPU information for a specific Node
type NodeCPU struct {
	Idle       float64 `json:"idleCPU"`
	User       float64 `json:"userCPU"`
	Steal      float64 `json:"stealCPU"`
	System     float64 `json:"systemCPU"`
	Nice       float64 `json:"niceCPU"`
	Irq        float64 `json:"irqCPU"`
	Softirq    float64 `json:"softCPU"`
	Iowait     float64 `json:"ioCPU"`
	VSwitchCPU float64 `json:"vSwitchCPU"`
	VSwitchMem float64 `json:"vSwitchMem"`
}

// PodCPU stores pod CPU
type PodCPU struct {
	Name  string  `json:"podName"`
	Value float64 `json:"cpuUsage"`
}

type PodMem struct {
	Name  string  `json:"podName"`
	Value float64 `json:"memUsage"`
}

// PodValues is a collection of PodCPU
type PodValues struct {
	Results    []PodCPU
	MemResults []PodMem
}

// PromConnect stores the prom information
type PromConnect struct {
	URL           string
	Token         string
	Client        *prometheus.Prometheus
	SkipTLSVerify bool
	OpenShift     bool
	MicroShift    bool
}

// Details stores the node details
type Details struct {
	Metric struct {
		Kernel  string `json:"kernel_version"`
		Kubelet string `json:"kubelet_version"`
	}
}

// Discover finds Prometheus and generates an auth token if necessary.
func Discover(meta *ocpmetadata.Metadata) (PromConnect, bool) {
	var conn PromConnect
	if meta == nil {
		logging.Info("😥 prometheus discovery failure")
		logging.Error("cluster metadata client unavailable")
		return conn, false
	}

	var err error
	conn.URL, conn.Token, err = meta.GetPrometheus()
	if err != nil {
		logging.Info("😥 prometheus discovery failure")
		logging.Error(err)
		return conn, false
	}
	logging.Info("🔬 prometheus discovery succeeded")
	conn.SkipTLSVerify = true
	conn.OpenShift = true
	return conn, true
}

// NodeDetails returns the Details of the nodes. Only returning a single node info.
func NodeDetails(conn PromConnect) Details {
	pd := Details{}
	query, ok := nodeDetailsQuery(conn)
	if !ok {
		logging.Warn("Not able to collect OpenShift specific node info")
		return pd
	}
	if conn.MicroShift {
		value, err := conn.Client.Query(query, time.Now())
		if err != nil {
			logging.Error("Issue querying Prometheus for node_uname_info")
			return pd
		}
		v := value.(model.Vector)
		for _, s := range v {
			pd.Metric.Kernel = string(s.Metric["release"])
			break
		}
		return pd
	}
	value, err := conn.Client.Query(query, time.Now())
	if err != nil {
		logging.Error("Issue querying Prometheus")
		return pd
	}
	status := unmarshalVector(value, &pd)
	if !status {
		logging.Error("cannot unmarshal node information")
	}
	return pd
}

func nodeDetailsQuery(conn PromConnect) (string, bool) {
	if conn.MicroShift {
		return `node_uname_info`, true
	}
	if conn.OpenShift {
		return `kube_node_info`, true
	}
	return "", false
}

// NodeMTU return mtu
func NodeMTU(conn PromConnect) (int, error) {
	if !conn.OpenShift && !conn.MicroShift {
		return 0, fmt.Errorf(" Not able to collect OpenShift specific mtu info ")
	}
	query := `node_network_mtu_bytes`
	value, err := conn.Client.QueryRange(query, time.Now().Add(-time.Minute*1), time.Now(), time.Minute)
	if err != nil {
		return 0, fmt.Errorf("issue querying openshift mtu info from prometheus: %w", err)
	}
	return extractMTU(value)
}

func extractMTU(value model.Value) (int, error) {
	matrix, ok := value.(model.Matrix)
	if !ok {
		return 0, fmt.Errorf("unexpected prometheus response type for mtu query: %T", value)
	}
	for _, stream := range matrix {
		if stream == nil || len(stream.Values) == 0 {
			continue
		}
		return int(stream.Values[len(stream.Values)-1].Value), nil
	}
	return 0, fmt.Errorf("no mtu samples returned from prometheus")
}

const (
	prometheusQueryAttempts       = 3
	prometheusQueryInitialBackoff = time.Second
)

// QueryNodeCPU will return all the CPU usage information for a given node.
func QueryNodeCPU(node NodeInfo, conn PromConnect, start time.Time, end time.Time) (NodeCPU, error) {
	nodeID := nodeIdentifier(node)
	if conn.Client == nil {
		return NodeCPU{}, fmt.Errorf("prometheus client is unavailable for node CPU metrics on %s", nodeID)
	}
	// Node CPU has explicit archive validity markers, so retry transient
	// Prometheus failures before marking the mode breakdown uncollected.
	return queryPrometheusWithRetry("Node CPU metrics query for "+nodeID, prometheusQueryAttempts, prometheusQueryInitialBackoff, func() (NodeCPU, error) {
		return queryNodeCPU(node, conn, start, end)
	})
}

func queryPrometheusWithRetry[T any](description string, attempts int, initialBackoff time.Duration, query func() (T, error)) (T, error) {
	var zero T
	var err error
	backoff := initialBackoff
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		value, queryErr := query()
		if queryErr == nil {
			return value, nil
		}
		err = queryErr
		if attempt < attempts {
			logging.Warnf("%s failed (attempt %d/%d): %v; retrying in %s", description, attempt, attempts, err, backoff)
			if backoff > 0 {
				time.Sleep(backoff)
			}
			backoff *= 2
		}
	}
	return zero, err
}

func queryNodeCPU(node NodeInfo, conn PromConnect, start time.Time, end time.Time) (NodeCPU, error) {
	cpu := NodeCPU{}
	nodeID := nodeIdentifier(node)
	query := nodeCPUQuery(node, conn)
	logging.Debugf("Prom Query : %s", query)
	val, err := conn.Client.QueryRange(query, start, end, time.Minute)
	if err != nil {
		return cpu, fmt.Errorf("querying prometheus for node CPU metrics on %s: %w", nodeID, err)
	}
	return extractNodeCPU(val, nodeID)
}

func extractNodeCPU(val model.Value, nodeID string) (NodeCPU, error) {
	cpu := NodeCPU{}
	v, ok := val.(model.Matrix)
	if !ok {
		return cpu, fmt.Errorf("unexpected prometheus response type for node CPU metrics on %s: %T", nodeID, val)
	}
	if len(v) == 0 {
		return cpu, fmt.Errorf("no node CPU series returned from prometheus for %s", nodeID)
	}
	collected := false
	for _, s := range v {
		if s == nil || len(s.Values) == 0 {
			continue
		}
		switch s.Metric[model.LabelName("mode")] {
		case "idle":
			cpu.Idle = avg(s.Values)
			collected = true
		case "steal":
			cpu.Steal = avg(s.Values)
			collected = true
		case "system":
			cpu.System = avg(s.Values)
			collected = true
		case "user":
			cpu.User = avg(s.Values)
			collected = true
		case "nice":
			cpu.Nice = avg(s.Values)
			collected = true
		case "irq":
			cpu.Irq = avg(s.Values)
			collected = true
		case "softirq":
			cpu.Softirq = avg(s.Values)
			collected = true
		case "iowait":
			cpu.Iowait = avg(s.Values)
			collected = true
		}
	}
	if !collected {
		return cpu, fmt.Errorf("no usable node CPU samples returned from prometheus for %s", nodeID)
	}
	return cpu, nil
}

func nodeIdentifier(node NodeInfo) string {
	if node.NodeName != "" {
		return node.NodeName
	}
	if node.IP != "" {
		return node.IP
	}
	return "unknown node"
}

func nodeCPUQuery(node NodeInfo, conn PromConnect) string {
	if conn.OpenShift || conn.MicroShift {
		// OpenShift and MicroShift expose node-exporter instance labels as node names.
		return fmt.Sprintf("(avg by(mode) (rate(node_cpu_seconds_total{instance=~\"%s\"}[2m])) * 100)", node.NodeName)
	}
	return fmt.Sprintf("(avg by(mode) (rate(node_cpu_seconds_total{instance=~\"%s:.*\"}[2m])) * 100)", node.IP)
}

// TopPodCPU will return the top 10 CPU consumers for a specific node
func TopPodCPU(node NodeInfo, conn PromConnect, start time.Time, end time.Time) (PodValues, bool) {
	var pods PodValues
	query := topPodCPUQuery(node, conn)
	logging.Debugf("Prom Query : %s", query)
	val, err := conn.Client.QueryRange(query, start, end, time.Minute)
	if err != nil {
		logging.Error("Issue querying Prometheus")
		return pods, false
	}
	v := val.(model.Matrix)
	for _, s := range v {
		p := PodCPU{
			Name:  string(s.Metric["pod"]),
			Value: avg(s.Values),
		}
		pods.Results = append(pods.Results, p)
	}
	return pods, true
}

func topPodCPUQuery(node NodeInfo, conn PromConnect) string {
	if conn.MicroShift {
		// MicroShift cAdvisor metrics use the node name in the instance label.
		return fmt.Sprintf("topk(10,sum(irate(container_cpu_usage_seconds_total{name!=\"\",instance=~\"%s\"}[2m]) * 100) by (pod, namespace, instance))", node.NodeName)
	}
	return fmt.Sprintf("topk(10,sum(irate(container_cpu_usage_seconds_total{name!=\"\",instance=~\"%s:.*\"}[2m]) * 100) by (pod, namespace, instance))", node.IP)
}

// VSwitchCPU will return the vswitchd cpu usage for specific node
func VSwitchCPU(node NodeInfo, conn PromConnect, start time.Time, end time.Time, ndata *NodeCPU) bool {
	query := vSwitchCPUQuery(node, conn)
	logging.Debugf("Prom Query : %s", query)
	val, err := conn.Client.QueryRange(query, start, end, time.Minute)
	if err != nil {
		logging.Error("Issue querying Prometheus")
		return false
	}
	v := val.(model.Matrix)
	for _, s := range v {
		ndata.VSwitchCPU = avg(s.Values)
	}
	return true
}

func vSwitchCPUQuery(node NodeInfo, conn PromConnect) string {
	if conn.MicroShift {
		// MicroShift exposes OVS through namedprocess metrics.
		return fmt.Sprintf("sum(irate(namedprocess_namegroup_cpu_seconds_total{groupname=~\"ovs-vswitchd\",instance=~\"%s\"}[2m]))*100", node.NodeName)
	}
	return fmt.Sprintf("irate(container_cpu_usage_seconds_total{id=~\"/system.slice/ovs-vswitchd.service\", node=~\"%s\"}[2m])*100", node.NodeName)
}

// VSwitchMem will return the vswitchd memory usage for specific node
func VSwitchMem(node NodeInfo, conn PromConnect, start time.Time, end time.Time, ndata *NodeCPU) bool {
	query := vSwitchMemQuery(node, conn)
	logging.Debugf("Prom Query : %s", query)
	val, err := conn.Client.QueryRange(query, start, end, time.Minute)
	if err != nil {
		logging.Error("Issue querying Prometheus")
		return false
	}
	v := val.(model.Matrix)
	for _, s := range v {
		ndata.VSwitchMem = avg(s.Values)
	}
	return true
}

func vSwitchMemQuery(node NodeInfo, conn PromConnect) string {
	if conn.MicroShift {
		// MicroShift exposes OVS through namedprocess metrics.
		return fmt.Sprintf("namedprocess_namegroup_memory_bytes{groupname=~\"ovs-vswitchd\",memtype=~\"resident\",instance=~\"%s\"}", node.NodeName)
	}
	return fmt.Sprintf("container_memory_rss{id=~\"/system.slice/ovs-vswitchd.service\", node=~\"%s\"}", node.NodeName)
}

// TopPodMem will return the top 10 Mem consumers for a specific node
func TopPodMem(node NodeInfo, conn PromConnect, start time.Time, end time.Time) (PodValues, bool) {
	var pods PodValues
	query := topPodMemQuery(node, conn)
	logging.Debugf("Prom Query : %s", query)
	val, err := conn.Client.QueryRange(query, start, end, time.Minute)
	if err != nil {
		logging.Error("Issue querying Prometheus")
		return pods, false
	}
	v := val.(model.Matrix)
	for _, s := range v {
		p := PodMem{
			Name:  string(s.Metric["pod"]),
			Value: avg(s.Values),
		}
		pods.MemResults = append(pods.MemResults, p)
	}
	return pods, true
}

func topPodMemQuery(node NodeInfo, conn PromConnect) string {
	if conn.MicroShift {
		// MicroShift cAdvisor metrics use the node name in the instance label.
		return fmt.Sprintf("topk(10,sum(container_memory_rss{container!=\"POD\",name!=\"\",instance=~\"%s\"}) by (pod, namespace, instance))", node.NodeName)
	}
	return fmt.Sprintf("topk(10,sum(container_memory_rss{container!=\"POD\",name!=\"\",node=~\"%s\"}) by (pod, namespace, node))", node.NodeName)
}

// Calculates average for the given data
func avg(data []model.SamplePair) float64 {
	sum := 0.0
	for s := range data {
		sum += float64(data[s].Value)
	}
	return sum / float64(len(data))
}

// Unmarshals the vector to a given type
func unmarshalVector(value model.Value, pd interface{}) bool {
	v := value.(model.Vector)
	for _, s := range v {
		d, _ := s.MarshalJSON()
		error := json.Unmarshal(d, &pd)
		if error != nil {
			continue
		} else {
			return true
		}
	}
	return false
}
