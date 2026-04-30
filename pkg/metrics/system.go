package metrics

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	ocpmetadata "github.com/cloud-bulldozer/go-commons/ocp-metadata"
	"github.com/cloud-bulldozer/go-commons/prometheus"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
)

// JsonFloat64 is a float64 that always marshals with a decimal point,
// ensuring OpenSearch consistently maps the field as a float type.
type JsonFloat64 float64

func (f JsonFloat64) MarshalJSON() ([]byte, error) {
	v := float64(f)
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return []byte("0.0"), nil
	}
	s := strconv.FormatFloat(v, 'f', -1, 64)
	// Ensure there's always a decimal point
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return []byte(s), nil
}

func (f *JsonFloat64) UnmarshalJSON(data []byte) error {
	var v float64
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*f = JsonFloat64(v)
	return nil
}

// NodeInfo stores the node metadata like IP and Hostname
type NodeInfo struct {
	IP       string `json:"ip"`
	NodeName string `json:"nodeName"`
	corev1.NodeSystemInfo
}

// NodeCPU stores CPU information for a specific Node
type NodeCPU struct {
	Idle       JsonFloat64 `json:"idleCPU"`
	User       JsonFloat64 `json:"userCPU"`
	Steal      JsonFloat64 `json:"stealCPU"`
	System     JsonFloat64 `json:"systemCPU"`
	Nice       JsonFloat64 `json:"niceCPU"`
	Irq        JsonFloat64 `json:"irqCPU"`
	Softirq    JsonFloat64 `json:"softCPU"`
	Iowait     JsonFloat64 `json:"ioCPU"`
	VSwitchCPU JsonFloat64 `json:"vSwitchCPU"`
	VSwitchMem JsonFloat64 `json:"vSwitchMem"`
}

// PodCPU stores pod CPU
type PodCPU struct {
	Name  string      `json:"podName"`
	Value JsonFloat64 `json:"cpuUsage"`
}

type PodMem struct {
	Name  string      `json:"podName"`
	Value JsonFloat64 `json:"memUsage"`
}

// PodValues is a collection of PodCPU
type PodValues struct {
	Results    []PodCPU
	MemResults []PodMem
}

// PromConnect stores the prom information
type PromConnect struct {
	URL       string
	Token     string
	Client    *prometheus.Prometheus
	Verify    bool
	OpenShift bool
}

// Details stores the node details
type Details struct {
	Metric struct {
		Kernel  string `json:"kernel_version"`
		Kubelet string `json:"kubelet_version"`
	}
}

// Discover tries to find Prometheus via OpenShift routes using the provided rest config.
func Discover(config *rest.Config) (PromConnect, bool) {
	var conn PromConnect
	if config == nil {
		logging.Debug("No rest config provided, skipping Prometheus discovery")
		return conn, false
	}
	ocpMetadata, err := ocpmetadata.NewMetadata(config)
	if err != nil {
		logging.Debug("Unable to initialize OCP metadata, likely not an OpenShift cluster")
		return conn, false
	}
	conn.URL, conn.Token, err = ocpMetadata.GetPrometheus()
	if err != nil {
		logging.Debug("Prometheus auto-discovery not available (non-OpenShift cluster or route not exposed)")
		return conn, false
	}
	logging.Info("🔬 prometheus discovered at openshift-monitoring")
	conn.Verify = true
	conn.OpenShift = true
	return conn, true
}

// NodeDetails returns the Details of the nodes. Only returning a single node info.
func NodeDetails(conn PromConnect) Details {
	pd := Details{}
	if conn.Client == nil {
		return pd
	}
	query := `kube_node_info`
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

// NodeMTU return mtu
func NodeMTU(conn PromConnect) (int, error) {
	if conn.Client == nil {
		return 0, fmt.Errorf("prometheus client not available for MTU query")
	}
	query := `node_network_mtu_bytes`
	value, err := conn.Client.QueryRange(query, time.Now().Add(-time.Minute*1), time.Now(), time.Minute)
	if err != nil {
		return 0, fmt.Errorf("issue querying openshift mtu info from prometheus")
	}
	mtu := int(value.(model.Matrix)[0].Values[0].Value)
	return mtu, nil
}

// QueryNodeCPU will return all the CPU usage information for a given node
func QueryNodeCPU(node NodeInfo, conn PromConnect, start time.Time, end time.Time) (NodeCPU, bool) {
	cpu := NodeCPU{}
	query := fmt.Sprintf("(avg by(mode) (rate(node_cpu_seconds_total{instance=~\"%s:.*\"}[2m])) * 100)", node.IP)
	if conn.OpenShift {
		// OpenShift changes the instance in its metrics.
		query = fmt.Sprintf("(avg by(mode) (rate(node_cpu_seconds_total{instance=~\"%s\"}[2m])) * 100)", node.NodeName)
	}
	logging.Debugf("Prom Query : %s", query)
	val, err := conn.Client.QueryRange(query, start, end, time.Minute)
	if err != nil {
		logging.Error("Issue querying Prometheus")
		return cpu, false
	}
	v := val.(model.Matrix)
	for _, s := range v {
		if strings.Contains(s.Metric.String(), "idle") {
			cpu.Idle = avg(s.Values)
		}
		if strings.Contains(s.Metric.String(), "steal") {
			cpu.Steal = avg(s.Values)
		}
		if strings.Contains(s.Metric.String(), "system") {
			cpu.System = avg(s.Values)
		}
		if strings.Contains(s.Metric.String(), "user") {
			cpu.User = avg(s.Values)
		}
		if strings.Contains(s.Metric.String(), "nice") {
			cpu.Nice = avg(s.Values)
		}
		if strings.Contains(s.Metric.String(), "\"irq\"") {
			cpu.Irq = avg(s.Values)
		}
		if strings.Contains(s.Metric.String(), "softirq") {
			cpu.Softirq = avg(s.Values)
		}
		if strings.Contains(s.Metric.String(), "iowait") {
			cpu.Iowait = avg(s.Values)
		}
	}
	return cpu, true
}

// TopPodCPU will return the top 10 CPU consumers for a specific node
func TopPodCPU(node NodeInfo, conn PromConnect, start time.Time, end time.Time) (PodValues, bool) {
	var pods PodValues
	query := fmt.Sprintf("topk(10,sum(irate(container_cpu_usage_seconds_total{name!=\"\",instance=~\"%s:.*\"}[2m]) * 100) by (pod, namespace, instance))", node.IP)
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

// VSwitchCPU will return the vswitchd cpu usage for specific node
func VSwitchCPU(node NodeInfo, conn PromConnect, start time.Time, end time.Time, ndata *NodeCPU) bool {
	query := fmt.Sprintf("irate(container_cpu_usage_seconds_total{id=~\"/system.slice/ovs-vswitchd.service\", node=~\"%s\"}[2m])*100", node.NodeName)
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

// VSwitchMem will return the vswitchd cpu usage for specific node
func VSwitchMem(node NodeInfo, conn PromConnect, start time.Time, end time.Time, ndata *NodeCPU) bool {
	query := fmt.Sprintf("container_memory_rss{id=~\"/system.slice/ovs-vswitchd.service\", node=~\"%s\"}", node.NodeName)
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

// TopPodMem will return the top 10 Mem consumers for a specific node
func TopPodMem(node NodeInfo, conn PromConnect, start time.Time, end time.Time) (PodValues, bool) {
	var pods PodValues
	query := fmt.Sprintf("topk(10,sum(container_memory_rss{container!=\"POD\",name!=\"\",node=~\"%s\"}) by (pod, namespace, node))", node.NodeName)
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

// Calculates average for the given data
func avg(data []model.SamplePair) JsonFloat64 {
	sum := 0.0
	for s := range data {
		sum += float64(data[s].Value)
	}
	return JsonFloat64(sum / float64(len(data)))
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
