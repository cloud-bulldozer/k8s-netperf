package netperf

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"regexp"
	"strconv"
	"strings"

	log "gihub.com/jtaleric/k8s-netperf/logging"
	"gopkg.in/yaml.v2"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// Config describes the netperf tests
type Config struct {
	Duration    int    `yaml:"duration,omitempty"`
	Profile     string `yaml:"profile,omitempty"`
	Samples     int    `yaml:"samples,omitempty"`
	MessageSize int    `yaml:"messagesize,omitempty"`
	Service     bool   `default:"false" yaml:"service,omitempty"`
}

// Sample describes the values we will return with each execution.
type Sample struct {
	Latency        float64
	Latency99ptile float64
	Throughput     float64
	Metric         string
}

// DeploymentParams describes the deployment
type DeploymentParams struct {
	HostNetwork     bool
	Name            string
	Namespace       string
	Replicas        int32
	Image           string
	Labels          map[string]string
	Command         []string
	PodAffinity     apiv1.PodAffinity
	PodAntiAffinity apiv1.PodAntiAffinity
	NodeAffinity    apiv1.NodeAffinity
	Port            int
}

// PerfScenarios describes the different scenarios
type PerfScenarios struct {
	NodeLocal    bool
	HostNetwork  bool
	Configs      []Config
	Client       apiv1.PodList
	Server       apiv1.PodList
	ClientAcross apiv1.PodList
	ClientHost   apiv1.PodList
	ServerHost   apiv1.PodList
	Service      *apiv1.Service
	RestConfig   rest.Config
}

// ServiceParams describes the service specific details
type ServiceParams struct {
	Name      string
	Namespace string
	Labels    map[string]string
	CtlPort   int32
	DataPort  int32
}

// ServerCtlPort control port for the service
const ServerCtlPort = 12865

// ServerDataPort data port for the service
const ServerDataPort = 42424

// Labels we will apply to k8s assets.
const serverRole = "server"
const clientRole = "client"
const clientAcrossRole = "client-across"
const hostNetServerRole = "host-server"
const hostNetClientRole = "host-client"

// Tests we will support in k8s-netperf
const validTests = "tcp_stream|udp_stream|tcp_rr|udp_rr|tcp_crr"

// omniOptions are netperf specific options that we will pass to the netperf client.
const omniOptions = "rt_latency,p99_latency,throughput,throughput_units"

// BuildSUT Build the k8s env to run network performance tests
func BuildSUT(client *kubernetes.Clientset, s *PerfScenarios) error {
	// Check if nodes have the zone label to keep the netperf test
	// in the same AZ/Zone versus across AZ/Zone
	z, err := GetZone(client)
	if err != nil {
		log.Warn(err)
	}
	// Get node count
	nodes, err := client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker="})
	if err != nil {
		return err
	}
	ncount := len(nodes.Items)
	log.Debugf("Number of nodes with role worker: %d", ncount)

	if s.NodeLocal {
		//  Create Netperf client on the same node as the server.
		cdp := DeploymentParams{
			Name:      "client",
			Namespace: "netperf",
			Replicas:  1,
			Image:     "quay.io/jtaleric/k8snetperf:latest",
			Labels:    map[string]string{"role": clientRole},
			Command:   []string{"/bin/bash", "-c", "sleep 10000000"},
			Port:      ServerCtlPort,
		}
		if z != "" {
			cdp.NodeAffinity = apiv1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: []apiv1.PreferredSchedulingTerm{
					{
						Weight: 100,
						Preference: apiv1.NodeSelectorTerm{
							MatchExpressions: []apiv1.NodeSelectorRequirement{
								{Key: "topology.kubernetes.io/zone", Operator: apiv1.NodeSelectorOpIn, Values: []string{z}},
							},
						},
					},
				},
			}
		}
		s.Client, err = deployDeployment(client, cdp)
		if err != nil {
			return err
		}

	}

	// Create netperf TCP service
	spTCP := ServiceParams{
		Name:      "netperf-service",
		Namespace: "netperf",
		Labels:    map[string]string{"role": serverRole},
		CtlPort:   ServerCtlPort,
		DataPort:  ServerDataPort,
	}
	s.Service, err = CreateService(spTCP, client)
	if err != nil {
		return fmt.Errorf("😥 Unable to create TCP netperf service")
	}
	cdpAcross := DeploymentParams{
		Name:      "client-across",
		Namespace: "netperf",
		Replicas:  1,
		Image:     "quay.io/jtaleric/k8snetperf:latest",
		Labels:    map[string]string{"role": clientAcrossRole},
		Command:   []string{"/bin/bash", "-c", "sleep 10000000"},
		Port:      ServerCtlPort,
	}
	cdpHostAcross := DeploymentParams{
		Name:        "client-host",
		Namespace:   "netperf",
		Replicas:    1,
		HostNetwork: true,
		Image:       "quay.io/jtaleric/k8snetperf:latest",
		Labels:      map[string]string{"role": hostNetClientRole},
		Command:     []string{"/bin/bash", "-c", "sleep 10000000"},
		Port:        ServerCtlPort,
	}
	if z != "" {
		cdpAcross.NodeAffinity = apiv1.NodeAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []apiv1.PreferredSchedulingTerm{
				{
					Weight: 100,
					Preference: apiv1.NodeSelectorTerm{
						MatchExpressions: []apiv1.NodeSelectorRequirement{
							{Key: "topology.kubernetes.io/zone", Operator: apiv1.NodeSelectorOpIn, Values: []string{z}},
						},
					},
				},
			},
		}
	}
	if ncount > 1 {
		if s.HostNetwork {
			s.ClientHost, err = deployDeployment(client, cdpHostAcross)
		}
		if err != nil {
			return err
		}
		s.ClientAcross, err = deployDeployment(client, cdpAcross)
		if err != nil {
			return err
		}
	}
	sdpHost := DeploymentParams{
		Name:        "server-host",
		Namespace:   "netperf",
		Replicas:    1,
		HostNetwork: true,
		Image:       "quay.io/jtaleric/k8snetperf:latest",
		Labels:      map[string]string{"role": hostNetServerRole},
		Command:     []string{"/bin/bash", "-c", fmt.Sprintf("netserver; sleep 10000000")},
		Port:        ServerCtlPort,
	}
	// Start netperf server
	sdp := DeploymentParams{
		Name:      "server",
		Namespace: "netperf",
		Replicas:  1,
		Image:     "quay.io/jtaleric/k8snetperf:latest",
		Labels:    map[string]string{"role": serverRole},
		Command:   []string{"/bin/bash", "-c", fmt.Sprintf("netserver; sleep 10000000")},
		Port:      ServerCtlPort,
	}
	if z != "" {
		affinity := apiv1.NodeAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []apiv1.PreferredSchedulingTerm{
				{
					Weight: 100,
					Preference: apiv1.NodeSelectorTerm{
						MatchExpressions: []apiv1.NodeSelectorRequirement{
							{Key: "topology.kubernetes.io/zone", Operator: apiv1.NodeSelectorOpIn, Values: []string{z}},
						},
					},
				},
			},
		}
		sdp.NodeAffinity = affinity
		sdpHost.NodeAffinity = affinity
	}
	if ncount > 1 {
		antiAffinity := apiv1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []apiv1.WeightedPodAffinityTerm{
				{
					Weight: 100,
					PodAffinityTerm: apiv1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "role", Operator: metav1.LabelSelectorOpIn, Values: []string{clientAcrossRole}},
							},
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		}
		sdp.PodAntiAffinity = antiAffinity
		antiAffinity = apiv1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []apiv1.WeightedPodAffinityTerm{
				{
					Weight: 100,
					PodAffinityTerm: apiv1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "role", Operator: metav1.LabelSelectorOpIn, Values: []string{hostNetClientRole}},
							},
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		}
		sdpHost.PodAntiAffinity = antiAffinity
	}
	if s.HostNetwork {
		s.ServerHost, err = deployDeployment(client, sdpHost)
		if err != nil {
			return err
		}
	}
	s.Server, err = deployDeployment(client, sdp)
	if err != nil {
		return err
	}
	return nil
}

// deployDeployment Manages the creation and waits for the pods to become ready.
// returns a podList which is associated with the Deployment.
func deployDeployment(client *kubernetes.Clientset, dp DeploymentParams) (apiv1.PodList, error) {
	pods := apiv1.PodList{}
	_, err := CreateDeployment(dp, client)
	if err != nil {
		return pods, fmt.Errorf("😥 Unable to create deployment")
	}
	_, err = WaitForReady(client, dp)
	if err != nil {
		return pods, err
	}
	// Retrieve pods which match the server/client role labels
	pods, err = GetPods(client, dp)
	if err != nil {
		return pods, err
	}
	return pods, nil
}

// ParseConf will read in the netperf configuration file which
// describes which tests to run
// Returns Config struct
func ParseConf(fn string) ([]Config, error) {
	log.Infof("📒 Reading %s file.\n", fn)
	buf, err := ioutil.ReadFile(fn)
	if err != nil {
		return nil, err
	}
	c := make(map[string]Config)
	err = yaml.Unmarshal(buf, &c)
	if err != nil {
		return nil, fmt.Errorf("In file %q: %v", fn, err)
	}
	// Ignore the key
	// Pull out the specific tests
	var tests []Config
	for _, value := range c {
		p, _ := regexp.MatchString("(?i)"+validTests, value.Profile)
		if !p {
			return nil, fmt.Errorf("unknown netperf profile")
		}
		if value.Duration < 1 {
			return nil, fmt.Errorf("duration must be > 0")
		}
		if value.Samples < 1 {
			return nil, fmt.Errorf("samples must be > 0")
		}
		if value.MessageSize < 1 {
			return nil, fmt.Errorf("messagesize must be > 0")
		}
		tests = append(tests, value)
	}
	return tests, nil
}

// ShowConfig Display the netperf config
func ShowConfig(c Config) {
	log.Infof("🗒️  Running netperf %s (service %t) for %ds\n", c.Profile, c.Service, c.Duration)
}

// Run will use the k8s client to run the netperf binary in the container image
// it will return a bytes.Buffer of the stdout.
func Run(c *kubernetes.Clientset, rc rest.Config, nc Config, client apiv1.PodList, serverIP string) (bytes.Buffer, error) {
	var stdout, stderr bytes.Buffer
	pod := client.Items[0]
	log.Infof("🔥 Client (%s,%s) starting netperf against server : %s\n", pod.Name, pod.Status.PodIP, serverIP)
	ShowConfig(nc)
	cmd := []string{"/usr/local/bin/netperf", "-H",
		serverIP, "-l",
		fmt.Sprintf("%d", nc.Duration),
		"-t", nc.Profile,
		"--",
		"-k", fmt.Sprintf("%s", omniOptions),
		"-m", fmt.Sprintf("%d", nc.MessageSize),
		"-P", fmt.Sprintf("0,%d", ServerDataPort), "-R", "1"}
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
	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return stdout, err
	}
	log.Debug(strings.TrimSpace(stdout.String()))
	// Sound check stderr
	return stdout, nil
}

// ParseResults accepts the stdout from the execution of the benchmark. It also needs
// The NetPerfConfig to determine aspects of the workload the user provided.
// It will return a Sample struct or error
func ParseResults(stdout *bytes.Buffer, nc Config) (Sample, error) {
	sample := Sample{}
	for _, line := range strings.Split(stdout.String(), "\n") {
		l := strings.Split(line, "=")
		if len(l) < 2 {
			continue
		}
		if strings.Contains(l[0], "THROUGHPUT_UNITS") {
			sample.Metric = l[1]
		} else if strings.Contains(l[0], "THROUGHPUT") {
			sample.Throughput, _ = strconv.ParseFloat(strings.Trim(l[1], "\r"), 64)
		} else if strings.Contains(l[0], "P99_LATENCY") {
			sample.Latency99ptile, _ = strconv.ParseFloat(strings.Trim(l[1], "\r"), 64)
		} else if strings.Contains(l[0], "RT_LATENCY") {
			sample.Latency, _ = strconv.ParseFloat(strings.Trim(l[1], "\r"), 64)
		}
	}
	return sample, nil
}
