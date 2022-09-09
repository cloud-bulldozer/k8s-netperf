package netperf

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"

	"gopkg.in/yaml.v2"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

type Config struct {
	Duration    int    `yaml:"duration,omitempty"`
	Profile     string `yaml:"profile,omitempty"`
	Samples     int    `yaml:"samples,omitempty"`
	MessageSize int    `yaml:"messagesize,omitempty"`
	Service     bool   `default:"false" yaml:"service,omitempty"`
}

type DeploymentParams struct {
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

type PerfScenarios struct {
	NodeLocal    bool
	HostNetwork  bool
	Configs      []Config
	Client       apiv1.PodList
	Server       apiv1.PodList
	ClientAcross apiv1.PodList
	Service      *apiv1.Service
	RestConfig   rest.Config
}

type ServiceParams struct {
	Name      string
	Namespace string
	Labels    map[string]string
	CtlPort   int32
	DataPort  int32
}

const ServerCtlPort = 12865
const ServerDataPort = 42424

func BuildSUT(client *kubernetes.Clientset, s *PerfScenarios) error {
	// Check if nodes have the zone label to keep the netperf test
	// in the same AZ/Zone versus across AZ/Zone
	z, err := GetZone(client)
	if err != nil {
		fmt.Println(err)
	}
	// Get node count
	nodes, err := client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker="})
	if err != nil {
		return err
	}
	ncount := len(nodes.Items)

	if s.NodeLocal {
		//  Create Netperf client on the same node as the server.
		cdp := DeploymentParams{
			Name:      "client",
			Namespace: "netperf",
			Replicas:  1,
			Image:     "quay.io/jtaleric/k8snetperf:latest",
			Labels:    map[string]string{"role": "client"},
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
		_, err = CreateDeployment(cdp, client)
		if err != nil {
			return fmt.Errorf("Unable to create Client deployment")
		}
		// Wait for pod(s) in the deployment to be ready
		_, err = WaitForReady(client, cdp)
		if err != nil {
			return err
		}
		// Retrieve the pod information
		s.Client, err = GetPods(client, cdp)
		if err != nil {
			return err
		}
	}

	// Create netperf TCP service
	spTcp := ServiceParams{
		Name:      "netperf-service",
		Namespace: "netperf",
		Labels:    map[string]string{"role": "server"},
		CtlPort:   ServerCtlPort,
		DataPort:  ServerDataPort,
	}
	s.Service, err = CreateService(spTcp, client)
	if err != nil {
		return fmt.Errorf("😥 Unable to create TCP netperf service")
	}
	cdp_across := DeploymentParams{
		Name:      "client-across",
		Namespace: "netperf",
		Replicas:  1,
		Image:     "quay.io/jtaleric/k8snetperf:latest",
		Labels:    map[string]string{"role": "client-across"},
		Command:   []string{"/bin/bash", "-c", "sleep 10000000"},
		Port:      ServerCtlPort,
	}
	if z != "" {
		cdp_across.NodeAffinity = apiv1.NodeAffinity{
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
		_, err = CreateDeployment(cdp_across, client)
		if err != nil {
			return fmt.Errorf("Unable to create Client deployment")
		}
		// Wait for pod(s) in the deployment to be ready
		_, err = WaitForReady(client, cdp_across)
		if err != nil {
			return err
		}
		// Retrieve the pod information
		s.ClientAcross, err = GetPods(client, cdp_across)
		if err != nil {
			return err
		}
	}

	// Start netperf server
	sdp := DeploymentParams{
		Name:      "server",
		Namespace: "netperf",
		Replicas:  1,
		Image:     "quay.io/jtaleric/k8snetperf:latest",
		Labels:    map[string]string{"role": "server"},
		Command:   []string{"/bin/bash", "-c", "netserver; sleep 10000000"},
		PodAffinity: apiv1.PodAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []apiv1.WeightedPodAffinityTerm{
				{
					Weight: 100,
					PodAffinityTerm: apiv1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "role", Operator: metav1.LabelSelectorOpIn, Values: []string{"client"}},
							},
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		},
		Port: ServerCtlPort,
	}
	if z != "" {
		sdp.NodeAffinity = apiv1.NodeAffinity{
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
		sdp.PodAntiAffinity = apiv1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []apiv1.WeightedPodAffinityTerm{
				{
					Weight: 100,
					PodAffinityTerm: apiv1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "role", Operator: metav1.LabelSelectorOpIn, Values: []string{"client-across"}},
							},
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		}

	}
	_, err = CreateDeployment(sdp, client)
	if err != nil {
		return fmt.Errorf("😥 Unable to create Server deployment")
	}
	_, err = WaitForReady(client, sdp)
	if err != nil {
		return err
	}
	// Retrieve pods which match the server/client role labels
	s.Server, err = GetPods(client, sdp)
	if err != nil {
		return err
	}

	return nil
}

// ParseConfig will read in the netperf configuration file which
// describes which tests to run
// Returns Config struct
func ParseConf(fn string) ([]Config, error) {
	fmt.Printf("📒 Reading %s file.\r\n", fn)
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
		tests = append(tests, value)
	}
	return tests, nil
}

// Display the netperf config
func ShowConfig(c Config) {
	fmt.Printf("🗒️  Running netperf %s (service %t) for %ds\r\n", c.Profile, c.Service, c.Duration)
}

// RunNetPerf will use the k8s client to run the netperf binary in the container image
// it will return a bytes.Buffer of the stdout.
func Run(c *kubernetes.Clientset, rc rest.Config, nc Config, client apiv1.PodList, serverIP string) (bytes.Buffer, error) {
	var stdout, stderr bytes.Buffer
	pod := client.Items[0]
	fmt.Printf("🔥 Client (%s,%s) starting netperf against server : %s\n", pod.Name, pod.Status.PodIP, serverIP)
	ShowConfig(nc)
	cmd := []string{"/usr/local/bin/netperf", "-H", serverIP, "-l", fmt.Sprintf("%d", nc.Duration), "-t", nc.Profile, "--", "-R", "1", "-m", fmt.Sprintf("%d", nc.MessageSize), "-P", fmt.Sprintf("0,%d", ServerDataPort)}
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
	// Sound check stderr
	return stdout, nil
}
