package drivers

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	apiv1 "k8s.io/api/core/v1"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/k8s"
	log "github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/sample"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

var Netperf netperf

func init() {
	Netperf = netperf{
		driverName: "netperf",
	}
}

const superNetperf = "super-netperf"

// omniOptions are netperf specific options that we will pass to the netperf client.
const omniOptions = "rt_latency,p99_latency,throughput,throughput_units,remote_recv_calls,local_send_calls,local_transport_retrans"

// Run will use the k8s client to run the netperf binary in the container image
// it will return a bytes.Buffer of the stdout.
func (n *netperf) Run(c *kubernetes.Clientset, rc rest.Config, nc config.Config, client apiv1.PodList, serverIP string) (bytes.Buffer, error) {
	var stdout, stderr bytes.Buffer
	pod := client.Items[0]
	log.Debugf("🔥 Client (%s,%s) starting netperf against server: %s", pod.Name, pod.Status.PodIP, serverIP)
	config.Show(nc, n.driverName)
	cmd := []string{superNetperf, strconv.Itoa(nc.Parallelism), strconv.Itoa(k8s.NetperfServerDataPort), "-H",
		serverIP, "-l",
		fmt.Sprint(nc.Duration),
		"-t", nc.Profile,
		"--",
		"-k", fmt.Sprint(omniOptions),
		"-m", fmt.Sprint(nc.MessageSize),
		"-R", "1"}
	log.Debug(cmd)
	req := c.CoreV1().RESTClient().
		Post().
		Namespace(pod.Namespace).
		Resource("pods").
		Name(pod.Name).
		SubResource("exec").
		VersionedParams(&apiv1.PodExecOptions{
			Container: pod.Spec.Containers[0].Name,
			Command:   cmd,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       true,
		}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(&rc, "POST", req.URL())
	if err != nil {
		return stdout, err
	}
	// Connect this process' std{in,out,err} to the remote shell process.
	err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return stdout, err
	}
	log.Debug(strings.TrimSpace(stdout.String()))
	return stdout, nil
}

// ParseResults accepts the stdout from the execution of the benchmark. It also needs
// It will return a Sample struct or error
func (n *netperf) ParseResults(stdout *bytes.Buffer, _ config.Config) (sample.Sample, error) {
	sample := sample.Sample{}
	sample.Driver = n.driverName
	send := 0.0
	recv := 0.0
	if len(strings.Split(stdout.String(), "\n")) < 5 {
		return sample, fmt.Errorf("Length of output from netperf was too short.")
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		l := strings.Split(line, "=")
		if len(l) < 2 {
			continue
		}
		if strings.Contains(l[0], "THROUGHPUT_UNITS") {
			sample.Metric = l[1]
		} else if strings.Contains(l[0], "THROUGHPUT") {
			if len(strings.TrimSpace(l[1])) < 1 {
				return sample, fmt.Errorf("Throughput was empty.")
			}
			sample.Throughput, _ = strconv.ParseFloat(strings.Trim(l[1], "\r"), 64)
		} else if strings.Contains(l[0], "P99_LATENCY") {
			if len(strings.TrimSpace(l[1])) < 1 {
				return sample, fmt.Errorf("P99_Latency was empty.")
			}
			sample.Latency99ptile, _ = strconv.ParseFloat(strings.Trim(l[1], "\r"), 64)
		} else if strings.Contains(l[0], "RT_LATENCY") {
			sample.Latency, _ = strconv.ParseFloat(strings.Trim(l[1], "\r"), 64)
		} else if strings.Contains(l[0], "LOCAL_SEND_CALLS") {
			send, _ = strconv.ParseFloat(strings.Trim(l[1], "\r"), 64)
		} else if strings.Contains(l[0], "REMOTE_RECV_CALLS") {
			recv, _ = strconv.ParseFloat(strings.Trim(l[1], "\r"), 64)
		} else if strings.Contains(l[0], "LOCAL_TRANSPORT_RETRANS") {
			sample.Retransmits, _ = strconv.ParseFloat(strings.Trim(l[1], "\r"), 64)
		}
	}
	if math.IsNaN(sample.Throughput) {
		return sample, fmt.Errorf("Throughput value is NaN")
	}
	if math.IsNaN(sample.Latency99ptile) {
		return sample, fmt.Errorf("Latency value is NaN")
	}
	// Negative values will mean UDP_STREAM
	if sample.Retransmits < 0.0 {
		sample.LossPercent = 100 - (recv / send * 100)
	} else {
		sample.LossPercent = 0
	}
	return sample, nil
}

// IsTestSupported Determine if the test is supported for driver
func (n *netperf) IsTestSupported(test string) bool {
	return true
}
