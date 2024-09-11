package drivers

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"encoding/json"

	apiv1 "k8s.io/api/core/v1"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/k8s"
	log "github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/sample"
	"github.com/google/uuid"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

var Iperf iperf3

func init() {
	Iperf = iperf3{
		driverName: "iperf",
	}
}

type IperfResult struct {
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

// IsTestSupported Determine if the test is supported for driver
func (i *iperf3) IsTestSupported(test string) bool {
	return strings.Contains(test, "STREAM")
}

// Run will invoke iperf3 in a client container
func (i *iperf3) Run(c *kubernetes.Clientset,
	rc rest.Config,
	nc config.Config,
	client apiv1.PodList,
	serverIP string, perf *config.PerfScenarios) (bytes.Buffer, error) {
	var stdout, stderr bytes.Buffer
	id := uuid.New()
	file := fmt.Sprintf("/tmp/iperf-%s", id.String())
	pod := client.Items[0]
	log.Debugf("🔥 Client (%s,%s) starting iperf3 against server: %s", pod.Name, pod.Status.PodIP, serverIP)
	config.Show(nc, i.driverName)
	tcp := true
	if !strings.Contains(nc.Profile, "STREAM") {
		return bytes.Buffer{}, fmt.Errorf("unable to run iperf3 with non-stream tests")
	}
	if strings.Contains(nc.Profile, "UDP") {
		tcp = false
	}
	var cmd []string
	if tcp {
		cmd = []string{"iperf3", "-J", "-P", strconv.Itoa(nc.Parallelism), "-c",
			serverIP, "-t",
			fmt.Sprint(nc.Duration),
			"-l", fmt.Sprint(nc.MessageSize),
			"-p", fmt.Sprint(k8s.IperfServerCtlPort),
			fmt.Sprintf("--logfile=%s", file),
		}
	} else {
		cmd = []string{"iperf3", "-J", "-P", strconv.Itoa(nc.Parallelism), "-c",
			serverIP, "-t",
			fmt.Sprint(nc.Duration), "-u",
			"-l", fmt.Sprint(nc.MessageSize),
			"-p", fmt.Sprint(k8s.IperfServerCtlPort),
			"-b", "0",
			fmt.Sprintf("--logfile=%s", file),
		}
	}
	log.Debug(cmd)
	if !perf.VM {
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
	} else {
		retry := 3
		present := false
		sshclient, err := k8s.SSHConnect(perf)
		if err != nil {
			return stdout, err
		}
		for i := 0; i <= retry; i++ {
			log.Debug("⏰ Waiting for iperf3 to be present on VM")
			_, err = sshclient.Run("until iperf3 -h; do sleep 30; done")
			if err == nil {
				present = true
				break
			}
			time.Sleep(10 * time.Second)
		}
		if !present {
			sshclient.Close()
			return stdout, fmt.Errorf("iperf3 binary is not present on the VM")
		}
		var stdout []byte
		ran := false
		for i := 0; i <= retry; i++ {
			stdout, err = sshclient.Run(strings.Join(cmd[:], " "))
			if err == nil {
				ran = true
				break
			}
			log.Debugf("Failed running command %s", err)
			log.Debugf("⏰ Retrying iperf3 command -- cloud-init still finishing up")
			time.Sleep(60 * time.Second)
		}
		sshclient.Close()
		if !ran {
			return *bytes.NewBuffer(stdout), fmt.Errorf("Unable to run iperf3")
		}
	}

	//Empty buffer
	stdout = bytes.Buffer{}
	stderr = bytes.Buffer{}

	if !perf.VM {
		req := c.CoreV1().RESTClient().
			Post().
			Namespace(pod.Namespace).
			Resource("pods").
			Name(pod.Name).
			SubResource("exec").
			VersionedParams(&apiv1.PodExecOptions{
				Container: pod.Spec.Containers[0].Name,
				Command:   []string{"cat", file},
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
	} else {
		sshclient, err := k8s.SSHConnect(perf)
		if err != nil {
			return stdout, err
		}
		stdout, err := sshclient.Run(fmt.Sprintf("cat %s", file))
		if err != nil {
			sshclient.Close()
			return *bytes.NewBuffer(stdout), err
		}
		log.Debug(strings.TrimSpace(bytes.NewBuffer(stdout).String()))
		sshclient.Close()
		return *bytes.NewBuffer(stdout), nil
	}
}

// ParseResults accepts the stdout from the execution of the benchmark.
// It will return a Sample struct or error
func (i *iperf3) ParseResults(stdout *bytes.Buffer, _ config.Config) (sample.Sample, error) {
	sample := sample.Sample{}
	sample.Driver = i.driverName
	result := IperfResult{}
	sample.Metric = "Mb/s"
	err := json.NewDecoder(stdout).Decode(&result)
	if err != nil {
		log.Errorf("Issue while decoding: %v", err)
	}
	if result.Data.TCPStream.Rate > 0 {
		sample.Throughput = float64(result.Data.TCPStream.Rate) / 1000000
		sample.Retransmits = result.Data.TCPRetransmit.Count

	} else {
		sample.Throughput = float64(result.Data.UDPStream.Rate) / 1000000
		sample.LossPercent = result.Data.UDPStream.LossPercent
	}

	log.Debugf("Storing %s sample throughput: %f", sample.Driver, sample.Throughput)

	return sample, nil
}
