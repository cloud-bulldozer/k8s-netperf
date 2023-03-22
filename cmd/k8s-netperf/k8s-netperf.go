package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jtaleric/k8s-netperf/pkg/config"
	"github.com/jtaleric/k8s-netperf/pkg/k8s"
	log "github.com/jtaleric/k8s-netperf/pkg/logging"
	"github.com/jtaleric/k8s-netperf/pkg/metrics"
	"github.com/jtaleric/k8s-netperf/pkg/netperf"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {

	cfgfile := flag.String("config", "netperf.yml", "K8s netperf Configuration File")
	nl := flag.Bool("local", false, "Run Netperf with pod/server on the same node")
	full := flag.Bool("all", false, "Run all tests scenarios - hostNet and podNetwork (if possible)")
	debug := flag.Bool("debug", false, "Enable debug log")
	promURL := flag.String("prom", "", "Prometheus URL")
	id := flag.String("uuid", "", "User provided UUID")
	searchURL := flag.String("search", "", "OpenSearch URL, if you have auth, pass in the format of https://user:pass@url:port")
	showMetrics := flag.Bool("metrics", false, "Show all system metrics retrieved from prom")
	tcpt := flag.Float64("tcp-tolerance", 10, "Allowed %diff from hostNetwork to podNetwork, anything above tolerance will result in k8s-netperf exiting 1.")
	flag.Parse()

	uid := ""
	if len(*id) > 0 {
		uid = *id
	} else {
		u := uuid.New()
		uid = fmt.Sprintf("%s", u.String())
	}

	if *debug {
		log.SetDebug()
	}

	cfg, err := config.ParseConf(*cfgfile)
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
		log.Error(err)
		os.Exit(1)
	}
	client, err := kubernetes.NewForConfig(rconfig)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}
	s := config.PerfScenarios{
		HostNetwork: *full,
		NodeLocal:   *nl,
		RestConfig:  *rconfig,
		Configs:     cfg,
	}
	// Get node count
	nodes, err := client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker="})
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}
	if !s.NodeLocal && len(nodes.Items) < 2 {
		log.Error("Node count too low to run pod to pod across nodes.")
		log.Error("To run k8s-netperf on a single node deployment pass -local.")
		log.Error("	$ k8s-netperf -local")
		os.Exit(1)
	}

	pavail := false
	pcon, found := metrics.Discover()
	if !found {
		// Assume we are not running against OpenShift
		if len(*promURL) > 1 {
			pcon.URL = *promURL
			pavail = metrics.PromCheck(pcon)
		}
	} else {
		// If the env isn't OpenShift assume the user is providing the path to prom
		if !pcon.OpenShift {
			pcon.URL = *promURL
			if len(*promURL) > 1 {
				pavail = metrics.PromCheck(pcon)
			}
		} else {
			if len(*promURL) > 1 {
				pcon.URL = *promURL
			}
			pavail = metrics.PromCheck(pcon)
		}
	}

	if !pavail {
		log.Warn("😥 Prometheus is not available")
	}

	// Build the SUT (Deployments)
	err = k8s.BuildSUT(client, &s)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}

	var sr netperf.ScenarioResults

	// Run through each test
	for _, nc := range s.Configs {
		// Determine the metric for the test
		metric := string("OP/s")
		if strings.Contains(nc.Profile, "STREAM") {
			metric = "Mb/s"
		}
		var serverIP string
		var service bool
		service = false
		if nc.Service {
			service = true
			serverIP = s.Service.Spec.ClusterIP
		} else {
			serverIP = s.Server.Items[0].Status.PodIP
		}
		if !s.NodeLocal {
			npr := netperf.Data{}
			npr.Config = nc
			npr.Metric = metric
			npr.Service = service
			npr.HostNetwork = true
			if !nc.Service && *full {
				npr.StartTime = time.Now()
				for i := 0; i < nc.Samples; i++ {
					r, err := netperf.Run(client, s.RestConfig, nc, s.ClientHost, s.ServerHost.Items[0].Status.PodIP)
					if err != nil {
						log.Error(err)
						log.Error("Note : Running netperf with hostNetwork will require some host configuration -- poking a hole in the firewall.")
						os.Exit(1)
					}
					nr, err := netperf.ParseResults(&r, nc)
					if err != nil {
						log.Error(err)
						os.Exit(1)
					}
					npr.ThroughputSummary = append(npr.ThroughputSummary, nr.Throughput)
					npr.LatencySummary = append(npr.LatencySummary, nr.Latency99ptile)
				}
				npr.EndTime = time.Now()
				npr.ClientNodeInfo = s.ClientNodeInfo
				npr.ServerNodeInfo = s.ServerNodeInfo
				sr.Results = append(sr.Results, npr)
			}
			npr = netperf.Data{}
			npr.Config = nc
			npr.Metric = metric
			npr.Service = service
			npr.SameNode = false
			npr.StartTime = time.Now()
			for i := 0; i < nc.Samples; i++ {
				r, err := netperf.Run(client, s.RestConfig, nc, s.ClientAcross, serverIP)
				if err != nil {
					log.Error(err)
					os.Exit(1)
				}
				nr, err := netperf.ParseResults(&r, nc)
				if err != nil {
					log.Error(err)
					os.Exit(1)
				}
				npr.ThroughputSummary = append(npr.ThroughputSummary, nr.Throughput)
				npr.LatencySummary = append(npr.LatencySummary, nr.Latency99ptile)
			}
			npr.EndTime = time.Now()
			npr.ClientNodeInfo = s.ClientNodeInfo
			npr.ServerNodeInfo = s.ServerNodeInfo
			sr.Results = append(sr.Results, npr)
		} else {
			// Reset the result as we are now testing a different scenario
			// Consider breaking the result per-scenario-config
			npr := netperf.Data{}
			npr.Config = nc
			npr.Metric = metric
			npr.Service = service
			npr.SameNode = true
			npr.StartTime = time.Now()
			for i := 0; i < nc.Samples; i++ {
				r, err := netperf.Run(client, s.RestConfig, nc, s.Client, serverIP)
				if err != nil {
					log.Error(err)
					os.Exit(1)
				}
				nr, err := netperf.ParseResults(&r, nc)
				if err != nil {
					log.Error(err)
					os.Exit(1)
				}
				npr.ThroughputSummary = append(npr.ThroughputSummary, nr.Throughput)
				npr.LatencySummary = append(npr.LatencySummary, nr.Latency99ptile)
			}
			npr.EndTime = time.Now()
			npr.ClientNodeInfo = s.ClientNodeInfo
			npr.ServerNodeInfo = s.ServerNodeInfo
			sr.Results = append(sr.Results, npr)
		}
	}

	var fTime time.Time
	var lTime time.Time
	if pavail {
		for i, npr := range sr.Results {
			sr.Results[i].ClientMetrics, _ = metrics.QueryNodeCPU(npr.ClientNodeInfo, pcon, npr.StartTime, npr.EndTime)
			sr.Results[i].ServerMetrics, _ = metrics.QueryNodeCPU(npr.ServerNodeInfo, pcon, npr.StartTime, npr.EndTime)
			sr.Results[i].ClientPodCPU, _ = metrics.TopPodCPU(npr.ClientNodeInfo, pcon, npr.StartTime, npr.EndTime)
			sr.Results[i].ServerPodCPU, _ = metrics.TopPodCPU(npr.ServerNodeInfo, pcon, npr.StartTime, npr.EndTime)
			fTime = npr.StartTime
			lTime = npr.EndTime
		}
	}

	if len(*searchURL) > 1 {
		// Metadata
		node := metrics.NodeDetails(pcon)
		sr.Metadata.Kernel = node.Metric.Kernel
		sr.Metadata.Kubelet = node.Metric.Kubelet
		sr.Metadata.OCPVersion = metrics.OCPversion(pcon, fTime, lTime)
		sr.Metadata.Platform = metrics.Platform(pcon)
		sec, err := metrics.IPSecEnabled(pcon, fTime, lTime)
		if err == nil {
			sr.Metadata.IPsec = sec
		}
		mtu, err := metrics.NodeMTU(pcon)
		if err == nil {
			sr.Metadata.MTU = mtu
		}

		jdocs, err := netperf.BuildDocs(sr, uid)
		if err != nil {
			log.Error(err)
			os.Exit(1)
		}
		esClient, err := netperf.Connect(*searchURL, true)
		if err != nil {
			log.Error(err)
			os.Exit(1)
		}
		err = netperf.IndexDocs(esClient, jdocs)
		if err != nil {
			log.Error(err)
			os.Exit(1)
		}
	}

	netperf.ShowStreamResult(sr)
	netperf.ShowRRResult(sr)
	netperf.ShowLatencyResult(sr)
	if *showMetrics {
		netperf.ShowNodeCPU(sr)
		netperf.ShowPodCPU(sr)
	}
	err = netperf.WriteCSVResult(sr)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}
	if pavail {
		err = netperf.WritePromCSVResult(sr)
		if err != nil {
			log.Error(err)
			os.Exit(1)
		}
	}
	// Initially we are just checking against TCP_STREAM results.
	if netperf.CheckHostResults(sr) {
		diff, err := netperf.TCPThroughputDiff(sr)
		if err != nil {
			fmt.Println("Unable to calculate difference between HostNetwork and PodNetwork")
			os.Exit(1)
		}
		if diff < *tcpt {
			os.Exit(0)
		}
		fmt.Printf("😥 TCP Stream percent difference when comparing hostNetwork to podNetwork is greater than %.1f percent (%.1f percent)\r\n", *tcpt, diff)
		os.Exit(1)
	}
}
