package iperf

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"encoding/json"

	apiv1 "k8s.io/api/core/v1"

	"github.com/jtaleric/k8s-netperf/pkg/config"
	log "github.com/jtaleric/k8s-netperf/pkg/logging"
	"github.com/jtaleric/k8s-netperf/pkg/sample"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

type Result struct {
	Data struct {
		TCPStream struct {
			Rate float32 `json:"bits_per_second"`
		} `json:"sum_received"`
		UDPStream struct {
			Rate        float32 `json:"bits_per_second"`
			LossPercent float32 `json:"lost_percent"`
		} `json:"sum"`
	} `json:"end"`
}

const workload = "iperf3"

// ServerDataPort data port for the service
const ServerDataPort = 43433

// ServerCtlPort control port for the service
const ServerCtlPort = 22865

// TestSupported Determine if the test is supproted for driver
func TestSupported(test string) bool {
	return strings.Contains(test, "STREAM")
}

// Run will invoke iperf3 in a client container
func Run(c *kubernetes.Clientset, rc rest.Config, nc config.Config, client apiv1.PodList, serverIP string) (bytes.Buffer, error) {
	var stdout, stderr bytes.Buffer
	pod := client.Items[0]
	log.Debugf("🔥 Client (%s,%s) starting iperf3 against server : %s\n", pod.Name, pod.Status.PodIP, serverIP)
	config.Show(nc, workload)
	tcp := true
	if !strings.Contains(nc.Profile, "STREAM") {
		return bytes.Buffer{}, fmt.Errorf(" Unable to run iperf3 with non-stream tests ")
	}
	if strings.Contains(nc.Profile, "UDP") {
		tcp = false
	}
	var cmd []string
	if nc.Service {
		if tcp {
			cmd = []string{"iperf3", "-P", "1", "-c",
				serverIP, "-J", "-t",
				fmt.Sprintf("%d", nc.Duration),
				"-l", fmt.Sprintf("%d", nc.MessageSize),
				"-p", fmt.Sprintf("%d", ServerCtlPort),
			}
		} else {
			cmd = []string{"iperf3", "-P", "1", "-c",
				serverIP, "-t",
				fmt.Sprintf("%d", nc.Duration), "-u", "-J",
				"-l", fmt.Sprintf("%d", nc.MessageSize),
				"-p", fmt.Sprintf("%d", ServerCtlPort),
				"-b", "0",
			}
		}
	} else {
		if tcp {
			cmd = []string{"iperf3", "-J", "-P", strconv.Itoa(nc.Parallelism), "-c",
				serverIP, "-t",
				fmt.Sprintf("%d", nc.Duration),
				"-l", fmt.Sprintf("%d", nc.MessageSize),
				"-p", fmt.Sprintf("%d", ServerCtlPort),
			}
		} else {
			cmd = []string{"iperf3", "-J", "-P", strconv.Itoa(nc.Parallelism), "-c",
				serverIP, "-t",
				fmt.Sprintf("%d", nc.Duration), "-u",
				"-l", fmt.Sprintf("%d", nc.MessageSize),
				"-p", fmt.Sprintf("%d", ServerCtlPort),
				"-b", "0",
			}
		}
	}
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
// The NetConfig to determine aspects of the workload the user provided.
// It will return a Sample struct or error
func ParseResults(stdout *bytes.Buffer) (sample.Sample, error) {
	sample := sample.Sample{}
	sample.Driver = workload
	result := Result{}
	sample.Metric = "Mb/s"
	error := json.NewDecoder(stdout).Decode(&result)
	if error != nil {
		log.Error(" Issue while decoding ")
	}
	if result.Data.TCPStream.Rate > 0 {
		sample.Throughput = float64(result.Data.TCPStream.Rate) / 1000000

	} else {
		sample.Throughput = float64(result.Data.UDPStream.Rate) / 1000000
	}
	log.Debugf("Storing %s sample throughput:  %f", sample.Driver, sample.Throughput)

	return sample, nil
}
