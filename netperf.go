package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

type NetPerfResult struct {
	NetPerfConfig
	Metric   string
	SameNode bool
	Sample   float64
	Summary  []float64
}

type ScenarioResults struct {
	results []NetPerfResult
}

type NetPerfConfig struct {
	Duration    int    `yaml:"duration,omitempty"`
	Profile     string `yaml:"profile,omitempty"`
	Samples     int    `yaml:"samples,omitempty"`
	MessageSize int    `yaml:"messagesize,omitempty"`
}

type PerfScenarios struct {
	configs      []NetPerfConfig
	client       *apiv1.PodList
	server       *apiv1.PodList
	clientAcross *apiv1.PodList
	restconfig   rest.Config
}

type DeploymentParams struct {
	name            string
	namespace       string
	replicas        int32
	image           string
	labels          map[string]string
	command         []string
	podaffinity     apiv1.PodAffinity
	podantiaffinity apiv1.PodAntiAffinity
	nodeaffinity    apiv1.NodeAffinity
	port            int
}

func parseConf(fn string) ([]NetPerfConfig, error) {
	fmt.Printf("📒 Reading %s file.\r\n", fn)
	buf, err := ioutil.ReadFile(fn)
	if err != nil {
		return nil, err
	}
	c := make(map[string]NetPerfConfig)
	err = yaml.Unmarshal(buf, &c)
	if err != nil {
		return nil, fmt.Errorf("In file %q: %v", fn, err)
	}
	// Ignore the key
	// Pull out the specific tests
	var tests []NetPerfConfig
	for _, value := range c {
		tests = append(tests, value)
	}
	return tests, nil
}

func main() {
	cfgfile := flag.String("config", "netperf.yml", "K8s netperf Configuration File")
	flag.Parse()
	cfg, err := parseConf(*cfgfile)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}

	// Read in k8s config
	kconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{})
	rconfig, err := kconfig.ClientConfig()
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	client, err := kubernetes.NewForConfig(rconfig)
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	s := PerfScenarios{
		restconfig: *rconfig,
		configs:    cfg,
	}

	// Check if nodes have the zone label to keep the netperf test
	// in the same AZ/Zone versus across AZ/Zone
	z, err := GetZone(client)
	if err != nil {
		log.Warn(err)
	}

	// Get node count
	nodes, err := client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker="})
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
	ncount := len(nodes.Items)

	//  Create Netperf client on the same node as the server.
	cdp := DeploymentParams{
		name:      "client",
		namespace: "netperf",
		replicas:  1,
		image:     "quay.io/jtaleric/k8snetperf:latest",
		labels:    map[string]string{"role": "client"},
		command:   []string{"/bin/bash", "-c", "sleep 10000000"},
		port:      12865,
	}
	if z != "" {
		cdp.nodeaffinity = apiv1.NodeAffinity{
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
		log.Error("Unable to create Client deployment")
		os.Exit(1)
	}
	// Wait for pod(s) in the deployment to be ready
	_, err = WaitForReady(client, cdp)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}
	// Retrieve the pod information
	s.client, err = GetPods(client, cdp)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}

	// Start netperf server
	sdp := DeploymentParams{
		name:      "server",
		namespace: "netperf",
		replicas:  1,
		image:     "quay.io/jtaleric/k8snetperf:latest",
		labels:    map[string]string{"role": "server"},
		command:   []string{"/bin/bash", "-c", "netserver; sleep 10000000"},
		podaffinity: apiv1.PodAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []apiv1.PodAffinityTerm{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{Key: "role", Operator: metav1.LabelSelectorOpIn, Values: []string{"client"}},
						},
					},
					TopologyKey: "kubernetes.io/hostname",
				},
			},
		},
		port: 12865,
	}
	if z != "" {
		sdp.nodeaffinity = apiv1.NodeAffinity{
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
		sdp.podantiaffinity = apiv1.PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []apiv1.PodAffinityTerm{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{Key: "role", Operator: metav1.LabelSelectorOpIn, Values: []string{"client-across"}},
						},
					},
					TopologyKey: "kubernetes.io/hostname",
				},
			},
		}

	}
	_, err = CreateDeployment(sdp, client)
	if err != nil {
		fmt.Println("😥 Unable to create Server deployment")
		log.Error(err)
		os.Exit(1)
	}
	_, err = WaitForReady(client, sdp)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}

	// Retrieve pods which match the server/client role labels
	s.server, err = GetPods(client, sdp)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}
	cdp_across := DeploymentParams{
		name:      "client-across",
		namespace: "netperf",
		replicas:  1,
		image:     "quay.io/jtaleric/k8snetperf:latest",
		labels:    map[string]string{"role": "client-across"},
		command:   []string{"/bin/bash", "-c", "sleep 10000000"},
		port:      12865,
	}
	if z != "" {
		cdp_across.nodeaffinity = apiv1.NodeAffinity{
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
			log.Error("Unable to create Client deployment")
			os.Exit(1)
		}
		// Wait for pod(s) in the deployment to be ready
		_, err = WaitForReady(client, cdp_across)
		if err != nil {
			log.Error(err)
			os.Exit(1)
		}
		// Retrieve the pod information
		s.clientAcross, err = GetPods(client, cdp_across)
		if err != nil {
			log.Error(err)
			os.Exit(1)
		}
	}

	var sr ScenarioResults

	// Run through each test
	for _, nc := range s.configs {
		// Determine the metric for the test
		metric := string("OP/s")
		if strings.Contains(nc.Profile, "STREAM") {
			metric = "Mb/s"
		}
		if s.clientAcross != nil {
			npr := NetPerfResult{}
			npr.NetPerfConfig = nc
			npr.Metric = metric
			npr.SameNode = false
			for i := 0; i < nc.Samples; i++ {
				r, err := RunNetPerf(client, s.restconfig, nc, s.clientAcross, s.server)
				if err != nil {
					log.Error(err)
					os.Exit(1)
				}
				nr, err := ParseResults(&r, nc)
				if err != nil {
					log.Error(err)
					os.Exit(1)
				}
				npr.Summary = append(npr.Summary, nr)
			}
			sr.results = append(sr.results, npr)
		}
		// Reset the result as we are now testing a different scenario
		// Consider breaking the result per-scenario-config
		npr := NetPerfResult{}
		npr.NetPerfConfig = nc
		npr.Metric = metric
		npr.SameNode = true
		for i := 0; i < nc.Samples; i++ {
			r, err := RunNetPerf(client, s.restconfig, nc, s.client, s.server)
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
			nr, err := ParseResults(&r, nc)
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
			npr.Summary = append(npr.Summary, nr)
		}
		sr.results = append(sr.results, npr)
	}
	ShowResult(sr)
}

// Display the netperf config
func ShowConfig(c NetPerfConfig) {
	fmt.Printf("🗒️  Running netperf %s for %ds\r\n", c.Profile, c.Duration)
}

// RunNetPerf will use the k8s client to run the netperf binary in the container image
// it will return a bytes.Buffer of the stdout.
func RunNetPerf(c *kubernetes.Clientset, rc rest.Config, nc NetPerfConfig, client *apiv1.PodList, server *apiv1.PodList) (bytes.Buffer, error) {
	var stdout, stderr bytes.Buffer
	sip := server.Items[0].Status.PodIP
	pod := client.Items[0]
	fmt.Printf("🔥 Client (%s,%s) starting netperf against server : %s\n", pod.Name, pod.Status.PodIP, sip)
	ShowConfig(nc)
	cmd := []string{"/usr/local/bin/netperf", "-H", sip, "-l", fmt.Sprintf("%d", nc.Duration), "-t", nc.Profile, "--", "-R", "1", "-m", fmt.Sprintf("%d", nc.MessageSize)}
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
