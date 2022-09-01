package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

var NetReg = regexp.MustCompile(`\s+\d+\s+\d+\s+(\d+|\S+)\s+(\S+|\d+)\s+(\S+)+\s+(\S+)?`)

type NetPerfResults struct {
	duration    int
	messagesize int32
	profile     string
	metric      string
	values      float64
	setup       string
}

type ScenarioResults struct {
	results []NetPerfResults
}

type NetPerfConfig struct {
	duration    int
	profile     string
	samples     int
	messageSize int32
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

func main() {
	flag.Parse()
	kconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{})
	rconfig, err := kconfig.ClientConfig()
	if err != nil {
		panic(err)
	}
	client, err := kubernetes.NewForConfig(rconfig)
	if err != nil {
		panic(err)
	}
	s := PerfScenarios{
		restconfig: *rconfig,
		configs: []NetPerfConfig{
			{
				duration:    10,
				profile:     "TCP_STREAM",
				samples:     1,
				messageSize: 1024,
			},
			{
				duration:    10,
				profile:     "UDP_STREAM",
				samples:     1,
				messageSize: 1024,
			},
			{
				duration:    10,
				profile:     "TCP_CRR",
				samples:     1,
				messageSize: 1024,
			},
			{
				duration:    10,
				profile:     "TCP_RR",
				samples:     1,
				messageSize: 1024,
			},
		},
	}

	// Check if nodes have the zone label to keep the netperf test
	// in the same AZ/Zone versus across AZ/Zone
	z, err := GetZone(client)
	if err != nil {
		fmt.Println(err)
	}

	// Get node count
	nodes, err := client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker="})
	if err != nil {
		panic(err)
	}
	ncount := len(nodes.Items)

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
			fmt.Println("Unable to create Client deployment")
			panic(err)
		}
		// Wait for pod(s) in the deployment to be ready
		_, err = WaitForReady(client, cdp_across)
		if err != nil {
			panic(err)
		}
		// Retrieve the pod information
		s.clientAcross, err = GetPods(client, cdp_across)
		if err != nil {
			panic(err)
		}
	}
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
		fmt.Println("Unable to create Client deployment")
		panic(err)
	}
	// Wait for pod(s) in the deployment to be ready
	_, err = WaitForReady(client, cdp)
	if err != nil {
		panic(err)
	}
	// Retrieve the pod information
	s.client, err = GetPods(client, cdp)
	if err != nil {
		panic(err)
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
		panic(err)
	}
	_, err = WaitForReady(client, sdp)
	if err != nil {
		panic(err)
	}

	// Retrieve pods which match the server/client role labels
	s.server, err = GetPods(client, sdp)
	if err != nil {
		panic(err)
	}

	var sr ScenarioResults

	// Run through each test
	for _, nc := range s.configs {
		for i := 1; i <= nc.samples; i++ {
			// If we have the ability to run across node
			if s.clientAcross != nil {
				r, err := RunNetPerf(client, s.restconfig, nc, s.clientAcross, s.server)
				if err != nil {
					panic(err)
				}
				nr, err := ParseResults(&r, nc)
				if err != nil {
					panic(err)
				}
				nr.setup = "Across Node"
				sr.results = append(sr.results, nr)

			}
			r, err := RunNetPerf(client, s.restconfig, nc, s.client, s.server)
			if err != nil {
				panic(err)
			}
			nr, err := ParseResults(&r, nc)
			if err != nil {
				panic(err)
			}
			nr.setup = "Same Node"
			sr.results = append(sr.results, nr)
		}
	}
	ShowResult(sr)
}

// Display the netperf config
func ShowConfig(c NetPerfConfig) {
	fmt.Printf("🗒️  Running netperf %s for %d(sec)\r\n", c.profile, c.duration)
}

// ShowResults accepts NetPerfResults to display to the user via stdout
func ShowResult(s ScenarioResults) {
	fmt.Printf("%s\r\n", strings.Repeat("-", (18+25+55)))
	fmt.Printf("%-18s | %-15s | %-15s | %-15s | %-15s\r\n", "Scenario", "Message Size", "Setup", "Duration", "Value")
	fmt.Printf("%s\r\n", strings.Repeat("-", (18+25+55)))
	for _, r := range s.results {
		fmt.Printf("📊 %-15s | %-15d | %-15s | %-15d | %-15f (%s) \r\n", r.profile, r.messagesize, r.setup, r.duration, r.values, r.metric)
	}
	fmt.Printf("%s\r\n", strings.Repeat("-", (18+25+55)))
}

// ParseResults accepts the stdout from the execution of the benchmark. It also needs
// The NetPerfConfig to determine aspects of the workload the user provided.
// It will return a NetPerfResults struct or error
func ParseResults(stdout *bytes.Buffer, nc NetPerfConfig) (NetPerfResults, error) {
	metric := string("OP/s")
	if strings.Contains(nc.profile, "STREAM") {
		metric = "Mb/s"
	}
	r := NetPerfResults{
		metric: metric,
	}
	d := NetReg.FindStringSubmatch(stdout.String())
	if len(d) < 5 {
		return r, fmt.Errorf("❌ Unable to process results")
	}
	val := ""
	if len(d[len(d)-1]) > 0 {
		val = d[len(d)-1]
	} else {
		val = d[len(d)-2]
	}
	v, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return r, fmt.Errorf("❌ Unable to parse netperf result")
	}
	r.values = v
	r.duration = nc.duration
	r.profile = nc.profile
	r.messagesize = nc.messageSize
	return r, nil
}

// RunNetPerf will use the k8s client to run the netperf binary in the container image
// it will return a bytes.Buffer of the stdout.
func RunNetPerf(c *kubernetes.Clientset, rc rest.Config, nc NetPerfConfig, client *apiv1.PodList, server *apiv1.PodList) (bytes.Buffer, error) {
	var stdout, stderr bytes.Buffer
	sip := server.Items[0].Status.PodIP
	pod := client.Items[0]
	fmt.Printf("🔥 Client (%s,%s) starting netperf against server : %s\n", pod.Name, pod.Status.PodIP, sip)
	ShowConfig(nc)
	cmd := []string{"/usr/local/bin/netperf", "-H", sip, "-l", fmt.Sprintf("%d", nc.duration), "-t", nc.profile, "--", "-R", "1", "-m", fmt.Sprintf("%d", nc.messageSize)}
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

// GetPods searches for a specific set of pods from DeploymentParms
// It returns a PodList if the deployment is found.
// NOTE : Since we can update the replicas to be > 1, is why I return a PodList.
func GetPods(c *kubernetes.Clientset, dp DeploymentParams) (*apiv1.PodList, error) {
	d, err := c.AppsV1().Deployments(dp.namespace).Get(context.TODO(), dp.name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("❌ Failure to capture deployment")
	}
	selector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("❌ Failure to capture deployment label")
	}
	pods, err := c.CoreV1().Pods(dp.namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, fmt.Errorf("❌ Failure to capture pods")
	}
	return pods, nil
}

// WaitForReady accepts the client and deployment params to determine which pods to watch.
// It will return a bool based on if the pods ever become ready before we move on.
func WaitForReady(c *kubernetes.Clientset, dp DeploymentParams) (bool, error) {
	d, err := c.AppsV1().Deployments(dp.namespace).Get(context.TODO(), dp.name, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("❌ Failure to capture deployment information")
	}
	selector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	fmt.Println("⏰ Waiting for Pods to become ready...")
	w, err := c.CoreV1().Pods(dp.namespace).Watch(context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		panic(err)
	}
	defer w.Stop()
	for event := range w.ResultChan() {
		p, ok := event.Object.(*apiv1.Pod)
		if !ok {
			fmt.Println("❌ Issue with the pod event")
		}
		if p.Status.Phase == "Running" {
			return true, nil
		}
	}
	return false, fmt.Errorf("❌ Deployment had issues launching pods")
}

// GetZone will determine if we have a multiAZ/Zone cloud.
func GetZone(c *kubernetes.Clientset) (string, error) {
	zones := map[string]int{}
	zone := ""
	lz := ""
	n, err := c.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("Unable to query nodes")
	}
	for _, l := range n.Items {
		if len(l.GetLabels()["topology.kubernetes.io/zone"]) < 1 {
			return "", fmt.Errorf("⚠️ No zone label")
		}
		if _, ok := zones[l.GetLabels()["topology.kubernetes.io/zone"]]; ok {
			zone = l.GetLabels()["topology.kubernetes.io/zone"]
			break
		} else {
			zones[l.GetLabels()["topology.kubernetes.io/zone"]] = 1
			lz = l.GetLabels()["topology.kubernetes.io/zone"]
		}
	}
	// No zone had > 1, use the last zone.
	if zone == "" {
		fmt.Println("⚠️ Single node per zone")
		zone = lz
	}
	return zone, nil
}

func CreateDeployment(dp DeploymentParams, client *kubernetes.Clientset) (*appsv1.Deployment, error) {
	fmt.Printf("🚀 Starting Deployment for %s in %s\n", dp.name, dp.namespace)
	d, err := client.AppsV1().Deployments(dp.namespace).Get(context.TODO(), dp.name, metav1.GetOptions{})
	if err == nil {
		fmt.Println("♻️  Using existing Deployment")
		return d, nil
	}
	dc := client.AppsV1().Deployments(dp.namespace)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: dp.name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &dp.replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: dp.labels,
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: dp.labels,
				},
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{
							Name:    dp.name,
							Image:   dp.image,
							Command: dp.command,
						},
					},
					Affinity: &apiv1.Affinity{
						NodeAffinity:    &dp.nodeaffinity,
						PodAffinity:     &dp.podaffinity,
						PodAntiAffinity: &dp.podantiaffinity,
					},
				},
			},
		},
	}
	return dc.Create(context.TODO(), deployment, metav1.CreateOptions{})
}
