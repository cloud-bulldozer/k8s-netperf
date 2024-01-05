package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/cloud-bulldozer/go-commons/indexers"
	ocpmetadata "github.com/cloud-bulldozer/go-commons/ocp-metadata"
	"github.com/cloud-bulldozer/go-commons/prometheus"
	cmdVersion "github.com/cloud-bulldozer/go-commons/version"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/archive"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/iperf"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/k8s"
	log "github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/metrics"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/netperf"
	result "github.com/cloud-bulldozer/k8s-netperf/pkg/results"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/sample"
	uperf_driver "github.com/cloud-bulldozer/k8s-netperf/pkg/uperf"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const namespace = "netperf"
const index = "k8s-netperf"
const retry = 3

var (
	cfgfile     string
	nl          bool
	clean       bool
	iperf3      bool
	uperf       bool
	acrossAZ    bool
	full        bool
	debug       bool
	promURL     string
	id          string
	searchURL   string
	showMetrics bool
	tcpt        float64
	json        bool
	version     bool
)

var rootCmd = &cobra.Command{
	Use:   "k8s-netperf",
	Short: "A tool to run network performance tests in Kubernetes cluster",
	Run: func(cmd *cobra.Command, args []string) {
		if version {
			fmt.Println("Version:", cmdVersion.Version)
			fmt.Println("Git Commit:", cmdVersion.GitCommit)
			fmt.Println("Build Date:", cmdVersion.BuildDate)
			fmt.Println("Go Version:", cmdVersion.GoVersion)
			fmt.Println("OS/Arch:", cmdVersion.OsArch)
			os.Exit(0)
		}

		uid := ""
		if len(id) > 0 {
			uid = id
		} else {
			u := uuid.New()
			uid = u.String()
		}

		if json {
			log.SetError()
		}

		if debug {
			log.SetDebug()
		}

		cfg, err := config.ParseConf(cfgfile)
		if err != nil {
			cf, err := config.ParseV2Conf(cfgfile)
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
			cfg = cf
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
		if clean {
			cleanup(client)
		}
		s := config.PerfScenarios{
			HostNetwork: full,
			NodeLocal:   nl,
			AcrossAZ:    acrossAZ,
			RestConfig:  *rconfig,
			Configs:     cfg,
			ClientSet:   client,
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
			log.Error("	$ k8s-netperf --local")
			os.Exit(1)
		}

		pavail := true
		pcon, _ := metrics.Discover()
		if len(promURL) > 1 {
			pcon.URL = promURL
		}
		pcon.Client, err = prometheus.NewClient(pcon.URL, pcon.Token, "", "", pcon.Verify)
		if err != nil {
			pavail = false
			log.Warn("😥 Prometheus is not available")
		} else {
			// Checking for microshift. In the event there is more nuance we will create a special microshift flag
			_, err := client.CoreV1().ConfigMaps("kube-public").Get(context.TODO(), "microshift-version", metav1.GetOptions{})
			if err == nil {
				pcon.OpenShift = true
			}

		}

		// Build the SUT (Deployments)
		err = k8s.BuildSUT(client, &s)
		if err != nil {
			log.Error(err)
			os.Exit(1)
		}

		var sr result.ScenarioResults
		sr.Version = cmdVersion.Version
		sr.GitCommit = cmdVersion.GitCommit
		// If the client and server needs to be across zones
		lz, zones, _ := k8s.GetZone(client)
		nodesInZone := zones[lz]
		var acrossAZ bool
		if nodesInZone > 1 {
			acrossAZ = false
		} else {
			acrossAZ = true
		}

		// Run through each test
		for _, nc := range s.Configs {
			// Determine the metric for the test
			metric := string("OP/s")
			if strings.Contains(nc.Profile, "STREAM") {
				metric = "Mb/s"
			}
			nc.Metric = metric
			nc.AcrossAZ = acrossAZ

			if s.HostNetwork {
				// No need to run hostNetwork through Service.
				if !nc.Service {
					npr := executeWorkload(nc, s, true, false, false)
					sr.Results = append(sr.Results, npr)
					if iperf3 {
						ipr := executeWorkload(nc, s, true, true, false)
						if len(ipr.Profile) > 1 {
							sr.Results = append(sr.Results, ipr)
						}
					}
					if uperf {
						upr := executeWorkload(nc, s, true, false, true)
						if len(upr.Profile) > 1 {
							sr.Results = append(sr.Results, upr)
						}
					}
				}
			}
			npr := executeWorkload(nc, s, false, false, false)
			sr.Results = append(sr.Results, npr)
			if iperf3 {
				ipr := executeWorkload(nc, s, false, true, false)
				if len(ipr.Profile) > 1 {
					sr.Results = append(sr.Results, ipr)
				}
			}
			if uperf {
				upr := executeWorkload(nc, s, false, false, true)
				if len(upr.Profile) > 1 {
					sr.Results = append(sr.Results, upr)
				}
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

		// Metadata
		meta, err := ocpmetadata.NewMetadata(&s.RestConfig)
		if err == nil {
			metadata, err := meta.GetClusterMetadata()
			if err == nil {
				sr.Metadata.ClusterMetadata = metadata
			} else {
				log.Error(" issue getting common metadata using go-commons")
			}
		}

		node := metrics.NodeDetails(pcon)
		sr.Metadata.Kernel = node.Metric.Kernel
		shortReg, _ := regexp.Compile(`([0-9]\.[0-9]+)-*`)
		short := shortReg.FindString(sr.Metadata.OCPVersion)
		sr.Metadata.OCPShortVersion = short
		sec, err := metrics.IPSecEnabled(pcon, fTime, lTime)
		if err == nil {
			sr.Metadata.IPsec = sec
		}
		mtu, err := metrics.NodeMTU(pcon)
		if err == nil {
			sr.Metadata.MTU = mtu
		}

		if len(searchURL) > 1 {
			jdocs, err := archive.BuildDocs(sr, uid)
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
			esClient, err := archive.Connect(searchURL, index, true)
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
			log.Infof("Indexing [%d] documents in %s with UUID %s", len(jdocs), index, uid)
			resp, err := (*esClient).Index(jdocs, indexers.IndexingOpts{})
			if err != nil {
				log.Error(err.Error())
			} else {
				log.Info(resp)
			}
		}

		if !json {
			result.ShowStreamResult(sr)
			result.ShowRRResult(sr)
			result.ShowLatencyResult(sr)
			result.ShowSpecificResults(sr)
			if showMetrics {
				result.ShowNodeCPU(sr)
				result.ShowPodCPU(sr)
			}
		} else {
			err = archive.WriteJSONResult(sr)
			if err != nil {
				log.Error(err)
			}
		}
		err = archive.WriteCSVResult(sr)
		if err != nil {
			log.Error(err)
			os.Exit(1)
		}
		if pavail {
			err = archive.WritePromCSVResult(sr)
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
		}
		err = archive.WriteSpecificCSV(sr)
		if err != nil {
			log.Error(err)
			os.Exit(1)
		}
		// Initially we are just checking against TCP_STREAM results.
		retCode := 0
		if result.CheckHostResults(sr) {
			diffs, err := result.TCPThroughputDiff(&sr)
			if err != nil {
				log.Error("Unable to calculate difference between HostNetwork and PodNetwork")
				retCode = 1
			} else {
				for _, diff := range diffs {
					if tcpt < diff.Result {
						log.Errorf("😥 TCP Single Stream (Message Size : %d) percent difference when comparing hostNetwork to podNetwork is greater than %.1f percent (%.1f percent) for %d streams", diff.MessageSize, tcpt, diff.Result, diff.Streams)
						retCode = 1
					}
				}
			}
		}
		if clean {
			cleanup(client)
		}
		os.Exit(retCode)
	},
}

func cleanup(client *kubernetes.Clientset) {
	log.Info("Cleaning resources created by k8s-netperf")
	svcList, err := k8s.GetServices(client, namespace)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}
	for svc := range svcList.Items {
		err = k8s.DestroyService(client, svcList.Items[svc])
		if err != nil {
			log.Error(err)
		}
	}
	dpList, err := k8s.GetDeployments(client, namespace)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}
	for dp := range dpList.Items {
		err = k8s.DestroyDeployment(client, dpList.Items[dp])
		if err != nil {
			log.Error(err)
		}
		_, err := k8s.WaitForDelete(client, dpList.Items[dp])
		if err != nil {
			log.Error(err)
			os.Exit(1)
		}
	}

}

func executeWorkload(nc config.Config, s config.PerfScenarios, hostNet bool, iperf3 bool, uperf bool) result.Data {
	serverIP := ""
	service := false
	sameNode := true
	Client := s.Client
	if nc.Service {
		service = true
		if iperf3 {
			serverIP = s.IperfService.Spec.ClusterIP
		} else if uperf {
			serverIP = s.UperfService.Spec.ClusterIP
		} else {
			serverIP = s.NetperfService.Spec.ClusterIP
		}
	} else {
		if hostNet {
			serverIP = s.ServerHost.Items[0].Status.PodIP
		} else {
			serverIP = s.Server.Items[0].Status.PodIP
		}
	}
	if !s.NodeLocal {
		Client = s.ClientAcross
		sameNode = false
	}
	if hostNet {
		Client = s.ClientHost
	}
	npr := result.Data{}
	if iperf3 {
		// iperf doesn't support all tests cases
		if !iperf.TestSupported(nc.Profile) {
			return npr
		}
	}
	if uperf {
		// uperf doesn't support all tests cases
		if !uperf_driver.TestSupported(nc.Profile) {
			return npr
		}
	}

	npr.Config = nc
	npr.Metric = nc.Metric
	npr.Service = service
	npr.SameNode = sameNode
	npr.HostNetwork = hostNet
	if s.AcrossAZ {
		npr.AcrossAZ = true
	} else {
		npr.AcrossAZ = nc.AcrossAZ
	}
	npr.StartTime = time.Now()
	log.Debugf("Executing workloads. hostNetwork is %t, service is %t", hostNet, service)
	for i := 0; i < nc.Samples; i++ {
		nr := sample.Sample{}
		if iperf3 {
			npr.Driver = "iperf3"
			r, err := iperf.Run(s.ClientSet, s.RestConfig, nc, Client, serverIP)
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
			nr, err = iperf.ParseResults(&r)
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
		} else if uperf {
			npr.Driver = "uperf"
			r, err := uperf_driver.Run(s.ClientSet, s.RestConfig, nc, Client, serverIP)
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
			nr, err = uperf_driver.ParseResults(&r)
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
		} else {
			npr.Driver = "netperf"
			r, err := netperf.Run(s.ClientSet, s.RestConfig, nc, Client, serverIP)
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
			nr, err = netperf.ParseResults(&r)
			if err != nil {
				log.Error(err)
				try := 0
				success := false
				// Retry the current test.
				for try < retry {
					log.Warn("Rerunning test.")
					r, err := netperf.Run(s.ClientSet, s.RestConfig, nc, Client, serverIP)
					if err != nil {
						log.Error(err)
						continue
					}
					nr, err = netperf.ParseResults(&r)
					if err != nil {
						log.Error(err)
						try++
					} else {
						success = true
						break
					}
				}
				if !success {
					log.Error("test was unsuccessful after retry.")
					os.Exit(1)
				}
			}
		}
		npr.LossSummary = append(npr.LossSummary, float64(nr.LossPercent))
		npr.RetransmitSummary = append(npr.RetransmitSummary, nr.Retransmits)
		npr.ThroughputSummary = append(npr.ThroughputSummary, nr.Throughput)
		npr.LatencySummary = append(npr.LatencySummary, nr.Latency99ptile)
	}
	npr.EndTime = time.Now()
	npr.ClientNodeInfo = s.ClientNodeInfo
	npr.ServerNodeInfo = s.ServerNodeInfo
	npr.ServerNodeLabels, _ = k8s.GetNodeLabels(s.ClientSet, s.ServerNodeInfo.Hostname)
	npr.ClientNodeLabels, _ = k8s.GetNodeLabels(s.ClientSet, s.ClientNodeInfo.Hostname)

	return npr
}

func main() {
	rootCmd.Flags().StringVar(&cfgfile, "config", "netperf.yml", "K8s netperf Configuration File")
	rootCmd.Flags().BoolVar(&iperf3, "iperf", false, "Use iperf3 as load driver (along with netperf)")
	rootCmd.Flags().BoolVar(&uperf, "uperf", false, "Use uperf as load driver (along with netperf)")
	rootCmd.Flags().BoolVar(&clean, "clean", true, "Clean-up resources created by k8s-netperf")
	rootCmd.Flags().BoolVar(&json, "json", false, "Instead of human-readable output, return JSON to stdout")
	rootCmd.Flags().BoolVar(&nl, "local", false, "Run network performance tests with Server-Pods/Client-Pods on the same Node")
	rootCmd.Flags().BoolVar(&acrossAZ, "across", false, "Place the client and server across availability zones")
	rootCmd.Flags().BoolVar(&full, "all", false, "Run all tests scenarios - hostNet and podNetwork (if possible)")
	rootCmd.Flags().BoolVar(&debug, "debug", false, "Enable debug log")
	rootCmd.Flags().StringVar(&promURL, "prom", "", "Prometheus URL")
	rootCmd.Flags().StringVar(&id, "uuid", "", "User provided UUID")
	rootCmd.Flags().StringVar(&searchURL, "search", "", "OpenSearch URL, if you have auth, pass in the format of https://user:pass@url:port")
	rootCmd.Flags().BoolVar(&showMetrics, "metrics", false, "Show all system metrics retrieved from prom")
	rootCmd.Flags().Float64Var(&tcpt, "tcp-tolerance", 10, "Allowed %diff from hostNetwork to podNetwork, anything above tolerance will result in k8s-netperf exiting 1.")
	rootCmd.Flags().BoolVar(&version, "version", false, "k8s-netperf version")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

}
