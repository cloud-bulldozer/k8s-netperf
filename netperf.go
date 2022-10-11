package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"gihub.com/jtaleric/k8s-netperf/netperf"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	cfgfile := flag.String("config", "netperf.yml", "K8s netperf Configuration File")
	nl := flag.Bool("local", false, "Run Netperf with pod/server on the same node")
	full := flag.Bool("all", false, "Run all tests scenarios - hostNet and podNetwork (if possible)")
	tcpt := flag.Float64("tcp-tolerance", 10, "Allowed %diff from hostNetwork to podNetwork, anything above tolerance will result in k8s-netperf exiting 1.")
	flag.Parse()
	cfg, err := netperf.ParseConf(*cfgfile)
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
	s := netperf.PerfScenarios{
		NodeLocal:  *nl,
		RestConfig: *rconfig,
		Configs:    cfg,
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
		log.Error("		$ k8s-netperf -local")
		os.Exit(1)
	}

	// Build the SUT (Deployments)
	err = netperf.BuildSUT(client, &s)
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
		if nc.Service {
			serverIP = s.Service.Spec.ClusterIP
		} else {
			serverIP = s.Server.Items[0].Status.PodIP
		}
		if !s.NodeLocal {
			npr := netperf.Data{}
			npr.Config = nc
			npr.Metric = metric
			npr.HostNetwork = true
			if !nc.Service && *full {
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
					npr.Summary = append(npr.Summary, nr)
				}
				sr.Results = append(sr.Results, npr)
			}
			npr = netperf.Data{}
			npr.Config = nc
			npr.Metric = metric
			npr.SameNode = false
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
				npr.Summary = append(npr.Summary, nr)
			}
			sr.Results = append(sr.Results, npr)
		} else {
			// Reset the result as we are now testing a different scenario
			// Consider breaking the result per-scenario-config
			npr := netperf.Data{}
			npr.Config = nc
			npr.Metric = metric
			npr.SameNode = true
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
				npr.Summary = append(npr.Summary, nr)
			}
			sr.Results = append(sr.Results, npr)
		}
	}
	netperf.ShowStreamResult(sr)
	netperf.ShowRRResult(sr)
	err = netperf.WriteCSVResult(sr)
	if err != nil {
		log.Error(err)
		os.Exit(1)
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
