package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	log "github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/metrics"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/pointer"
)

// DeploymentParams describes the deployment
// Server pod can run multiple containers, each command in Commands will represent a container command
type DeploymentParams struct {
	HostNetwork     bool
	Name            string
	Namespace       string
	Replicas        int32
	Image           string
	Labels          map[string]string
	Commands        [][]string
	PodAffinity     corev1.PodAffinity
	PodAntiAffinity corev1.PodAntiAffinity
	NodeAffinity    corev1.NodeAffinity
	Port            int
}

// ServiceParams describes the service specific details
type ServiceParams struct {
	Name      string
	Namespace string
	Labels    map[string]string
	CtlPort   int32
	DataPorts []int32
}

type PodNetworksData struct {
	IPAddresses []string `json:"ip_addresses"`
	MacAddress  string   `json:"mac_address"`
	GatewayIPs  []string `json:"gateway_ips"`
	Role        string   `json:"role"`
}

const sa string = "netperf"
const namespace string = "netperf"

// NetperfServerCtlPort control port for the service
const NetperfServerCtlPort = 12865

// IperfServerCtlPort control port for the service
const IperfServerCtlPort = 22865

// UperferverCtlPort control port for the service
const UperfServerCtlPort = 30000

// NetperfServerDataPort data port for the service
const NetperfServerDataPort = 42424

// IperfServerDataPort data port for the service
const IperfServerDataPort = 43433

// UperfServerDataPort data port for the service
const UperfServerDataPort = 30001

// Labels we will apply to k8s assets.
const serverRole = "server"
const clientRole = "client-local"
const clientAcrossRole = "client-across"
const hostNetServerRole = "host-server"
const hostNetClientRole = "host-client"
const k8sNetperfImage = "quay.io/cloud-bulldozer/k8s-netperf:latest"
const udnName = "udn-l2-primary"

// BuildInfra will create the infra for the SUT
func BuildInfra(client *kubernetes.Clientset, udn bool) error {
	_, err := client.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err == nil {
		log.Infof("♻️ Namespace already exists, reusing it")
	} else {
		log.Infof("🔨 Creating namespace: %s", namespace)
		if udn {
			_, err = client.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace,
				Labels: map[string]string{"k8s.ovn.org/primary-user-defined-network": ""}}}, metav1.CreateOptions{})
		} else {
			_, err = client.CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{})
		}
		if err != nil {
			return fmt.Errorf("😥 Unable to create namespace: %v", err)
		}
	}
	_, err = client.CoreV1().ServiceAccounts(namespace).Get(context.TODO(), sa, metav1.GetOptions{})
	if err == nil {
		log.Infof("♻️ Service account already exists, reusing it")
	} else {
		log.Infof("🔨 Creating service account: %s", sa)
		_, err = client.CoreV1().ServiceAccounts(namespace).Create(context.TODO(), &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: sa}}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("😥 Unable to create service account: %v", err)
		}
	}
	rBinding := &v1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sa,
			Namespace: namespace,
		},
		RoleRef: v1.RoleRef{
			Kind: "ClusterRole",
			Name: "system:openshift:scc:hostnetwork",
		},
		Subjects: []v1.Subject{
			{
				Namespace: namespace,
				Name:      sa,
				Kind:      "ServiceAccount",
			},
		},
	}
	_, err = client.RbacV1().RoleBindings(namespace).Get(context.TODO(), sa, metav1.GetOptions{})
	if err == nil {
		log.Infof("♻️ Role binding already exists, reusing it")
	} else {
		_, err = client.RbacV1().RoleBindings(namespace).Create(context.TODO(), rBinding, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("😥 Unable to create role-binding: %v", err)
		}
	}
	return nil
}

// Create a User Defined Network for the tests
func DeployL2Udn(dynamicClient *dynamic.DynamicClient) error {
	log.Infof("Deploying L2 Primary UDN in the NS : %s", namespace)
	udn := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "k8s.ovn.org/v1",
			"kind":       "UserDefinedNetwork",
			"metadata": map[string]interface{}{
				"name":      udnName,
				"namespace": "netperf",
			},
			"spec": map[string]interface{}{
				"topology": "Layer2",
				"layer2": map[string]interface{}{
					"role":    "Primary",
					"subnets": []string{"10.0.0.0/24", "2001:db8::/60"},
				},
			},
		},
	}

	// Specify the GVR for UDN
	gvr := schema.GroupVersionResource{
		Group:    "k8s.ovn.org",
		Version:  "v1",
		Resource: "userdefinednetworks",
	}
	_, err := dynamicClient.Resource(gvr).Namespace(namespace).Create(context.TODO(), udn, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}

// Create a NetworkAttachcmentDefinition object for a bridge connection
func DeployNADBridge(dyn *dynamic.DynamicClient, bridgeName string) error {
	nadBridge := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "k8s.cni.cncf.io/v1",
			"kind":       "NetworkAttachmentDefinition",
			"metadata": map[string]interface{}{
				"name":      "br-netperf",
				"namespace": "netperf",
				"annotations": map[string]interface{}{
					"k8s.v1.cni.cncf.io/resourceName": "bridge.network.kubevirt.io/" + bridgeName,
				},
			},
			"spec": map[string]interface{}{
				"config": `{"cniVersion": "0.3.1", "type": "bridge", "name": "br-netperf", "bridge": "` + bridgeName + `"}`,
			},
		},
	}
	gvr := schema.GroupVersionResource{
		Group:    "k8s.cni.cncf.io",
		Version:  "v1",
		Resource: "network-attachment-definitions",
	}
	_, err := dyn.Resource(gvr).Namespace(namespace).Create(context.TODO(), nadBridge, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}

// BuildSUT Build the k8s env to run network performance tests
func BuildSUT(client *kubernetes.Clientset, s *config.PerfScenarios) error {
	var netperfDataPorts []int32
	// Check if nodes have the zone label to keep the netperf test
	// in the same AZ/Zone versus across AZ/Zone
	z, zones, err := GetZone(client)
	if err != nil {
		log.Warn(err)
	}
	numNodes := zones[z]
	if numNodes > 1 {
		log.Infof("Deploying in %s zone", z)
	} else {
		log.Warn("⚠️  Single node per zone and/or no zone labels")
	}
	if len(zones) < 2 && s.AcrossAZ {
		return fmt.Errorf("Unable to run AcrossAZ since there is < 2 zones")
	}
	acrossZone := ""
	if s.AcrossAZ {
		for az := range zones {
			if z != az {
				acrossZone = az
				log.Infof("Running AcrossAZ tests -- The other Zone is %s", acrossZone)
				break
			}
		}
	}

	// Get node count
	nodes, err := client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker=,node-role.kubernetes.io/infra!="})
	if err != nil {
		return err
	}
	ncount := len(nodes.Items)
	log.Debugf("Number of nodes with role worker: %d", ncount)
	if (s.HostNetwork || !s.NodeLocal) && ncount < 2 {
		return fmt.Errorf(" not enough nodes with label worker= to execute test (current number of nodes: %d).", ncount)
	}

	// Schedule pods to nodes with role worker=, but not nodes with infra= and workload=
	workerNodeSelectorExpression := &corev1.NodeSelector{
		NodeSelectorTerms: []corev1.NodeSelectorTerm{
			{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{Key: "node-role.kubernetes.io/worker", Operator: corev1.NodeSelectorOpIn, Values: []string{""}},
					{Key: "node-role.kubernetes.io/infra", Operator: corev1.NodeSelectorOpNotIn, Values: []string{""}},
					{Key: "node-role.kubernetes.io/workload", Operator: corev1.NodeSelectorOpNotIn, Values: []string{""}},
				},
			},
		},
	}

	clientRoleAffinity := []corev1.PodAffinityTerm{
		{
			LabelSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: "role", Operator: metav1.LabelSelectorOpIn, Values: []string{clientRole}},
				},
			},
			TopologyKey: "kubernetes.io/hostname",
		},
	}

	if s.NodeLocal {
		//  Create Netperf client on the same node as the server.
		cdp := DeploymentParams{
			Name:      "client",
			Namespace: "netperf",
			Replicas:  1,
			Image:     k8sNetperfImage,
			Labels:    map[string]string{"role": clientRole},
			Commands:  [][]string{{"/bin/bash", "-c", "sleep 10000000"}},
			Port:      NetperfServerCtlPort,
		}
		if z != "" && numNodes > 1 {
			cdp.NodeAffinity = corev1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: zoneNodeSelectorExpression(z, "client"),
			}
		}

		cdp.NodeAffinity = corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: workerNodeSelectorExpression,
		}
		if !s.VM {
			s.Client, err = deployDeployment(client, cdp)
			if err != nil {
				return err
			}
		}
		s.ClientNodeInfo, err = GetPodNodeInfo(client, labels.Set(cdp.Labels).String())
		if err != nil {
			return err
		}
	}

	// Create iperf service
	iperfSVC := ServiceParams{
		Name:      "iperf-service",
		Namespace: "netperf",
		Labels:    map[string]string{"role": serverRole},
		CtlPort:   IperfServerCtlPort,
		DataPorts: []int32{IperfServerDataPort},
	}
	s.IperfService, err = CreateService(iperfSVC, client)
	if err != nil {
		return fmt.Errorf("😥 Unable to create iperf service: %v", err)
	}

	// Create uperf service
	uperfSVC := ServiceParams{
		Name:      "uperf-service",
		Namespace: "netperf",
		Labels:    map[string]string{"role": serverRole},
		CtlPort:   UperfServerCtlPort,
		DataPorts: []int32{UperfServerDataPort},
	}
	s.UperfService, err = CreateService(uperfSVC, client)
	if err != nil {
		return fmt.Errorf("😥 Unable to create uperf service: %v", err)
	}

	// Create netperf service
	for i := 0; i < 16; i++ {
		netperfDataPorts = append(netperfDataPorts, NetperfServerDataPort+int32(i))
	}
	netperfSVC := ServiceParams{
		Name:      "netperf-service",
		Namespace: "netperf",
		Labels:    map[string]string{"role": serverRole},
		CtlPort:   NetperfServerCtlPort,
		DataPorts: netperfDataPorts,
	}
	s.NetperfService, err = CreateService(netperfSVC, client)
	if err != nil {
		return fmt.Errorf("😥 Unable to create netperf service: %v", err)
	}
	cdpAcross := DeploymentParams{
		Name:      "client-across",
		Namespace: "netperf",
		Replicas:  1,
		Image:     k8sNetperfImage,
		Labels:    map[string]string{"role": clientAcrossRole},
		Commands:  [][]string{{"/bin/bash", "-c", "sleep 10000000"}},
		Port:      NetperfServerCtlPort,
	}
	cdpAcross.PodAntiAffinity = corev1.PodAntiAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: clientRoleAffinity,
	}

	cdpHostAcross := DeploymentParams{
		Name:        "client-host",
		Namespace:   "netperf",
		Replicas:    1,
		HostNetwork: true,
		Image:       k8sNetperfImage,
		Labels:      map[string]string{"role": hostNetClientRole},
		Commands:    [][]string{{"/bin/bash", "-c", "sleep 10000000"}},
		Port:        NetperfServerCtlPort,
	}
	if z != "" {
		if numNodes > 1 {
			cdpAcross.NodeAffinity = corev1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: zoneNodeSelectorExpression(z, "client"),
				RequiredDuringSchedulingIgnoredDuringExecution:  workerNodeSelectorExpression,
			}
		} else {
			cdpAcross.NodeAffinity = corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: workerNodeSelectorExpression,
			}
		}
	} else {
		affinity := corev1.NodeAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
				{
					Weight: 100,
					Preference: corev1.NodeSelectorTerm{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{Key: "netperf", Operator: corev1.NodeSelectorOpIn, Values: []string{"client"}},
						},
					},
				},
			},
		}
		cdpAcross.NodeAffinity = affinity
		cdpHostAcross.NodeAffinity = affinity
	}

	if ncount > 1 {
		if s.HostNetwork {
			cdpHostAcross.NodeAffinity = corev1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: zoneNodeSelectorExpression(z, "client"),
				RequiredDuringSchedulingIgnoredDuringExecution:  workerNodeSelectorExpression,
			}
			cdpHostAcross.PodAntiAffinity = corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: clientRoleAffinity,
			}
			if !s.VM {
				s.ClientHost, err = deployDeployment(client, cdpHostAcross)
				if err != nil {
					return err
				}
			} else {
				err = launchClientVM(s, clientAcrossRole, &cdpAcross.PodAntiAffinity, &cdpHostAcross.NodeAffinity)
				if err != nil {
					return err
				}
			}
		}
		if !s.VM {
			s.ClientAcross, err = deployDeployment(client, cdpAcross)
			if err != nil {
				return err
			}
		} else {
			err = launchClientVM(s, clientAcrossRole, &cdpAcross.PodAntiAffinity, &cdpHostAcross.NodeAffinity)
			if err != nil {
				return err
			}
		}

	}

	// Use separate containers for servers
	dpCommands := [][]string{{"/bin/bash", "-c", "netserver && sleep 10000000"},
		{"/bin/bash", "-c", fmt.Sprintf("iperf3 -s -p %d && sleep 10000000", IperfServerCtlPort)},
		{"/bin/bash", "-c", fmt.Sprintf("uperf -s -v -P %d && sleep 10000000", UperfServerCtlPort)}}

	sdpHost := DeploymentParams{
		Name:        "server-host",
		Namespace:   "netperf",
		Replicas:    1,
		HostNetwork: true,
		Image:       k8sNetperfImage,
		Labels:      map[string]string{"role": hostNetServerRole},
		Commands:    dpCommands,
		Port:        NetperfServerCtlPort,
	}
	// Start netperf server
	sdp := DeploymentParams{
		Name:      "server",
		Namespace: "netperf",
		Replicas:  1,
		Image:     k8sNetperfImage,
		Labels:    map[string]string{"role": serverRole},
		Commands:  dpCommands,
		Port:      NetperfServerCtlPort,
	}
	if s.NodeLocal {
		sdp.PodAffinity = corev1.PodAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: clientRoleAffinity,
		}
	}
	if z != "" {
		var affinity corev1.NodeAffinity
		if numNodes > 1 {
			nodeZone := zoneNodeSelectorExpression(z, "server")
			if s.AcrossAZ {
				nodeZone = zoneNodeSelectorExpression(acrossZone, "server")
			}
			affinity = corev1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: nodeZone,
				RequiredDuringSchedulingIgnoredDuringExecution:  workerNodeSelectorExpression,
			}
		} else {
			affinity = corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: workerNodeSelectorExpression,
			}
		}
		sdp.NodeAffinity = affinity
		sdpHost.NodeAffinity = affinity
	} else {
		affinity := corev1.NodeAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
				{
					Weight: 100,
					Preference: corev1.NodeSelectorTerm{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{Key: "netperf", Operator: corev1.NodeSelectorOpIn, Values: []string{"server"}},
						},
					},
				},
			},
		}
		sdp.NodeAffinity = affinity
		sdpHost.NodeAffinity = affinity
	}
	if ncount > 1 {
		antiAffinity := corev1.PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{Key: "role", Operator: metav1.LabelSelectorOpIn, Values: []string{clientAcrossRole}},
						},
					},
					TopologyKey: "kubernetes.io/hostname",
				},
			},
		}
		sdp.PodAntiAffinity = antiAffinity
		antiAffinity = corev1.PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
				{
					LabelSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{Key: "role", Operator: metav1.LabelSelectorOpIn, Values: []string{hostNetClientRole}},
						},
					},
					TopologyKey: "kubernetes.io/hostname",
				},
			},
		}
		sdpHost.PodAntiAffinity = antiAffinity
	}
	if ncount > 1 {
		if s.HostNetwork {
			if !s.VM {
				s.ServerHost, err = deployDeployment(client, sdpHost)
				if err != nil {
					return err
				}
			} else {
				err = launchServerVM(s, serverRole, &sdp.PodAntiAffinity, &sdp.NodeAffinity)
				if err != nil {
					return err
				}
			}
		}
	}
	if !s.VM {
		s.Server, err = deployDeployment(client, sdp)
		if err != nil {
			return err
		}
		s.ServerNodeInfo, err = GetPodNodeInfo(client, labels.Set(sdp.Labels).String())
		if err != nil {
			return err
		}
		if !s.NodeLocal {
			s.ClientNodeInfo, err = GetPodNodeInfo(client, labels.Set(cdpAcross.Labels).String())
		}
		if err != nil {
			return err
		}
	} else {
		err = launchServerVM(s, serverRole, &sdp.PodAntiAffinity, &sdp.NodeAffinity)
		if err != nil {
			return err
		}
	}

	return nil
}

// Extract the UDN Ip address of a pod from the annotations - Support only ipv4
func ExtractUdnIp(s config.PerfScenarios) (string, error) {
	podNetworksJson := s.Server.Items[0].Annotations["k8s.ovn.org/pod-networks"]
	//
	var root map[string]json.RawMessage
	err := json.Unmarshal([]byte(podNetworksJson), &root)
	if err != nil {
		fmt.Println("Error unmarshalling JSON:", err)
		return "", err
	}
	//
	var udnData PodNetworksData
	err = json.Unmarshal(root["netperf/"+udnName], &udnData)
	if err != nil {
		return "", err
	}
	// Extract the IPv4 address
	var ipv4 net.IP
	for _, ip := range udnData.IPAddresses {
		if strings.Contains(ip, ".") { // Check if it's an IPv4 address
			ipv4, _, err = net.ParseCIDR(ip)
			if err != nil {
				return "", err
			}
		}
	}
	return ipv4.String(), nil
}

// launchServerVM will create the ServerVM with the specific node and pod affinity.
func launchServerVM(perf *config.PerfScenarios, name string, podAff *corev1.PodAntiAffinity, nodeAff *corev1.NodeAffinity) error {
	_, err := CreateVMServer(perf.KClient, serverRole, serverRole, *podAff, *nodeAff, perf.VMImage, perf.BridgeServerNetwork)
	if err != nil {
		return err
	}
	err = WaitForVMI(perf.KClient, serverRole)
	if err != nil {
		return err
	}
	if strings.Contains(name, "host") {
		perf.ServerHost, err = GetPods(perf.ClientSet, fmt.Sprintf("app=%s", serverRole))
		if err != nil {
			return err
		}
	} else {
		perf.Server, err = GetPods(perf.ClientSet, fmt.Sprintf("app=%s", serverRole))
		if err != nil {
			return err
		}
	}
	perf.ServerNodeInfo, _ = GetPodNodeInfo(perf.ClientSet, fmt.Sprintf("app=%s", serverRole))
	return nil
}

// launchClientVM will create the ClientVM with the specific node and pod affinity.
func launchClientVM(perf *config.PerfScenarios, name string, podAff *corev1.PodAntiAffinity, nodeAff *corev1.NodeAffinity) error {
	host, err := CreateVMClient(perf.KClient, perf.ClientSet, perf.DClient, name, podAff, nodeAff, perf.VMImage, perf.BridgeClientNetwork)
	if err != nil {
		return err
	}
	perf.VMHost = host
	err = WaitForVMI(perf.KClient, name)
	if err != nil {
		return err
	}
	if strings.Contains(name, "host") {
		perf.ClientHost, err = GetPods(perf.ClientSet, fmt.Sprintf("app=%s", name))
		if err != nil {
			return err
		}
	} else {
		perf.ClientAcross, err = GetPods(perf.ClientSet, fmt.Sprintf("app=%s", name))
		if err != nil {
			return err
		}
	}
	perf.ClientNodeInfo, _ = GetPodNodeInfo(perf.ClientSet, fmt.Sprintf("app=%s", name))
	return nil
}

func zoneNodeSelectorExpression(zone string, role string) []corev1.PreferredSchedulingTerm {
	return []corev1.PreferredSchedulingTerm{
		{
			Weight: 100,
			Preference: corev1.NodeSelectorTerm{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{Key: "topology.kubernetes.io/zone", Operator: corev1.NodeSelectorOpIn, Values: []string{zone}},
					{Key: "netperf", Operator: corev1.NodeSelectorOpIn, Values: []string{role}},
				},
			},
		},
	}
}

// deployDeployment Manages the creation and waits for the pods to become ready.
// returns a podList which is associated with the Deployment.
func deployDeployment(client *kubernetes.Clientset, dp DeploymentParams) (corev1.PodList, error) {
	pods := corev1.PodList{}
	_, err := CreateDeployment(dp, client)
	if err != nil {
		return pods, fmt.Errorf("😥 Unable to create deployment: %v", err)
	}
	_, err = WaitForReady(client, dp)
	if err != nil {
		return pods, err
	}
	// Retrieve pods which match the server/client role labels
	pods, err = GetPods(client, labels.Set(dp.Labels).String())
	if err != nil {
		return pods, err
	}
	return pods, nil
}

// WaitForReady accepts the client and deployment params to determine which pods to watch.
// It will return a bool based on if the pods ever become ready before we move on.
func WaitForReady(c *kubernetes.Clientset, dp DeploymentParams) (bool, error) {
	log.Infof("⏰ Checking for %s Pods to become ready...", dp.Name)
	dw, err := c.AppsV1().Deployments(dp.Namespace).Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	defer dw.Stop()
	for event := range dw.ResultChan() {
		d, ok := event.Object.(*appsv1.Deployment)
		if !ok {
			log.Error("❌ Issue with the Deployment")
		}
		if d.Name == dp.Name {
			if d.Status.ReadyReplicas == 1 {
				return true, nil
			}
		}
	}
	return false, fmt.Errorf("❌ Deployment had issues")
}

// WaitForDelete return nil if the namespace is deleted, error otherwise
func waitForNamespaceDelete(c *kubernetes.Clientset, nsName string) error {
	log.Infof("⏰ Waiting for %s Namespace to be deleted...", nsName)
	// Timeout in seconds
	ns, err := c.CoreV1().Namespaces().Watch(context.TODO(), metav1.ListOptions{FieldSelector: "metadata.name=" + nsName})
	if err != nil {
		return err
	}
	defer ns.Stop()
	for event := range ns.ResultChan() {
		if event.Type == watch.Deleted {
			return nil
		}
	}
	return fmt.Errorf("❌ Namespace delete issues: %v", err)
}

// GetZone will determine if we have a multiAZ/Zone cloud.
// returns string of the zone, int of the node count in that zone, error if encountered a problem.
func GetZone(c *kubernetes.Clientset) (string, map[string]int, error) {
	zones := map[string]int{}
	zone := ""
	lz := ""
	n, err := c.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker="})
	if err != nil {
		return "", zones, fmt.Errorf("unable to query nodes: %v", err)
	}
	for _, l := range n.Items {
		if len(l.GetLabels()["topology.kubernetes.io/zone"]) < 1 {
			return "", zones, fmt.Errorf("⚠️  No zone label")
		}
		if _, ok := zones[l.GetLabels()["topology.kubernetes.io/zone"]]; ok {
			zone = l.GetLabels()["topology.kubernetes.io/zone"]
			zones[zone]++
		} else {
			zones[l.GetLabels()["topology.kubernetes.io/zone"]] = 1
			lz = l.GetLabels()["topology.kubernetes.io/zone"]
		}
	}
	// No zone had > 1, use the last zone.
	if zone == "" {
		zone = lz
	}
	return zone, zones, nil
}

// CreateDeployment will create the different deployments we need to do network performance tests
func CreateDeployment(dp DeploymentParams, client *kubernetes.Clientset) (*appsv1.Deployment, error) {
	d, err := client.AppsV1().Deployments(dp.Namespace).Get(context.TODO(), dp.Name, metav1.GetOptions{})
	if err == nil {
		if d.Status.ReadyReplicas > 0 {
			log.Info("♻️  Using existing Deployment")
			return d, nil
		}
	}
	log.Infof("🚀 Starting Deployment for: %s in namespace: %s", dp.Name, dp.Namespace)
	dc := client.AppsV1().Deployments(dp.Namespace)

	// Add containers to deployment
	var cmdContainers []corev1.Container
	for i := 0; i < len(dp.Commands); i++ {
		// each container should have a unique name
		containerName := fmt.Sprintf("%s-%d", dp.Name, i)
		cmdContainers = append(cmdContainers,
			corev1.Container{
				Name:            containerName,
				Image:           dp.Image,
				Command:         dp.Commands[i],
				ImagePullPolicy: corev1.PullAlways,
			})
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: dp.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &dp.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: dp.Labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: dp.Labels,
					Annotations: map[string]string{
						"sidecar.istio.io/inject": "true",
					},
				},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: pointer.Int64(1),
					ServiceAccountName:            sa,
					HostNetwork:                   dp.HostNetwork,
					Containers:                    cmdContainers,
					Affinity: &corev1.Affinity{
						NodeAffinity:    &dp.NodeAffinity,
						PodAffinity:     &dp.PodAffinity,
						PodAntiAffinity: &dp.PodAntiAffinity,
					},
				},
			},
		},
	}
	return dc.Create(context.TODO(), deployment, metav1.CreateOptions{})
}

// GetPodNodeInfo collects the node information for a node running a pod with a specific label
func GetPodNodeInfo(c *kubernetes.Clientset, label string) (metrics.NodeInfo, error) {
	var info metrics.NodeInfo
	listOpt := metav1.ListOptions{
		LabelSelector: label,
		FieldSelector: "status.phase=Running",
	}
	pods, err := c.CoreV1().Pods(namespace).List(context.TODO(), listOpt)
	if err != nil {
		return info, fmt.Errorf("❌ Failure to capture pods: %v", err)
	}
	info.NodeName = pods.Items[0].Spec.NodeName
	info.IP = pods.Items[0].Status.HostIP
	node, err := c.CoreV1().Nodes().Get(context.TODO(), info.NodeName, metav1.GetOptions{})
	if err != nil {
		return info, err
	}
	info.NodeSystemInfo = node.Status.NodeInfo
	log.Debugf("Machine with label %s is Running on %s with IP %s", label, info.NodeName, info.IP)
	return info, nil
}

// GetPods returns pods with a specific label
func GetPods(c *kubernetes.Clientset, label string) (corev1.PodList, error) {
	listOpt := metav1.ListOptions{
		LabelSelector: label,
		FieldSelector: "status.phase=Running",
	}
	log.Infof("Looking for pods with label %s", fmt.Sprint(label))
	pods, err := c.CoreV1().Pods(namespace).List(context.TODO(), listOpt)
	if err != nil {
		return *pods, fmt.Errorf("❌ Failure to capture pods: %v", err)
	}
	return *pods, nil

}

// CreateService will build a k8s service
func CreateService(sp ServiceParams, client *kubernetes.Clientset) (*corev1.Service, error) {
	s, err := client.CoreV1().Services(sp.Namespace).Get(context.TODO(), sp.Name, metav1.GetOptions{})
	log.Debugf("Looking for service %s in namespace %s", sp.Name, sp.Namespace)
	if err == nil {
		log.Info("♻️  Using existing Service")
		return s, nil
	}
	log.Infof("🚀 Creating service for %s in namespace %s", sp.Name, sp.Namespace)
	sc := client.CoreV1().Services(sp.Namespace)
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sp.Name,
			Namespace: sp.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       fmt.Sprintf("%s-tcp-ctl", sp.Name),
					Protocol:   corev1.ProtocolTCP,
					TargetPort: intstr.Parse(fmt.Sprintf("%d", sp.CtlPort)),
					Port:       sp.CtlPort,
				},
				{
					Name:       fmt.Sprintf("%s-udp-ctl", sp.Name),
					Protocol:   corev1.ProtocolUDP,
					TargetPort: intstr.Parse(fmt.Sprintf("%d", sp.CtlPort)),
					Port:       sp.CtlPort,
				},
			},
			Type:     corev1.ServiceType("ClusterIP"),
			Selector: sp.Labels,
		},
	}
	for _, port := range sp.DataPorts {
		service.Spec.Ports = append(service.Spec.Ports,
			corev1.ServicePort{
				Name:       fmt.Sprintf("%s-tcp-%d", sp.Name, port),
				Protocol:   corev1.ProtocolTCP,
				TargetPort: intstr.Parse(fmt.Sprintf("%d", port)),
				Port:       port,
			},
			corev1.ServicePort{
				Name:       fmt.Sprintf("%s-udp-%d", sp.Name, port),
				Protocol:   corev1.ProtocolUDP,
				TargetPort: intstr.Parse(fmt.Sprintf("%d", port)),
				Port:       port,
			},
		)
	}
	return sc.Create(context.TODO(), service, metav1.CreateOptions{})
}

// GetServices retrieve all services for a given namespoace, in this case for netperf
func GetServices(client *kubernetes.Clientset, namespace string) (*corev1.ServiceList, error) {
	services, err := client.CoreV1().Services(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return services, nil
}

// GetDeployments retrieve all deployments for a given namespace, in this case for netperf
func GetDeployments(client *kubernetes.Clientset, namespace string) (*appsv1.DeploymentList, error) {
	dps, err := client.AppsV1().Deployments(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return dps, nil
}

// DestroyService cleans up a specific service from a namespace
func DestroyService(client *kubernetes.Clientset, serv corev1.Service) error {
	deletePolicy := metav1.DeletePropagationForeground
	return client.CoreV1().Services(serv.Namespace).Delete(context.TODO(), serv.Name, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	})
}

// DestroyNamespace cleans up the namespace k8s-netperf created
func DestroyNamespace(client *kubernetes.Clientset) error {
	_, err := client.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err == nil {
		log.Info("Cleaning resources created by k8s-netperf")
		err = client.CoreV1().Namespaces().Delete(context.TODO(), namespace, metav1.DeleteOptions{})
		if err != nil {
			return err
		}
		return waitForNamespaceDelete(client, namespace)
	}
	return nil
}

// DestroyDeployment cleans up a specific deployment from a namespace
func DestroyDeployment(client *kubernetes.Clientset, dp appsv1.Deployment) error {
	deletePolicy := metav1.DeletePropagationForeground
	gracePeriod := int64(0)
	return client.AppsV1().Deployments(dp.Namespace).Delete(context.TODO(), dp.Name, metav1.DeleteOptions{
		PropagationPolicy:  &deletePolicy,
		GracePeriodSeconds: &gracePeriod,
	})
}
