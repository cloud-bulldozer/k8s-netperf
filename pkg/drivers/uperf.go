package drivers

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	apiv1 "k8s.io/api/core/v1"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/k8s"
	log "github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/sample"
	"github.com/montanaflynn/stats"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

type Result struct {
	Data struct {
		TCPRetransmit struct {
			Count float64 `json:"retransmits"`
		} `json:"sum_sent"`
		TCPStream struct {
			Rate float32 `json:"bits_per_second"`
		} `json:"sum_received"`
		UDPStream struct {
			Rate        float64 `json:"bits_per_second"`
			LossPercent float64 `json:"lost_percent"`
		} `json:"sum"`
	} `json:"end"`
}

var Uperf uperf

func init() {
	Uperf = uperf{
		driverName: "uperf",
	}
}

// TestSupported Determine if the test is supported for driver
func (u *uperf) IsTestSupported(test string) bool {
	return !strings.Contains(test, "TCP_CRR")
}

// uperf needs "rr" or "stream" profiles which are config files passed to uperf command through -m option
// We need to create these profiles based on the test using provided configuration
func createUperfProfile(c *kubernetes.Clientset, rc rest.Config, nc config.Config, pod apiv1.Pod, serverIP string) (string, error) {
	var stdout, stderr bytes.Buffer

	var fileContent string
	var filePath string

	protocol := "tcp"
	if strings.Contains(nc.Profile, "UDP") {
		protocol = "udp"
	}

	if strings.Contains(nc.Profile, "STREAM") {
		fileContent = fmt.Sprintf(`<?xml version=1.0?>
		<profile name="stream-%s-%d-%d">
		<group nprocs="%d">
		<transaction iterations="1">
		  <flowop type="connect" options="remotehost=%s protocol=%s port=%d"/>
		</transaction>
		<transaction duration="%d">		  
		  <flowop type=write options="count=16 size=%d"/>
		<transaction iterations="1">
		  <flowop type=disconnect />
		</transaction>
		</group>		
		</profile>`, protocol, nc.MessageSize, nc.Parallelism, nc.Parallelism, serverIP, protocol, k8s.UperfServerCtlPort+1, nc.Duration, nc.MessageSize)
		filePath = fmt.Sprintf("/tmp/uperf-stream-%s-%d-%d", protocol, nc.MessageSize, nc.Parallelism)
	} else {
		fileContent = fmt.Sprintf(`<?xml version=1.0?>		
		<profile name="rr-%s-%d-%d">
		<group nprocs="%d">
		<transaction iterations="1">
		  <flowop type="connect" options="remotehost=%s protocol=%s port=%d"/>
		</transaction>
		<transaction duration="%d">
		  <flowop type=write options="size=%d"/>
		  <flowop type=read  options="size=%d"/>		  
		</transaction>
		<transaction iterations="1">
		  <flowop type=disconnect />
		</transaction>
		</group>		
		</profile>`, protocol, nc.MessageSize, nc.Parallelism, nc.Parallelism, serverIP, protocol, k8s.UperfServerCtlPort+1, nc.Duration, nc.MessageSize, nc.MessageSize)
		filePath = fmt.Sprintf("/tmp/uperf-rr-%s-%d-%d", protocol, nc.MessageSize, nc.Parallelism)
	}

	var cmd []string
	uperfCmd := "echo '" + fileContent + "' > " + filePath
	cmd = []string{"bash", "-c", uperfCmd}

	//Empty buffer
	stdout = bytes.Buffer{}

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
		return filePath, err
	}
	// Connect this process' std{in,out,err} to the remote shell process.
	err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return filePath, err
	}

	log.Debug(strings.TrimSpace(stdout.String()))
	return filePath, nil
}

// Run will invoke uperf in a client container

func (u *uperf) Run(c *kubernetes.Clientset, rc rest.Config, nc config.Config, client apiv1.PodList, serverIP string) (bytes.Buffer, error) {
	var stdout, stderr bytes.Buffer
	var exec remotecommand.Executor

	pod := client.Items[0]
	log.Debugf("🔥 Client (%s,%s) starting uperf against server : %s", pod.Name, pod.Status.PodIP, serverIP)
	config.Show(nc, u.driverName)

	filePath, err := createUperfProfile(c, rc, nc, pod, serverIP)
	if err != nil {
		return stdout, err
	}

	//Empty buffer
	stdout = bytes.Buffer{}
	stderr = bytes.Buffer{}

	cmd := []string{"uperf", "-v", "-a", "-R", "-i", "1", "-m", filePath, "-P", fmt.Sprint(k8s.UperfServerCtlPort)}
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
	exec, err = remotecommand.NewSPDYExecutor(&rc, "POST", req.URL())
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

	return stdout, nil
}

// ParseResults accepts the stdout from the execution of the benchmark.
// It will return a Sample struct or error
func (u *uperf) ParseResults(stdout *bytes.Buffer) (sample.Sample, error) {
	sample := sample.Sample{}
	sample.Driver = u.driverName
	sample.Metric = "Mb/s"

	transactions := regexp.MustCompile(`timestamp_ms:(.*) name:Txn2 nr_bytes:(.*) nr_ops:(.*)\r`).FindAllStringSubmatch(stdout.String(), -1)

	var prevTimestamp, normLtcy float64
	var prevBytes, prevOps, normOps float64
	var byteSummary, latSummary, opSummary []float64

	for _, transaction := range transactions {

		timestamp, _ := strconv.ParseFloat(transaction[1], 64)
		bytes, _ := strconv.ParseFloat(transaction[2], 64)
		ops, _ := strconv.ParseFloat(transaction[3], 64)

		normOps = ops - prevOps
		if normOps != 0 && prevTimestamp != 0.0 {
			normLtcy = ((timestamp - prevTimestamp) / float64(normOps)) * 1000
			byteSummary = append(byteSummary, bytes-prevBytes)
			latSummary = append(latSummary, float64(normLtcy))
			opSummary = append(opSummary, normOps)
		}
		prevTimestamp, prevBytes, prevOps = timestamp, bytes, ops

	}
	averageByte, _ := stats.Mean(byteSummary)
	averageOps, _ := stats.Mean(opSummary)
	sample.Throughput = float64(averageByte*8) / 1000000
	sample.Latency99ptile, _ = stats.Percentile(latSummary, 99)
	log.Debugf("Storing uperf sample throughput: %f Mbps, P99 Latency %f, Average ops: %f ", sample.Throughput, sample.Latency99ptile, averageOps)

	return sample, nil

}
