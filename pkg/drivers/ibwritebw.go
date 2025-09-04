package drivers

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

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

var IbWriteBw ibWriteBw

func init() {
	IbWriteBw = ibWriteBw{
		driverName: "ib_write_bw",
	}
}

type ibWriteBw struct {
	driverName string
	testConfig config.Config
}

// IsTestSupported determines if the test is supported for ib_write_bw driver
func (i *ibWriteBw) IsTestSupported() bool {
	// ib_write_bw only supports bandwidth tests (STREAM profiles)
	return strings.Contains(i.testConfig.Profile, "STREAM")
}

// Run will invoke ib_write_bw in a client container
func (i *ibWriteBw) Run(c *kubernetes.Clientset,
	rc rest.Config,
	nc config.Config,
	client apiv1.PodList,
	serverIP string, perf *config.PerfScenarios) (bytes.Buffer, error) {
	var stdout, stderr bytes.Buffer
	pod := client.Items[0]
	log.Debugf("Client (%s,%s) starting ib_write_bw against server: %s", pod.Name, pod.Status.PodIP, serverIP)
	config.Show(nc, i.driverName)

	// ib_write_bw client command: "ib_write_bw -d mlx5_0 -x 3 -F --report_gbits $server_ip"
	cmd := []string{"ib_write_bw", "-d", "mlx5_0", "-x", "3", "-F", "--report_gbits", serverIP}
	
	// Add duration if specified (ib_write_bw uses -D for duration in seconds)
	if nc.Duration > 0 {
		cmd = append(cmd, "-D", fmt.Sprint(nc.Duration))
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
			log.Debug("⏰ Waiting for ib_write_bw to be present on VM")
			_, err = sshclient.Run("until ib_write_bw -h; do sleep 30; done")
			if err == nil {
				present = true
				break
			}
			time.Sleep(10 * time.Second)
		}
		if !present {
			sshclient.Close()
			return stdout, fmt.Errorf("ib_write_bw binary is not present on the VM")
		}
		var stdoutBytes []byte
		ran := false
		for i := 0; i <= retry; i++ {
			stdoutBytes, err = sshclient.Run(strings.Join(cmd[:], " "))
			if err == nil {
				ran = true
				break
			}
			log.Debugf("Failed running command %s", err)
			log.Debugf("⏰ Retrying ib_write_bw command -- cloud-init still finishing up")
			time.Sleep(60 * time.Second)
		}
		sshclient.Close()
		if !ran {
			return *bytes.NewBuffer(stdoutBytes), fmt.Errorf("Unable to run ib_write_bw")
		}
		stdout = *bytes.NewBuffer(stdoutBytes)
	}

	log.Debug(strings.TrimSpace(stdout.String()))
	return stdout, nil
}

// ParseResults accepts the stdout from the execution of ib_write_bw benchmark.
// It will return a Sample struct or error
// TODO: Implement proper parsing of ib_write_bw output
func (i *ibWriteBw) ParseResults(stdout *bytes.Buffer, _ config.Config) (sample.Sample, error) {
	sample := sample.Sample{}
	sample.Driver = i.driverName
	sample.Metric = "Gb/s"
	
	// Mock implementation - returns a placeholder value
	// TODO: Parse actual ib_write_bw output format
	sample.Throughput = 0.0
	
	log.Debugf("Mock parsing for %s - actual implementation needed", sample.Driver)
	log.Debugf("Raw output to parse: %s", stdout.String())
	
	return sample, nil
}