package main

import (
	"context"
	encodeJson "encoding/json"
	"fmt"
	"io"
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
	"github.com/cloud-bulldozer/k8s-netperf/pkg/virtctl"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const index = "k8s-netperf"
const retry = 3

var (
	cfgfile          string
	nl               bool
	clean            bool
	netperf          bool
	iperf3           bool
	uperf            bool
	ibWriteBw        string
	udnl2            bool
	udnl3            bool
	cudn             string
	udnPluginBinding string
	acrossAZ         bool
	full             bool
	hostNetOnly      bool
	pod              bool
	vm               bool
	vmimage          string
	useVirtctl       bool
	debug            bool
	bridge           string
	bridgeNetwork    string
	bridgeNamespace  string
	promURL          string
	id               string
	searchURL        string
	showMetrics      bool
	tcpt             float64
	json             bool
	version          bool
	csvArchive       bool
	searchIndex      string
	serverIPAddr     string
	sockets          uint32
	cores            uint32
	threads          uint32
	privileged       bool
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
		// Check if ibWriteBw flag was set and has a valid value
		ibWriteBwEnabled := cmd.Flags().Changed("ib-write-bw")
		if ibWriteBwEnabled && strings.TrimSpace(ibWriteBw) == "" {
			log.Fatalf("😭 --ib-write-bw requires nic:gid parameter (e.g., --ib-write-bw=mlx5_0:0)")
		}

		if !uperf && !netperf && !iperf3 && !ibWriteBwEnabled {
			log.Fatalf("😭 At least one driver needs to be enabled")
		}
		// Validate mutually exclusive UDN flags
		if udnl2 && udnl3 {
			log.Fatal("flags --udnl2 and --udnl3 are mutually exclusive; please set only one")
		}
		if cudn != "" && (udnl2 || udnl3) {
			log.Fatal("flags --cudn and --udnl2/--udnl3 are mutually exclusive; please set only one")
		}
		if ibWriteBwEnabled && (!privileged || !hostNetOnly) {
			log.Fatalf("😭 ib_write_bw driver requires both --privileged and --hostNet flags")
		}
		if ibWriteBwEnabled && (udnl2 || udnl3 || cudn != "") {
			log.Fatalf("😭 ib_write_bw driver cannot be used with UDN flags (--udnl2, --udnl3, --cudn)")
		}
		if ibWriteBwEnabled && vm {
			log.Fatalf("😭 ib_write_bw driver cannot be used with --vm flag")
		}
		if ibWriteBwEnabled && bridge != "" {
			log.Fatalf("😭 ib_write_bw driver cannot be used with --bridge flag")
		}

		// If a specific driver is explicitly requested, disable the default netperf driver
		if (iperf3 || uperf || ibWriteBwEnabled) && !cmd.Flags().Changed("netperf") {
			netperf = false
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
			cleanup(client, rconfig)
		}
		s := config.PerfScenarios{
			HostNetwork:     full || hostNetOnly,
			HostNetworkOnly: hostNetOnly,
			NodeLocal:       nl,
			AcrossAZ:        acrossAZ,
			RestConfig:      *rconfig,
			Configs:         cfg,
			ClientSet:       client,
			BridgeNetwork:   bridge,
			BridgeNamespace: bridgeNamespace,
			Cudn:            cudn != "",
			IbWriteBwParams: ibWriteBw,
			Sockets:         sockets,
			Cores:           cores,
			Threads:         threads,
			Privileged:      privileged,
		}
		if serverIPAddr != "" {
			s.ExternalServer = true
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
		err = k8s.BuildInfra(client, udnl2 || udnl3)
		if err != nil {
			log.Error(err)
			os.Exit(1)
		}

		if udnl2 || udnl3 {
			s.Udn = true
			// Create a dynamic client
			dynClient, err := dynamic.NewForConfig(rconfig)
			if err != nil {
				log.Error(err)
			}
			s.DClient = dynClient
			if udnl2 {
				err = k8s.DeployL2Udn(dynClient)
			} else if udnl3 {
				err = k8s.DeployL3Udn(dynClient)
			}
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
		} else if cudn != "" {
			s.Cudn = true
			dynClient, err := dynamic.NewForConfig(rconfig)
			if err != nil {
				log.Error(err)
			}
			err = k8s.DeployCUDN(dynClient, cudn)
			if err != nil {
				log.Error(err)
				os.Exit(1)
			}
		}

		s.Pod = pod
		if vm {
			s.VM = true
			s.VMImage = vmimage
			s.UseVirtctl = useVirtctl
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
			if len(bridge) > 0 {
				err := k8s.DeployNADBridge(s.DClient, bridge)
				if err != nil {
					log.Error(err)
				}
				s.BridgeServerNetwork, s.BridgeClientNetwork, err = parseNetworkConfig(bridgeNetwork)
				if err != nil {
					log.Error(err)
				}
			}
			if s.Udn {
				s.UdnPluginBinding = udnPluginBinding
			}
		}

		// Validate bridge network configuration before creating pods
		if bridge != "" && !vm {
			// Create dynamic client for validation if not already created
			var dynClient dynamic.Interface
			if s.DClient != nil {
				dynClient = s.DClient
			} else {
				dynClient, err = dynamic.NewForConfig(rconfig)
				if err != nil {
					log.Fatalf("Failed to create dynamic client for bridge validation: %v", err)
				}
			}

			err = k8s.ValidateBridgeNetwork(client, dynClient, bridge, bridgeNamespace)
			if err != nil {
				log.Fatalf("Bridge network validation failed: %v", err)
			}
		}

		// Determine requested drivers BEFORE building SUT
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
		if ibWriteBwEnabled {
			requestedDrivers = append(requestedDrivers, "ib_write_bw")
		}

		// Set requested drivers BEFORE BuildSUT
		s.RequestedDrivers = requestedDrivers

		// Debug: Print requested drivers
		log.Debugf("🔥 Requested drivers: %v", requestedDrivers)
		log.Debugf("🔥 netperf=%v, iperf3=%v, uperf=%v, ibWriteBw=%v", netperf, iperf3, uperf, ibWriteBwEnabled)

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

		// Run pod tests if enabled
		if s.Pod {
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
					// Skip podNetwork tests if hostNetOnly is enabled
					if !hostNetOnly {
						pr = executeWorkload(nc, s, false, driver, false)
						if len(pr.Profile) > 1 {
							sr.Results = append(sr.Results, pr)
						}
					}
				}
			}
		}

		// Run VM tests if enabled
		if s.VM {
			sr.Virt = true
			if s.UseVirtctl {
				log.Info("Connecting to VMI using virtctl")
			} else {
				log.Info("Connecting via ssh to the VMI")
			}

			// Use the new unified connection method
			vmClient, err := k8s.ConnectToVM(&s)
			if err != nil {
				log.Fatal(err)
			}
			s.VMClientExecutor = vmClient

			// Also set SSHClient for backward compatibility if using SSH
			if !s.UseVirtctl {
				sshClient, err := k8s.SSHConnect(&s)
				if err != nil {
					log.Fatal(err)
				}
				s.SSHClient = sshClient
			}
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
					// Skip podNetwork tests if hostNetOnly is enabled
					if !hostNetOnly {
						pr = executeWorkload(nc, s, false, driver, true)
						if len(pr.Profile) > 1 {
							sr.Results = append(sr.Results, pr)
						}
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
				sr.ClusterMetadata = metadata
			} else {
				log.Error("Issue getting common metadata using go-commons")
			}
		}

		node := metrics.NodeDetails(pcon)
		sr.Kernel = node.Metric.Kernel
		shortReg, _ := regexp.Compile(`([0-9]\.[0-9]+)-*`)
		short := shortReg.FindString(sr.OCPVersion)
		sr.OCPShortVersion = short
		mtu, err := metrics.NodeMTU(pcon)
		if err == nil {
			sr.MTU = mtu
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
		if !hostNetOnly && result.CheckHostResults(sr) {
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
			cleanup(client, rconfig)
		}
		// Cleanup extracted virtctl binary if any
		if err := virtctl.CleanupExtractedBinary(); err != nil {
			log.Debugf("Failed to cleanup extracted virtctl binary: %v", err)
		}
		os.Exit(retCode)
	},
}

func cleanup(client *kubernetes.Clientset, rconfig *rest.Config) {
	if cudn != "" {
		dynClient, err := dynamic.NewForConfig(rconfig)
		if err != nil {
			log.Error(err)
		}
		err = k8s.DestroyCUdn(dynClient, k8s.CudnName)
		if err != nil {
			log.Error(err)
		}
	}
	err := k8s.DestroyNamespace(client)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}
}

// Function to parse the JSON from a file and return the IP parts (before '/')
func parseNetworkConfig(jsonFile string) (string, string, error) {

	// Open the JSON file
	file, err := os.Open(jsonFile)
	if err != nil {
		return "", "", fmt.Errorf("error opening file: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warnf("Error closing file: %v", err)
		}
	}()

	// Read the file contents
	log.Debugf("Reading BridgeNetwork configuration from JSON file: %s ", jsonFile)
	content, err := io.ReadAll(file)
	if err != nil {
		return "", "", fmt.Errorf("error reading file: %v", err)
	}

	// Create an instance of the struct
	var netConfig config.BridgeNetworkConfig

	// Unmarshal the JSON string into the struct
	err = encodeJson.Unmarshal(content, &netConfig)
	if err != nil {
		return "", "", fmt.Errorf("error parsing JSON: %v", err)
	}

	// Extract the IP parts (before '/')
	serverIP := netConfig.BridgeServerNetwork
	clientIP := netConfig.BridgeClientNetwork

	// Return the extracted IPs
	return serverIP, clientIP, nil
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
	npr := result.Data{}
	npr.Virt = virt
	if serverIPAddr != "" {
		serverIP = serverIPAddr
		npr.ExternalServer = true
	} else if nc.Service {
		switch driverName {
		case "iperf3":
			serverIP = s.IperfService.Spec.ClusterIP
		case "uperf":
			serverIP = s.UperfService.Spec.ClusterIP
		default:
			serverIP = s.NetperfService.Spec.ClusterIP
		}
	} else if s.Udn {
		serverIP, err = k8s.ExtractUdnIp(s.Server.Items[0], k8s.UdnName)
		if err != nil {
			log.Fatal(err)
		}
		// collect UDN info
		if udnl2 {
			npr.UdnInfo = "layer2"
		} else if udnl3 {
			npr.UdnInfo = "layer3"
		}
		if s.VM {
			npr.UdnInfo = npr.UdnInfo + " - " + s.UdnPluginBinding
		}
	} else if s.Cudn {
		serverIP, err = k8s.ExtractUdnIp(s.Server.Items[0], k8s.CudnName)
		if err != nil {
			log.Fatal(err)
		}
		npr.UdnInfo = "Cudn -" + cudn
	} else if s.BridgeNetwork != "" {
		// For regular pods, extract bridge IP from network status
		serverIP, err = k8s.ExtractBridgeIp(s.Server.Items[0], s.BridgeNetwork, s.BridgeNamespace)
		if err != nil {
			log.Errorf("Failed to extract bridge IP: %v", err)
			// Fall back to default IP
			serverIP = s.Server.Items[0].Status.PodIP
		}
		log.Debugf("Using bridge network IP: %s (interface: net1)", serverIP)

		// Set bridge info similar to UdnInfo
		npr.BridgeInfo = fmt.Sprintf("%s/%s", s.BridgeNamespace, s.BridgeNetwork)
	} else if s.BridgeServerNetwork != "" {
		// For VMs, use static bridge IP from JSON config
		serverIP = strings.Split(s.BridgeServerNetwork, "/")[0]
		npr.BridgeInfo = fmt.Sprintf("VM Bridge (%s)", serverIP)
	} else {
		if virt {
			if hostNet && !s.NodeLocal {
				serverIP = s.VMServerHost.Items[0].Status.PodIP
			} else {
				serverIP = s.VMServer.Items[0].Status.PodIP
			}
		} else {
			if hostNet && !s.NodeLocal {
				serverIP = s.ServerHost.Items[0].Status.PodIP
			} else {
				serverIP = s.Server.Items[0].Status.PodIP
			}
		}
	}
	if !s.NodeLocal && !s.ExternalServer {
		if virt {
			Client = s.VMClientAcross
		} else {
			Client = s.ClientAcross
		}
	}
	if hostNet && !s.NodeLocal {
		if virt {
			Client = s.VMClientHost
		} else {
			Client = s.ClientHost
		}
	}
	npr.Config = nc
	npr.Metric = nc.Metric
	npr.Service = nc.Service
	npr.SameNode = s.NodeLocal
	npr.HostNetwork = hostNet
	npr.Virt = virt
	if s.AcrossAZ {
		npr.AcrossAZ = true
	} else {
		npr.AcrossAZ = nc.AcrossAZ
	}
	npr.StartTime = time.Now()
	log.Debugf("Executing workloads. hostNetwork is %t, service is %t, externalServer is %t, VM mode is %t", hostNet, nc.Service, npr.ExternalServer, virt)
	driver, err = drivers.NewDriver(driverName, nc)
	if err != nil {
		log.Fatal(err)
	}
	npr.Driver = driverName
	// Check if test is supported
	if !driver.IsTestSupported() {
		log.Warnf("Test %s is not supported with driver %s. Skipping.", nc.Profile, npr.Driver)
		return npr
	}
	for i := 0; i < nc.Samples; i++ {
		nr := sample.Sample{}
		r, err := driver.Run(s.ClientSet, s.RestConfig, nc, Client, serverIP, &s, virt)
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
				r, err := driver.Run(s.ClientSet, s.RestConfig, nc, Client, serverIP, &s, virt)
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
	rootCmd.Flags().BoolVar(&netperf, "netperf", true, "Use netperf as load driver (default true)")
	rootCmd.Flags().BoolVar(&iperf3, "iperf", false, "Use iperf3 as load driver (default false)")
	rootCmd.Flags().BoolVar(&uperf, "uperf", false, "Use uperf as load driver (default false)")
	rootCmd.Flags().StringVar(&ibWriteBw, "ib-write-bw", "", "Use ib_write_bw as load driver, requires nic:gid format (e.g., mlx5_0:0, requires --hostNet)")
	rootCmd.Flags().BoolVar(&clean, "clean", true, "Clean-up resources created by k8s-netperf (default true)")
	rootCmd.Flags().BoolVar(&json, "json", false, "Instead of human-readable output, return JSON to stdout (default false)")
	rootCmd.Flags().BoolVar(&nl, "local", false, "Run network performance tests with Server-Pods/Client-Pods on the same Node (default false)")
	rootCmd.Flags().BoolVar(&pod, "pod", true, "Run tests using pods (default true)")
	rootCmd.Flags().BoolVar(&vm, "vm", false, "Run tests using Virtual Machines (default false)")
	rootCmd.Flags().StringVar(&vmimage, "vm-image", "quay.io/containerdisks/fedora:39", "Use specified VM image (default quay.io/containerdisks/fedora:39)")
	rootCmd.Flags().BoolVar(&useVirtctl, "use-virtctl", false, "Use virtctl ssh for VM connections instead of traditional SSH (default false)")
	rootCmd.Flags().Uint32Var(&sockets, "sockets", 2, "Number of Sockets for VM (default 2)")
	rootCmd.Flags().Uint32Var(&cores, "cores", 2, "Number of cores for VM (default 2)")
	rootCmd.Flags().Uint32Var(&threads, "threads", 1, "Number of threads for VM (default 1)")
	rootCmd.Flags().BoolVar(&acrossAZ, "across", false, "Place the client and server across availability zones (default false)")
	rootCmd.Flags().BoolVar(&full, "all", false, "Run all tests scenarios - hostNet and podNetwork (if possible) (default false)")
	rootCmd.Flags().BoolVar(&hostNetOnly, "hostNet", false, "Run only hostNetwork tests (no podNetwork tests) (default false)")
	rootCmd.Flags().BoolVar(&debug, "debug", false, "Enable debug log (default false)")
	rootCmd.Flags().BoolVar(&udnl2, "udnl2", false, "Create and use a layer2 UDN as a primary network (default false)")
	rootCmd.Flags().BoolVar(&udnl3, "udnl3", false, "Create and use a layer3 UDN as a primary network (default false)")
	rootCmd.Flags().StringVar(&cudn, "cudn", "", "Create and use a Cluster UDN that would be used as a secondary network")
	rootCmd.MarkFlagsMutuallyExclusive("all", "hostNet")
	rootCmd.Flags().StringVar(&udnPluginBinding, "udnPluginBinding", "passt", "UDN with VMs only - the binding method of the UDN interface, select 'passt' or 'l2bridge' (default passt)")
	rootCmd.Flags().StringVar(&bridge, "bridge", "", "Name of the NetworkAttachmentDefinition to be used for bridge interface")
	rootCmd.Flags().StringVar(&bridgeNamespace, "bridgeNamespace", "default", "Namespace of the NetworkAttachmentDefinition for bridge interface (default default)")
	rootCmd.Flags().StringVar(&bridgeNetwork, "bridgeNetwork", "bridgeNetwork.json", "Json file for the VM network defined by the bridge interface - bridge should be enabled (default bridgeNetwork.json)")
	rootCmd.Flags().StringVar(&promURL, "prom", "", "Prometheus URL")
	rootCmd.Flags().StringVar(&id, "uuid", "", "User provided UUID")
	rootCmd.Flags().StringVar(&searchURL, "search", "", "OpenSearch URL, if you have auth, pass in the format of https://user:pass@url:port")
	rootCmd.Flags().StringVar(&searchIndex, "index", "", "OpenSearch Index to save the results to (default k8s-netperf)")
	rootCmd.Flags().BoolVar(&showMetrics, "metrics", false, "Show all system metrics retrieved from prom (default false)")
	rootCmd.Flags().Float64Var(&tcpt, "tcp-tolerance", 10, "Allowed %diff from hostNetwork to podNetwork, anything above tolerance will result in k8s-netperf exiting 1 (default 10)")
	rootCmd.Flags().BoolVar(&version, "version", false, "k8s-netperf version")
	rootCmd.Flags().BoolVar(&csvArchive, "csv", true, "Archive results, cluster and benchmark metrics in CSV files (default true)")
	rootCmd.Flags().StringVar(&serverIPAddr, "serverIP", "", "External Server IP Address")
	rootCmd.Flags().BoolVar(&privileged, "privileged", false, "Run pods with privileged security context (default false)")
	rootCmd.Flags().SortFlags = false
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
