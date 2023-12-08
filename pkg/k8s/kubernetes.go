package k8s

import (
	"context"
	"fmt"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	log "github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/metrics"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
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
	PodAffinity     apiv1.PodAffinity
	PodAntiAffinity apiv1.PodAntiAffinity
	NodeAffinity    apiv1.NodeAffinity
	Port            int
}

// ServiceParams describes the service specific details
type ServiceParams struct {
	Name      string
	Namespace string
	Labels    map[string]string
	CtlPort   int32
	DataPort  int32
}

const sa string = "netperf"

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

// BuildSUT Build the k8s env to run network performance tests
func BuildSUT(client *kubernetes.Clientset, s *config.PerfScenarios) error {
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
		return fmt.Errorf(" unable to run AcrossAZ since there is < 2 zones")
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
	nodes, err := client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker="})
	if err != nil {
		return err
	}
	ncount := 0
	for _, node := range nodes.Items {
		if _, ok := node.Labels["node-role.kubernetes.io/infra"]; !ok {
			ncount++
		}
	}
	log.Debugf("Number of nodes with role worker: %d", ncount)
	if (s.HostNetwork || !s.NodeLocal) && ncount < 2 {
		return fmt.Errorf(" not enough nodes with label worker= to execute test (current number of nodes: %d).", ncount)
	}

	// Schedule pods to nodes with role worker=, but not nodes with infra= and workload=
	workerNodeSelectorExpression := &apiv1.NodeSelector{
		NodeSelectorTerms: []apiv1.NodeSelectorTerm{
			{
				MatchExpressions: []apiv1.NodeSelectorRequirement{
					{Key: "node-role.kubernetes.io/worker", Operator: apiv1.NodeSelectorOpIn, Values: []string{""}},
					{Key: "node-role.kubernetes.io/infra", Operator: apiv1.NodeSelectorOpNotIn, Values: []string{""}},
					{Key: "node-role.kubernetes.io/workload", Operator: apiv1.NodeSelectorOpNotIn, Values: []string{""}},
				},
			},
		},
	}

	clientRoleAffinity := []apiv1.PodAffinityTerm{
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
			Image:     "quay.io/cloud-bulldozer/netperf:latest",
			Labels:    map[string]string{"role": clientRole},
			Commands:  [][]string{{"/bin/bash", "-c", "sleep 10000000"}},
			Port:      NetperfServerCtlPort,
		}
		if z != "" && numNodes > 1 {
			cdp.NodeAffinity = apiv1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: zoneNodeSelectorExpression(z),
			}
		}

		cdp.NodeAffinity = apiv1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: workerNodeSelectorExpression,
		}
		s.Client, err = deployDeployment(client, cdp)
		if err != nil {
			return err
		}
		s.ClientNodeInfo, _ = GetPodNodeInfo(client, cdp)
	}

	// Create iperf service
	iperfSVC := ServiceParams{
		Name:      "iperf-service",
		Namespace: "netperf",
		Labels:    map[string]string{"role": serverRole},
		CtlPort:   IperfServerCtlPort,
		DataPort:  IperfServerDataPort,
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
		DataPort:  UperfServerDataPort,
	}
	s.UperfService, err = CreateService(uperfSVC, client)
	if err != nil {
		return fmt.Errorf("😥 Unable to create uperf service")
	}

	// Create netperf service
	netperfSVC := ServiceParams{
		Name:      "netperf-service",
		Namespace: "netperf",
		Labels:    map[string]string{"role": serverRole},
		CtlPort:   NetperfServerCtlPort,
		DataPort:  NetperfServerDataPort,
	}
	s.NetperfService, err = CreateService(netperfSVC, client)
	if err != nil {
		return fmt.Errorf("😥 Unable to create netperf service")
	}
	cdpAcross := DeploymentParams{
		Name:      "client-across",
		Namespace: "netperf",
		Replicas:  1,
		Image:     "quay.io/cloud-bulldozer/netperf:latest",
		Labels:    map[string]string{"role": clientAcrossRole},
		Commands:  [][]string{{"/bin/bash", "-c", "sleep 10000000"}},
		Port:      NetperfServerCtlPort,
	}
	cdpAcross.PodAntiAffinity = apiv1.PodAntiAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: clientRoleAffinity,
	}

	cdpHostAcross := DeploymentParams{
		Name:        "client-host",
		Namespace:   "netperf",
		Replicas:    1,
		HostNetwork: true,
		Image:       "quay.io/cloud-bulldozer/netperf:latest",
		Labels:      map[string]string{"role": hostNetClientRole},
		Commands:    [][]string{{"/bin/bash", "-c", "sleep 10000000"}},
		Port:        NetperfServerCtlPort,
	}
	if z != "" {
		if numNodes > 1 {
			cdpAcross.NodeAffinity = apiv1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: zoneNodeSelectorExpression(z),
				RequiredDuringSchedulingIgnoredDuringExecution:  workerNodeSelectorExpression,
			}
		} else {
			cdpAcross.NodeAffinity = apiv1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: workerNodeSelectorExpression,
			}
		}
	}

	if ncount > 1 {
		if s.HostNetwork {
			cdpHostAcross.NodeAffinity = apiv1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: zoneNodeSelectorExpression(z),
				RequiredDuringSchedulingIgnoredDuringExecution:  workerNodeSelectorExpression,
			}
			cdpHostAcross.PodAntiAffinity = apiv1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: clientRoleAffinity,
			}
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

	// Use separate containers for servers
	dpCommands := [][]string{{"/bin/bash", "-c", "netserver && sleep 10000000"},
		{"/bin/bash", "-c", fmt.Sprintf("iperf3 -s -p %d && sleep 10000000", IperfServerCtlPort)},
		{"/bin/bash", "-c", fmt.Sprintf("uperf -s -v -P %d && sleep 10000000", UperfServerCtlPort)}}

	sdpHost := DeploymentParams{
		Name:        "server-host",
		Namespace:   "netperf",
		Replicas:    1,
		HostNetwork: true,
		Image:       "quay.io/cloud-bulldozer/netperf:latest",
		Labels:      map[string]string{"role": hostNetServerRole},
		Commands:    dpCommands,
		Port:        NetperfServerCtlPort,
	}
	// Start netperf server
	sdp := DeploymentParams{
		Name:      "server",
		Namespace: "netperf",
		Replicas:  1,
		Image:     "quay.io/cloud-bulldozer/netperf:latest",
		Labels:    map[string]string{"role": serverRole},
		Commands:  dpCommands,
		Port:      NetperfServerCtlPort,
	}
	if s.NodeLocal {
		sdp.PodAffinity = apiv1.PodAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: clientRoleAffinity,
		}
	}
	if z != "" {
		var affinity apiv1.NodeAffinity
		if numNodes > 1 {
			nodeZone := zoneNodeSelectorExpression(z)
			if s.AcrossAZ {
				nodeZone = zoneNodeSelectorExpression(acrossZone)
			}
			affinity = apiv1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: nodeZone,
				RequiredDuringSchedulingIgnoredDuringExecution:  workerNodeSelectorExpression,
			}
		} else {
			affinity = apiv1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: workerNodeSelectorExpression,
			}
		}
		sdp.NodeAffinity = affinity
		sdpHost.NodeAffinity = affinity
	}
	if ncount > 1 {
		antiAffinity := apiv1.PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []apiv1.PodAffinityTerm{
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
		antiAffinity = apiv1.PodAntiAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []apiv1.PodAffinityTerm{
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
			s.ServerHost, err = deployDeployment(client, sdpHost)
			if err != nil {
				return err
			}
		}
	}
	s.Server, err = deployDeployment(client, sdp)

	s.ServerNodeInfo, _ = GetPodNodeInfo(client, sdp)
	if !s.NodeLocal {
		s.ClientNodeInfo, _ = GetPodNodeInfo(client, cdpAcross)
	}
	if err != nil {
		return err
	}
	return nil
}

func zoneNodeSelectorExpression(zone string) []apiv1.PreferredSchedulingTerm {
	return []apiv1.PreferredSchedulingTerm{
		{
			Weight: 100,
			Preference: apiv1.NodeSelectorTerm{
				MatchExpressions: []apiv1.NodeSelectorRequirement{
					{Key: "topology.kubernetes.io/zone", Operator: apiv1.NodeSelectorOpIn, Values: []string{zone}},
				},
			},
		},
	}
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
			fmt.Println("❌ Issue with the Deployment")
		}
		if d.Name == dp.Name {
			if d.Status.ReadyReplicas == 1 {
				return true, nil
			}
		}
	}
	return false, fmt.Errorf("❌ Deployment had issues")
}

// WaitForDelete return true if the deployment is deleted, false otherwise. Error if it goes bad.
func WaitForDelete(c *kubernetes.Clientset, dp appsv1.Deployment) (bool, error) {
	log.Infof("⏰ Waiting for %s Deployment to deleted...", dp.Name)
	// Timeout in seconds
	timeout := int64(300)
	dw, err := c.AppsV1().Deployments(dp.Namespace).Watch(context.TODO(), metav1.ListOptions{TimeoutSeconds: &timeout})
	if err != nil {
		return false, err
	}
	defer dw.Stop()
	for event := range dw.ResultChan() {
		if event.Type == watch.Deleted {
			return true, nil
		}
	}
	return false, fmt.Errorf("❌ Deployment delete issues")
}

// GetZone will determine if we have a multiAZ/Zone cloud.
// returns string of the zone, int of the node count in that zone, error if encountered a problem.
func GetZone(c *kubernetes.Clientset) (string, map[string]int, error) {
	zones := map[string]int{}
	zone := ""
	lz := ""
	n, err := c.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker="})
	if err != nil {
		return "", zones, fmt.Errorf("unable to query nodes")
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
	var cmdContainers []apiv1.Container
	for i := 0; i < len(dp.Commands); i++ {
		// each container should have a unique name
		containerName := fmt.Sprintf("%s-%d", dp.Name, i)
		cmdContainers = append(cmdContainers,
			apiv1.Container{
				Name:            containerName,
				Image:           dp.Image,
				Command:         dp.Commands[i],
				ImagePullPolicy: apiv1.PullAlways,
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
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: dp.Labels,
					Annotations: map[string]string{
						"sidecar.istio.io/inject": "true",
					},
				},
				Spec: apiv1.PodSpec{
					ServiceAccountName: sa,
					HostNetwork:        dp.HostNetwork,
					Containers:         cmdContainers,
					Affinity: &apiv1.Affinity{
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

// GetNodeLabels Return Labels for a specific node
func GetNodeLabels(c *kubernetes.Clientset, node string) (map[string]string, error) {
	nodeInfo, err := c.CoreV1().Nodes().Get(context.TODO(), node, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return nodeInfo.GetLabels(), nil
}

// GetPodNodeInfo collects the node information for a specific pod
func GetPodNodeInfo(c *kubernetes.Clientset, dp DeploymentParams) (metrics.NodeInfo, error) {
	var info metrics.NodeInfo
	d, err := c.AppsV1().Deployments(dp.Namespace).Get(context.TODO(), dp.Name, metav1.GetOptions{})
	if err != nil {
		return info, fmt.Errorf("❌ Failure to capture deployment")
	}
	selector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return info, fmt.Errorf("❌ Failure to capture deployment label")
	}
	pods, err := c.CoreV1().Pods(dp.Namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: selector.String(), FieldSelector: "status.phase=Running"})
	if err != nil {
		return info, fmt.Errorf("❌ Failure to capture pods")
	}
	for pod := range pods.Items {
		p := pods.Items[pod]
		if pods.Items[pod].DeletionTimestamp != nil {
			continue
		} else {
			info.IP = p.Status.HostIP
			info.Hostname = p.Spec.NodeName
		}
	}
	log.Debugf("%s Running on %s with IP %s", d.Name, info.Hostname, info.IP)
	return info, nil
}

// GetPods searches for a specific set of pods from DeploymentParms
// It returns a PodList if the deployment is found.
// NOTE : Since we can update the replicas to be > 1, is why I return a PodList.
func GetPods(c *kubernetes.Clientset, dp DeploymentParams) (apiv1.PodList, error) {
	d, err := c.AppsV1().Deployments(dp.Namespace).Get(context.TODO(), dp.Name, metav1.GetOptions{})
	npl := apiv1.PodList{}
	if err != nil {
		return npl, fmt.Errorf("❌ Failure to capture deployment")
	}
	selector, err := metav1.LabelSelectorAsSelector(d.Spec.Selector)
	if err != nil {
		return npl, fmt.Errorf("❌ Failure to capture deployment label")
	}
	pods, err := c.CoreV1().Pods(dp.Namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: selector.String(), FieldSelector: "status.phase=Running"})
	if err != nil {
		return npl, fmt.Errorf("❌ Failure to capture pods")
	}
	for pod := range pods.Items {
		if pods.Items[pod].DeletionTimestamp != nil {
			continue
		} else {
			npl.Items = append(npl.Items, pods.Items[pod])
		}
	}
	return npl, nil
}

// CreateService will build a k8s service
func CreateService(sp ServiceParams, client *kubernetes.Clientset) (*apiv1.Service, error) {
	s, err := client.CoreV1().Services(sp.Namespace).Get(context.TODO(), sp.Name, metav1.GetOptions{})
	log.Debugf("Looking for service %s in namespace %s", sp.Name, sp.Namespace)
	if err == nil {
		log.Info("♻️  Using existing Service")
		return s, nil
	}
	log.Infof("🚀 Creating service for %s in namespace %s", sp.Name, sp.Namespace)
	sc := client.CoreV1().Services(sp.Namespace)
	service := &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sp.Name,
			Namespace: sp.Namespace,
		},
		Spec: apiv1.ServiceSpec{
			Ports: []apiv1.ServicePort{
				{
					Name:       fmt.Sprintf("%s-tcp-ctl", sp.Name),
					Protocol:   apiv1.ProtocolTCP,
					TargetPort: intstr.Parse(fmt.Sprintf("%d", sp.CtlPort)),
					Port:       sp.CtlPort,
				},
				{
					Name:       fmt.Sprintf("%s-udp-ctl", sp.Name),
					Protocol:   apiv1.ProtocolUDP,
					TargetPort: intstr.Parse(fmt.Sprintf("%d", sp.CtlPort)),
					Port:       sp.CtlPort,
				},
				{
					Name:       fmt.Sprintf("%s-data-tcp", sp.Name),
					Protocol:   apiv1.ProtocolTCP,
					TargetPort: intstr.Parse(fmt.Sprintf("%d", sp.DataPort)),
					Port:       sp.DataPort,
				},
				{
					Name:       fmt.Sprintf("%s-data-udp", sp.Name),
					Protocol:   apiv1.ProtocolUDP,
					TargetPort: intstr.Parse(fmt.Sprintf("%d", sp.DataPort)),
					Port:       sp.DataPort,
				},
			},
			Type:     apiv1.ServiceType("ClusterIP"),
			Selector: sp.Labels,
		},
	}
	return sc.Create(context.TODO(), service, metav1.CreateOptions{})
}

// GetServices retrieve all services for a given namespoace, in this case for netperf
func GetServices(client *kubernetes.Clientset, namespace string) (*apiv1.ServiceList, error) {
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
func DestroyService(client *kubernetes.Clientset, serv apiv1.Service) error {
	deletePolicy := metav1.DeletePropagationForeground
	return client.CoreV1().Services(serv.Namespace).Delete(context.TODO(), serv.Name, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	})
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
