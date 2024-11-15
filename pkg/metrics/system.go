package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	ocpmetadata "github.com/cloud-bulldozer/go-commons/ocp-metadata"
	"github.com/cloud-bulldozer/go-commons/prometheus"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/clientcmd"
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

// Discover is to find Prometheus and generate an auth token if necessary.
func Discover() (PromConnect, bool) {
	var conn PromConnect
	kconfig := os.Getenv("KUBECONFIG")
	config, err := clientcmd.BuildConfigFromFlags("", kconfig)
	if err != nil {
		logging.Error(err)
		return conn, false
	}
	ocpMetadata, err := ocpmetadata.NewMetadata(config)
	if err != nil {
		logging.Error(err)
		return conn, false
	}
	conn.URL, conn.Token, err = ocpMetadata.GetPrometheus()
	if err != nil {
		logging.Info("😥 prometheus discovery failure")
		logging.Error(err)
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
	if !conn.OpenShift {
		logging.Warn("Not able to collect OpenShift specific node info")
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
	if !conn.OpenShift {
		return 0, fmt.Errorf(" Not able to collect OpenShift specific mtu info ")
	}
	query := `node_network_mtu_bytes`
	value, err := conn.Client.QueryRange(query, time.Now().Add(-time.Minute*1), time.Now(), time.Minute)
	if err != nil {
		return 0, fmt.Errorf("Issue querying openshift mtu info from prometheus")
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
