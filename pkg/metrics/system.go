package metrics

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	routev1 "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	api "github.com/prometheus/client_golang/api"
	prom "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	auth "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/pointer"
)

// NodeInfo stores the node metadata like IP and Hostname
type NodeInfo struct {
	IP       string
	Hostname string
}

// NodeCPU stores CPU information for a specific Node
type NodeCPU struct {
	Idle    float64 `json:"idleCPU"`
	User    float64 `json:"userCPU"`
	Steal   float64 `json:"stealCPU"`
	System  float64 `json:"systemCPU"`
	Nice    float64 `json:"niceCPU"`
	Irq     float64 `json:"irqCPU"`
	Softirq float64 `json:"softCPU"`
	Iowait  float64 `json:"ioCPU"`
}

// PodCPU stores pod CPU
type PodCPU struct {
	Name  string  `json:"podName"`
	Value float64 `json:"cpuUsage"`
}

// PodValues is a collection of PodCPU
type PodValues struct {
	Results []PodCPU
}

// PromConnect stores the prom information
type PromConnect struct {
	URL       string
	Token     string
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
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		logging.Error(err)
		return conn, false
	}
	found := false
	_, err = client.CoreV1().Namespaces().Get(context.TODO(), "openshift-monitoring", metav1.GetOptions{})
	if err != nil {
		logging.Info("😥 openshift-monitoring namespace not found")
		_, err := client.CoreV1().Namespaces().Get(context.TODO(), "monitoring", metav1.GetOptions{})
		if err != nil {
			logging.Info("😥 monitoring namespace not found")
		} else {
			logging.Info("🔬 monitoring namespace found")
			// Not much we can do here, if the user provided URL fails, it could be due to a bad port-forward
			found = true
			conn.OpenShift = false
		}
	} else {
		expire := time.Hour * 2
		logging.Info("🔬 openshift-monitoring namespace found")
		conn.Verify = true
		found = true
		r, err := routev1.NewForConfig(config)
		if err != nil {
			logging.Error(err)
			return conn, false
		}
		proute, err := r.Routes("openshift-monitoring").Get(context.TODO(), "prometheus-k8s", metav1.GetOptions{})
		if err != nil {
			logging.Error(err)
			return conn, false
		}
		logging.Debugf("Found OpenShift Monitoring Route : %s", proute.Spec.Host)
		conn.URL = fmt.Sprintf("https://%s", proute.Spec.Host)
		request := auth.TokenRequest{
			Spec: auth.TokenRequestSpec{
				ExpirationSeconds: pointer.Int64(int64(expire.Seconds())),
			},
		}
		token, err := client.CoreV1().ServiceAccounts("openshift-monitoring").CreateToken(context.TODO(), "prometheus-k8s", &request, v1.CreateOptions{})
		if err != nil {
			logging.Error(err)
			return conn, false
		}
		conn.Token = token.Status.Token
		logging.Debugf("Token : %s", conn.Token)
		conn.OpenShift = true
	}
	if !found {
		return conn, false
	}
	return conn, true
}

// PromCheck will do a simple query, discard the results
func PromCheck(conn PromConnect) bool {
	_, ret := promQueryRange(time.Now(), time.Now(), "node_cpu_seconds_total{}", conn)
	return ret
}

// NodeDetails returns the Details of the nodes. Only returning a single node info.
func NodeDetails(conn PromConnect) Details {
	pd := Details{}
	if !conn.OpenShift {
		logging.Warn("Not able to collect OpenShift specific node info")
		return pd
	}
	query := `kube_node_info`
	val, q := promQueryRange(time.Now().Add(-time.Minute*1), time.Now(), query, conn)
	if !q {
		logging.Error("Issue querying Prometheus")
		return pd
	}
	if val.Type() == model.ValMatrix {
		v := val.(model.Matrix)
		for _, s := range v {
			d, _ := s.MarshalJSON()
			error := json.Unmarshal(d, &pd)
			if error != nil {
				logging.Error("cannot unmarshal node information")
			} else {
				break
			}
		}
	}
	return pd

}

// Platform returns the platform
func Platform(conn PromConnect) string {
	type Data struct {
		Metric struct {
			Platform string `json:"type"`
		}
	}
	pd := Data{}
	if !conn.OpenShift {
		logging.Warn("Not able to collect OpenShift Specific version info")
		return ""
	}
	query := `cluster_infrastructure_provider`
	val, q := promQueryRange(time.Now().Add(-time.Minute*1), time.Now(), query, conn)
	if !q {
		logging.Error("Issue querying Prometheus")
		return ""
	}
	if val.Type() == model.ValMatrix {
		v := val.(model.Matrix)
		for _, s := range v {
			d, _ := s.MarshalJSON()
			error := json.Unmarshal(d, &pd)
			if error != nil {
				logging.Errorf(" Cannot unmarshal Promquery ")
			}
		}
	}
	return pd.Metric.Platform

}

// OCPversion returns the Cluster version
func OCPversion(conn PromConnect, start time.Time, end time.Time) string {
	type Data struct {
		Metric struct {
			Version string `json:"version"`
		}
	}
	vd := Data{}
	ver := ""
	if !conn.OpenShift {
		logging.Warn("Not able to collect OpenShift Specific version info")
		return ver
	}
	query := `cluster_version{type="current"}`
	val, q := promQueryRange(start, end, query, conn)
	if !q {
		logging.Error("Issue querying Prometheus")
		return ver
	}
	if val.Type() == model.ValMatrix {
		v := val.(model.Matrix)
		for _, s := range v {
			d, _ := s.MarshalJSON()
			error := json.Unmarshal(d, &vd)
			if error != nil {
				logging.Error(" Cannot unmarshal the OCP Cluster information ")
			}
		}
	}
	return vd.Metric.Version
}

// NodeMTU return mtu
func NodeMTU(conn PromConnect) (int, error) {
	if !conn.OpenShift {
		return 0, fmt.Errorf(" Not able to collect OpenShift specific mtu info ")
	}
	query := `node_network_mtu_bytes`
	val, q := promQueryRange(time.Now().Add(-time.Minute*1), time.Now(), query, conn)
	if !q {
		return 0, fmt.Errorf(" Issue querying Prometheus ")
	}
	var mtu int
	if val.Type() == model.ValMatrix {
		v := val.(model.Matrix)
		for _, s := range v {
			d := s.Values
			mtu = int(d[0].Value)
			break
		}
	}
	return mtu, nil

}

// IPSecEnabled checks if IPsec
func IPSecEnabled(conn PromConnect, start time.Time, end time.Time) (bool, error) {
	if !conn.OpenShift {
		return false, fmt.Errorf(" Not able to collect OpenShift specific ovn ipsec info ")
	}
	query := `ovnkube_master_ipsec_enabled`
	val, q := promQueryRange(start, end, query, conn)
	if !q {
		return false, fmt.Errorf(" Issue querying Prometheus ")
	}
	var ipsec int
	if val.Type() == model.ValMatrix {
		v := val.(model.Matrix)
		for _, s := range v {
			d := s.Values
			ipsec = int(d[0].Value)
			break
		}
	}
	if ipsec == 0 {
		return false, nil
	}
	return true, nil

}

// QueryNodeCPU will return all the CPU usage information for a given node
func QueryNodeCPU(node NodeInfo, conn PromConnect, start time.Time, end time.Time) (NodeCPU, bool) {
	cpu := NodeCPU{}
	query := fmt.Sprintf("(avg by(mode) (rate(node_cpu_seconds_total{instance=~\"%s:.*\"}[2m])) * 100)", node.IP)
	if conn.OpenShift {
		// OpenShift changes the instance in its metrics.
		query = fmt.Sprintf("(avg by(mode) (rate(node_cpu_seconds_total{instance=~\"%s\"}[2m])) * 100)", node.Hostname)
	}
	logging.Debugf("Prom Query : %s", query)
	val, q := promQueryRange(start, end, query, conn)
	if !q {
		logging.Error("Issue querying Prometheus")
		return cpu, false
	}
	if val.Type() == model.ValMatrix {
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

	}
	return cpu, true
}

// TopPodCPU will return the top 5 CPU consumers for a specific node
func TopPodCPU(node NodeInfo, conn PromConnect, start time.Time, end time.Time) (PodValues, bool) {
	var pods PodValues
	query := fmt.Sprintf("topk(5,sum(irate(container_cpu_usage_seconds_total{name!=\"\",instance=~\"%s:.*\"}[2m]) * 100) by (pod, namespace, instance))", node.IP)
	logging.Debugf("Prom Query : %s", query)
	val, q := promQueryRange(start, end, query, conn)
	if !q {
		logging.Error("Issue querying Prometheus")
		return pods, false
	}
	if val.Type() == model.ValMatrix {
		v := val.(model.Matrix)
		for _, s := range v {
			p := PodCPU{
				Name:  string(s.Metric["pod"]),
				Value: avg(s.Values),
			}
			pods.Results = append(pods.Results, p)
		}
	}
	return pods, true
}

func avg(data []model.SamplePair) float64 {
	sum := 0.0
	for s := range data {
		sum += float64(data[s].Value)
	}
	return sum / float64(len(data))
}

// We will only use Bearer auth for prom (no user/pass)
type authTransport struct {
	Transport http.RoundTripper
	token     string
}

// RoundTrip takes http.Request
// Todo : Determine if we want to allow basicauth username/pass
func (auth authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", auth.token))
	return auth.Transport.RoundTrip(req)
}

// promQueryRange
func promQueryRange(start time.Time, end time.Time, query string, conn PromConnect) (model.Value, bool) {
	cfg := api.Config{
		Address: conn.URL,
	}
	if conn.OpenShift {
		cfg = api.Config{
			Address: conn.URL,
			RoundTripper: authTransport{
				Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{InsecureSkipVerify: conn.Verify}},
				token:     conn.Token,
			},
		}
	}
	client, err := api.NewClient(cfg)
	if err != nil {
		logging.Errorf("Error creating client: %v\n", err)
		return nil, false
	}
	v1api := prom.NewAPI(client)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := prom.Range{
		Start: start,
		End:   end,
		Step:  time.Minute,
	}
	result, warnings, err := v1api.QueryRange(ctx, query, r, prom.WithTimeout(5*time.Second))
	if err != nil {
		logging.Errorf("Error querying Prometheus: %v\n", err)
		return nil, false
	}
	if len(warnings) > 0 {
		logging.Warnf("Warnings: %v\n", warnings)
	}
	return result, true
}
