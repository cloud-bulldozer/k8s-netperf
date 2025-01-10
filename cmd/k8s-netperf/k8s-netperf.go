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
	"github.com/cloud-bulldozer/k8s-netperf/pkg/drivers"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/k8s"
	kubevirtv1 "github.com/cloud-bulldozer/k8s-netperf/pkg/kubevirt/client-go/clientset/versioned/typed/core/v1"
	log "github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/metrics"
	result "github.com/cloud-bulldozer/k8s-netperf/pkg/results"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/sample"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const index = "k8s-netperf"
const retry = 3

var (
	cfgfile     string
	nl          bool
	clean       bool
	netperf     bool
	iperf3      bool
	uperf       bool
	udn         bool
	acrossAZ    bool
	full        bool
	vm          bool
	vmimage     string
	debug       bool
	promURL     string
	id          string
	searchURL   string
	showMetrics bool
	tcpt        float64
	json        bool
	version     bool
	csvArchive  bool
	searchIndex string
)

var rootCmd = &cobra.Command{
	Use:   "k8s-netperf",
	Short: "A tool to run network performance tests in Kubernetes cluster",
	PreRun: func(cmd *cobra.Command, args []string) {
		log.Infof("Starting k8s-netperf (%s@%s)", cmdVersion.Version, cmdVersion.GitCommit)
	},
	Run: func(cmd *cobra.Command, args []string) {
		var acrossAZ bool
		var sr result.ScenarioResults
		if version {
			fmt.Println("Version:", cmdVersion.Version)
			fmt.Println("Git Commit:", cmdVersion.GitCommit)
			fmt.Println("Build Date:", cmdVersion.BuildDate)
			fmt.Println("Go Version:", cmdVersion.GoVersion)
			fmt.Println("OS/Arch:", cmdVersion.OsArch)
			os.Exit(0)
		}
		if !(uperf || netperf || iperf3) {
			log.Fatalf("😭 At least one driver needs to be enabled")
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
				log.Fatal(err)
			}
			cfg = cf
		}
		// Read in k8s config
		kconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			&clientcmd.ConfigOverrides{})
		rconfig, err := kconfig.ClientConfig()
		if err != nil {
			log.Fatal(err)
		}
		client, err := kubernetes.NewForConfig(rconfig)
		if err != nil {
			log.Fatal(err)
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
			log.Fatal(err)
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
		}

		// Build the namespace and create the sa account
		err = k8s.BuildInfra(client)
		if err != nil {
			log.Error(err)
			os.Exit(1)
		}

		if udn {
			s.Udn = true
			// Create a dynamic client
			dynClient, err := dynamic.NewForConfig(rconfig)
			if err != nil {
				log.Error(err)
			}
			s.DClient = dynClient
			err = k8s.DeployL2Udn(dynClient)
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
		}

		if vm {
			s.VM = true
			s.VMImage = vmimage
			// Create a dynamic client
			if s.DClient == nil {
				dynClient, err := dynamic.NewForConfig(rconfig)
				if err != nil {
					log.Error(err)
				}
				s.DClient = dynClient
			}
			kclient, err := kubevirtv1.NewForConfig(rconfig)
			if err != nil {
				log.Error(err)
			}
			s.KClient = kclient
		}

		// Build the SUT (Deployments)
		err = k8s.BuildSUT(client, &s)
		if err != nil {
			log.Fatal(err)
		}

		sr.Version = cmdVersion.Version
		sr.GitCommit = cmdVersion.GitCommit
		// If the client and server needs to be across zones
		lz, zones, _ := k8s.GetZone(client)
		nodesInZone := zones[lz]
		if nodesInZone > 1 {
			acrossAZ = false
		} else {
			acrossAZ = true
		}
		time.Sleep(5 * time.Second) // Wait some seconds to ensure service is ready
		var requestedDrivers []string
		if netperf {
			requestedDrivers = append(requestedDrivers, "netperf")
		}
		if uperf {
			requestedDrivers = append(requestedDrivers, "uperf")
		}
		if iperf3 {
			requestedDrivers = append(requestedDrivers, "iperf3")
		}

		// Run through each test
		if !s.VM {
			for _, nc := range s.Configs {
				// Determine the metric for the test
				metric := string("OP/s")
				if strings.Contains(nc.Profile, "STREAM") {
					metric = "Mb/s"
				}
				nc.Metric = metric
				nc.AcrossAZ = acrossAZ
				// No need to run hostNetwork through Service.
				var pr result.Data
				for _, driver := range requestedDrivers {
					if s.HostNetwork && !nc.Service {
						pr = executeWorkload(nc, s, true, driver, false)
						if len(pr.Profile) > 1 {
							sr.Results = append(sr.Results, pr)
						}
					}
					pr = executeWorkload(nc, s, false, driver, false)
					if len(pr.Profile) > 1 {
						sr.Results = append(sr.Results, pr)
					}
				}
			}
		} else {
			sr.Virt = true
			log.Info("Connecting via ssh to the VMI")
			client, err := k8s.SSHConnect(&s)
			if err != nil {
				log.Fatal(err)
			}
			s.SSHClient = client
			for _, nc := range s.Configs {
				// Determine the metric for the test
				metric := string("OP/s")
				if strings.Contains(nc.Profile, "STREAM") {
					metric = "Mb/s"
				}
				nc.Metric = metric
				nc.AcrossAZ = acrossAZ
				// No need to run hostNetwork through Service.
				var pr result.Data
				for _, driver := range requestedDrivers {
					if s.HostNetwork && !nc.Service {
						pr = executeWorkload(nc, s, true, driver, true)
						if len(pr.Profile) > 1 {
							sr.Results = append(sr.Results, pr)
						}
					}
					pr = executeWorkload(nc, s, false, driver, true)
					if len(pr.Profile) > 1 {
						sr.Results = append(sr.Results, pr)
					}
				}
			}
		}

		if pavail {
			for i, npr := range sr.Results {
				if len(npr.ClientNodeInfo.NodeName) > 0 && len(npr.ServerNodeInfo.NodeName) > 0 {
					sr.Results[i].ClientMetrics, _ = metrics.QueryNodeCPU(npr.ClientNodeInfo, pcon, npr.StartTime, npr.EndTime)
					sr.Results[i].ServerMetrics, _ = metrics.QueryNodeCPU(npr.ServerNodeInfo, pcon, npr.StartTime, npr.EndTime)
					metrics.VSwitchCPU(npr.ClientNodeInfo, pcon, npr.StartTime, npr.EndTime, &sr.Results[i].ClientMetrics)
					metrics.VSwitchMem(npr.ClientNodeInfo, pcon, npr.StartTime, npr.EndTime, &sr.Results[i].ClientMetrics)
					metrics.VSwitchCPU(npr.ServerNodeInfo, pcon, npr.StartTime, npr.EndTime, &sr.Results[i].ServerMetrics)
					metrics.VSwitchMem(npr.ServerNodeInfo, pcon, npr.StartTime, npr.EndTime, &sr.Results[i].ServerMetrics)
					sr.Results[i].ClientPodCPU, _ = metrics.TopPodCPU(npr.ClientNodeInfo, pcon, npr.StartTime, npr.EndTime)
					sr.Results[i].ServerPodCPU, _ = metrics.TopPodCPU(npr.ServerNodeInfo, pcon, npr.StartTime, npr.EndTime)
					sr.Results[i].ClientPodMem, _ = metrics.TopPodMem(npr.ClientNodeInfo, pcon, npr.StartTime, npr.EndTime)
					sr.Results[i].ServerPodMem, _ = metrics.TopPodMem(npr.ServerNodeInfo, pcon, npr.StartTime, npr.EndTime)
				}
			}
		}

		// Metadata
		meta, err := ocpmetadata.NewMetadata(&s.RestConfig)
		if err == nil {
			metadata, err := meta.GetClusterMetadata()
			if err == nil {
				sr.Metadata.ClusterMetadata = metadata
			} else {
				log.Error("Issue getting common metadata using go-commons")
			}
		}

		node := metrics.NodeDetails(pcon)
		sr.Metadata.Kernel = node.Metric.Kernel
		shortReg, _ := regexp.Compile(`([0-9]\.[0-9]+)-*`)
		short := shortReg.FindString(sr.Metadata.OCPVersion)
		sr.Metadata.OCPShortVersion = short
		mtu, err := metrics.NodeMTU(pcon)
		if err == nil {
			sr.Metadata.MTU = mtu
		}

		if len(searchURL) > 1 {
			var esClient *indexers.Indexer
			jdocs, err := archive.BuildDocs(sr, uid)
			if err != nil {
				log.Fatal(err)
			}
			if len(searchIndex) > 1 {
				esClient, err = archive.Connect(searchURL, searchIndex, true)
				if err != nil {
					log.Fatal(err)
				}
			} else {
				esClient, err = archive.Connect(searchURL, index, true)
				if err != nil {
					log.Fatal(err)
				}

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
				result.ShowPodMem(sr)
			}
		} else {
			err = archive.WriteJSONResult(sr)
			if err != nil {
				log.Error(err)
			}
		}
		if csvArchive {
			if archive.WriteCSVResult(sr) != nil {
				log.Fatal(err)
			}
			if pavail && archive.WritePromCSVResult(sr) != nil {
				log.Fatal(err)
			}
			if archive.WriteSpecificCSV(sr) != nil {
				log.Fatal(err)
			}
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
	err := k8s.DestroyNamespace(client)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}
}

// executeWorkload executes the workload and returns the result data.
func executeWorkload(nc config.Config,
	s config.PerfScenarios,
	hostNet bool,
	driverName string, virt bool) result.Data {
	serverIP := ""
	var err error
	Client := s.Client
	var driver drivers.Driver
	if nc.Service {
		if driverName == "iperf3" {
			serverIP = s.IperfService.Spec.ClusterIP
		} else if driverName == "uperf" {
			serverIP = s.UperfService.Spec.ClusterIP
		} else {
			serverIP = s.NetperfService.Spec.ClusterIP
		}
	} else if s.Udn {
		serverIP, err = k8s.ExtractUdnIp(s)
		if err != nil {
			log.Fatal(err)
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
	}
	if hostNet {
		Client = s.ClientHost
	}
	npr := result.Data{}
	npr.Config = nc
	npr.Metric = nc.Metric
	npr.Service = nc.Service
	npr.SameNode = s.NodeLocal
	npr.HostNetwork = hostNet
	if s.AcrossAZ {
		npr.AcrossAZ = true
	} else {
		npr.AcrossAZ = nc.AcrossAZ
	}
	npr.StartTime = time.Now()
	log.Debugf("Executing workloads. hostNetwork is %t, service is %t", hostNet, nc.Service)
	for i := 0; i < nc.Samples; i++ {
		nr := sample.Sample{}
		if driverName == "iperf3" {
			driver = drivers.NewDriver("iperf3")
			npr.Driver = "iperf3"
		} else if driverName == "uperf" {
			driver = drivers.NewDriver("uperf")
			npr.Driver = "uperf"
		} else {
			driver = drivers.NewDriver("netperf")
			npr.Driver = "netperf"
		}
		// Check if test is supported
		if !driver.IsTestSupported(nc.Profile) {
			log.Warnf("Test %s is not supported with driver %s. Skipping.", nc.Profile, npr.Driver)
			return npr
		}
		r, err := driver.Run(s.ClientSet, s.RestConfig, nc, Client, serverIP, &s)
		if err != nil {
			log.Fatal(err)
		}
		nr, err = driver.ParseResults(&r, nc)
		if err != nil {
			log.Error(err)
			try := 0
			success := false
			// Retry the current test.
			for try < retry {
				log.Warn("Rerunning test.")
				r, err := driver.Run(s.ClientSet, s.RestConfig, nc, Client, serverIP, &s)
				if err != nil {
					log.Error(err)
					continue
				}
				nr, err = driver.ParseResults(&r, nc)
				if err != nil {
					log.Error(err)
					try++
				} else {
					success = true
					break
				}
			}
			if !success {
				log.Fatal("test was unsuccessful after retry.")
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

	return npr
}

func main() {
	rootCmd.Flags().StringVar(&cfgfile, "config", "netperf.yml", "K8s netperf Configuration File")
	rootCmd.Flags().BoolVar(&netperf, "netperf", true, "Use netperf as load driver")
	rootCmd.Flags().BoolVar(&iperf3, "iperf", false, "Use iperf3 as load driver")
	rootCmd.Flags().BoolVar(&uperf, "uperf", false, "Use uperf as load driver")
	rootCmd.Flags().BoolVar(&clean, "clean", true, "Clean-up resources created by k8s-netperf")
	rootCmd.Flags().BoolVar(&json, "json", false, "Instead of human-readable output, return JSON to stdout")
	rootCmd.Flags().BoolVar(&nl, "local", false, "Run network performance tests with Server-Pods/Client-Pods on the same Node")
	rootCmd.Flags().BoolVar(&vm, "vm", false, "Launch Virtual Machines instead of pods for client/servers")
	rootCmd.Flags().StringVar(&vmimage, "vm-image", "kubevirt/fedora-cloud-container-disk-demo:latest", "Use specified VM image")
	rootCmd.Flags().BoolVar(&acrossAZ, "across", false, "Place the client and server across availability zones")
	rootCmd.Flags().BoolVar(&full, "all", false, "Run all tests scenarios - hostNet and podNetwork (if possible)")
	rootCmd.Flags().BoolVar(&debug, "debug", false, "Enable debug log")
	rootCmd.Flags().BoolVar(&udn, "udn", false, "Create and use a UDN called 'udn-l2-primary' as primary network.")
	rootCmd.Flags().StringVar(&promURL, "prom", "", "Prometheus URL")
	rootCmd.Flags().StringVar(&id, "uuid", "", "User provided UUID")
	rootCmd.Flags().StringVar(&searchURL, "search", "", "OpenSearch URL, if you have auth, pass in the format of https://user:pass@url:port")
	rootCmd.Flags().StringVar(&searchIndex, "index", "", "OpenSearch Index to save the results to, defaults to k8s-netperf")
	rootCmd.Flags().BoolVar(&showMetrics, "metrics", false, "Show all system metrics retrieved from prom")
	rootCmd.Flags().Float64Var(&tcpt, "tcp-tolerance", 10, "Allowed %diff from hostNetwork to podNetwork, anything above tolerance will result in k8s-netperf exiting 1.")
	rootCmd.Flags().BoolVar(&version, "version", false, "k8s-netperf version")
	rootCmd.Flags().BoolVar(&csvArchive, "csv", true, "Archive results, cluster and benchmark metrics in CSV files")
	rootCmd.Flags().SortFlags = false
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
