package metrics

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"gihub.com/jtaleric/k8s-netperf/logging"
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
	Idle    float64
	User    float64
	Steal   float64
	System  float64
	Nice    float64
	Irq     float64
	Softirq float64
	Iowait  float64
}

// PodCPU stores pod CPU
type PodCPU struct {
	Name  string
	Value float64
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
				ExpirationSeconds: pointer.Int64Ptr(int64(expire.Seconds())),
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
