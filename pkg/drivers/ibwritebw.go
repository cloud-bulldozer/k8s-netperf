package drivers

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	apiv1 "k8s.io/api/core/v1"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
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
	// ib_write_bw only supports UDP_STREAM profile
	return strings.ToUpper(i.testConfig.Profile) == "UDP_STREAM"
}

// parseNicGid parses the nic:gid parameter and returns the device and GID index
func parseNicGid(nicGidParam string) (string, string, error) {
	// Parameter is now mandatory
	if strings.TrimSpace(nicGidParam) == "" {
		return "", "", fmt.Errorf("ib-write-bw requires nic:gid parameter (e.g., mlx5_0:0)")
	}

	parts := strings.Split(nicGidParam, ":")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid nic:gid format '%s', expected format: 'device:gid_index'", nicGidParam)
	}

	device := strings.TrimSpace(parts[0])
	gid := strings.TrimSpace(parts[1])

	if device == "" || gid == "" {
		return "", "", fmt.Errorf("nic and gid cannot be empty in '%s'", nicGidParam)
	}

	return device, gid, nil
}

// Run will invoke ib_write_bw in a client container
func (i *ibWriteBw) Run(c *kubernetes.Clientset,
	rc rest.Config,
	nc config.Config,
	client apiv1.PodList,
	serverIP string, perf *config.PerfScenarios, virt bool) (bytes.Buffer, error) {
	var stdout, stderr bytes.Buffer
	pod := client.Items[0]
	clientIp := pod.Status.PodIP
	log.Debugf("Client (%s,%s) starting ib_write_bw against server: %s", pod.Name, clientIp, serverIP)
	config.Show(nc, i.driverName)

	// Parse the nic:gid parameter
	device, gidIndex, err := parseNicGid(perf.IbWriteBwParams)
	if err != nil {
		return stdout, fmt.Errorf("failed to parse ib-write-bw parameter: %v", err)
	}

	// ib_write_bw client command: "ib_write_bw -d {device} -x {gid} -F $server_ip"
	cmd := []string{"stdbuf", "-oL", "-eL", "ib_write_bw", "-d", device, "-x", gidIndex, "-F", serverIP}

	// Add duration (ib_write_bw uses -D for duration in seconds)
	cmd = append(cmd, "-D", fmt.Sprint(nc.Duration))

	log.Debug(cmd)
	//
	if virt {
		log.Info("IB Write BW is not supported for VM mode... skipping")
	} else {
		//Pod mode
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
	}
	log.Debug(strings.TrimSpace(stdout.String()))
	return stdout, nil
}

// ParseResults accepts the stdout from the execution of ib_write_bw benchmark.
// It will return a Sample struct or error
func (i *ibWriteBw) ParseResults(stdout *bytes.Buffer, _ config.Config) (sample.Sample, error) {
	sample := sample.Sample{}
	sample.Driver = i.driverName
	sample.Metric = "MiB/s"

	output := stdout.String()
	lines := strings.Split(output, "\n")

	// Look for the results line that contains the bandwidth data
	// Format: " 65536      130030           0.00               2708.88              0.043342"
	// We want the 4th column which is "BW average[MiB/sec]"
	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Skip header lines and empty lines
		if strings.Contains(line, "#bytes") || strings.Contains(line, "---") || line == "" {
			continue
		}

		// Look for data lines with numeric values
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			// Check if first field is numeric (bytes)
			if _, err := fmt.Sscanf(fields[0], "%d", new(int)); err == nil {
				// Parse the BW average field (4th column)
				var bwAverage float64
				if _, err := fmt.Sscanf(fields[3], "%f", &bwAverage); err == nil {
					sample.Throughput = bwAverage
					log.Debugf("Parsed ib_write_bw BW average: %.2f MiB/s", bwAverage)
					return sample, nil
				}
			}
		}
	}

	log.Debugf("Failed to parse ib_write_bw output: %s", output)
	return sample, fmt.Errorf("failed to parse BW average from ib_write_bw output")
}
