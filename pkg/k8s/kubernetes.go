package k8s

import (
	"context"
	"fmt"

	"github.com/jtaleric/k8s-netperf/pkg/config"
	log "github.com/jtaleric/k8s-netperf/pkg/logging"
	"github.com/jtaleric/k8s-netperf/pkg/metrics"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// DeploymentParams describes the deployment
type DeploymentParams struct {
	HostNetwork     bool
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

// ServiceParams describes the service specific details
type ServiceParams struct {
	Name      string
	Namespace string
	Labels    map[string]string
	CtlPort   int32
	DataPort  int32
}

const sa string = "netperf"

// ServerCtlPort control port for the service
const ServerCtlPort = 12865

// ServerDataPort data port for the service
const ServerDataPort = 42424

// Labels we will apply to k8s assets.
const serverRole = "server"
const clientRole = "client"
const clientAcrossRole = "client-across"
const hostNetServerRole = "host-server"
const hostNetClientRole = "host-client"

// BuildSUT Build the k8s env to run network performance tests
func BuildSUT(client *kubernetes.Clientset, s *config.PerfScenarios) error {
	// Check if nodes have the zone label to keep the netperf test
	// in the same AZ/Zone versus across AZ/Zone
	z, num_nodes, err := GetZone(client)
	if err != nil {
		log.Warn(err)
	}
	log.Infof("Deploying in %s zone", z)
	// Get node count
	nodes, err := client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker="})
	if err != nil {
		return err
	}
	ncount := len(nodes.Items)
	log.Debugf("Number of nodes with role worker: %d", ncount)

	zoneNodeSelectorExpression := []apiv1.PreferredSchedulingTerm{
		{
			Weight: 100,
			Preference: apiv1.NodeSelectorTerm{
				MatchExpressions: []apiv1.NodeSelectorRequirement{
					{Key: "topology.kubernetes.io/zone", Operator: apiv1.NodeSelectorOpIn, Values: []string{z}},
				},
			},
		},
	}
	if s.NodeLocal {
		//  Create Netperf client on the same node as the server.
		cdp := DeploymentParams{
			Name:      "client",
			Namespace: "netperf",
			Replicas:  1,
			Image:     "quay.io/jtaleric/netperf:beta",
			Labels:    map[string]string{"role": clientRole},
			Command:   []string{"/bin/bash", "-c", "sleep 10000000"},
			Port:      ServerCtlPort,
		}
		if z != "" {
			cdp.NodeAffinity = apiv1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: zoneNodeSelectorExpression,
			}
		}
		s.Client, err = deployDeployment(client, cdp)
		if err != nil {
			return err
		}
		s.ClientNodeInfo, _ = GetPodNodeInfo(client, cdp)
	}

	// Create netperf TCP service
	spTCP := ServiceParams{
		Name:      "netperf-service",
		Namespace: "netperf",
		Labels:    map[string]string{"role": serverRole},
		CtlPort:   ServerCtlPort,
		DataPort:  ServerDataPort,
	}
	s.Service, err = CreateService(spTCP, client)
	if err != nil {
		return fmt.Errorf("😥 Unable to create TCP netperf service")
	}
	cdpAcross := DeploymentParams{
		Name:      "client-across",
		Namespace: "netperf",
		Replicas:  1,
		Image:     "quay.io/jtaleric/netperf:beta",
		Labels:    map[string]string{"role": clientAcrossRole},
		Command:   []string{"/bin/bash", "-c", "sleep 10000000"},
		Port:      ServerCtlPort,
	}
	cdpHostAcross := DeploymentParams{
		Name:        "client-host",
		Namespace:   "netperf",
		Replicas:    1,
		HostNetwork: true,
		Image:       "quay.io/jtaleric/netperf:beta",
		Labels:      map[string]string{"role": hostNetClientRole},
		Command:     []string{"/bin/bash", "-c", "sleep 10000000"},
		Port:        ServerCtlPort,
	}
	workerNodeSelectorExpression := &apiv1.NodeSelector{
		NodeSelectorTerms: []apiv1.NodeSelectorTerm{
			{
				MatchExpressions: []apiv1.NodeSelectorRequirement{
					{Key: "node-role.kubernetes.io/worker", Operator: apiv1.NodeSelectorOpIn, Values: []string{""}},
				},
			},
		},
	}
	if z != "" {
		if num_nodes > 1 {
			cdpAcross.NodeAffinity = apiv1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: zoneNodeSelectorExpression,
				RequiredDuringSchedulingIgnoredDuringExecution: workerNodeSelectorExpression,
			}
		} else {
			cdpAcross.NodeAffinity = apiv1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: workerNodeSelectorExpression,
			}
		}
	}

	if ncount > 1 {
		if s.HostNetwork {
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
	sdpHost := DeploymentParams{
		Name:        "server-host",
		Namespace:   "netperf",
		Replicas:    1,
		HostNetwork: true,
		Image:       "quay.io/jtaleric/netperf:beta",
		Labels:      map[string]string{"role": hostNetServerRole},
		Command:     []string{"/bin/bash", "-c", fmt.Sprintf("netserver; sleep 10000000")},
		Port:        ServerCtlPort,
	}
	// Start netperf server
	sdp := DeploymentParams{
		Name:      "server",
		Namespace: "netperf",
		Replicas:  1,
		Image:     "quay.io/jtaleric/netperf:beta",
		Labels:    map[string]string{"role": serverRole},
		Command:   []string{"/bin/bash", "-c", fmt.Sprintf("netserver; sleep 10000000")},
		Port:      ServerCtlPort,
	}
	if z != "" {
		affinity := apiv1.NodeAffinity{}
		if num_nodes > 1 {
			affinity = apiv1.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: zoneNodeSelectorExpression,
				RequiredDuringSchedulingIgnoredDuringExecution: workerNodeSelectorExpression,
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
			PreferredDuringSchedulingIgnoredDuringExecution: []apiv1.WeightedPodAffinityTerm{
				{
					Weight: 100,
					PodAffinityTerm: apiv1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "role", Operator: metav1.LabelSelectorOpIn, Values: []string{clientAcrossRole}},
							},
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		}
		sdp.PodAntiAffinity = antiAffinity
		antiAffinity = apiv1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []apiv1.WeightedPodAffinityTerm{
				{
					Weight: 100,
					PodAffinityTerm: apiv1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "role", Operator: metav1.LabelSelectorOpIn, Values: []string{hostNetClientRole}},
							},
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		}
		sdpHost.PodAntiAffinity = antiAffinity
	}
	if s.HostNetwork {
		s.ServerHost, err = deployDeployment(client, sdpHost)
		if err != nil {
			return err
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
	log.Info("⏰ Checking for Pods to become ready...")
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

// GetZone will determine if we have a multiAZ/Zone cloud.
func GetZone(c *kubernetes.Clientset) (string, int, error) {
	zones := map[string]int{}
	zone := ""
	lz := ""
	num_nodes := 0
	n, err := c.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker="})
	if err != nil {
		return "", num_nodes, fmt.Errorf("Unable to query nodes")
	}
	for _, l := range n.Items {
		if len(l.GetLabels()["topology.kubernetes.io/zone"]) < 1 {
			return "", num_nodes, fmt.Errorf("⚠️  No zone label")
		}
		if _, ok := zones[l.GetLabels()["topology.kubernetes.io/zone"]]; ok {
			zone = l.GetLabels()["topology.kubernetes.io/zone"]
			num_nodes = 2
			// Simple check, no need to determine all the zones with > 1 node.
			break
		} else {
			zones[l.GetLabels()["topology.kubernetes.io/zone"]] = 1
			lz = l.GetLabels()["topology.kubernetes.io/zone"]
		}
	}
	// No zone had > 1, use the last zone.
	if zone == "" {
		log.Warn("⚠️  Single node per zone")
		num_nodes = 1
		zone = lz
	}
	return zone, num_nodes, nil
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
				},
				Spec: apiv1.PodSpec{
					ServiceAccountName: sa,
					HostNetwork:        dp.HostNetwork,
					Containers: []apiv1.Container{
						{
							Name:    dp.Name,
							Image:   dp.Image,
							Command: dp.Command,
						},
					},
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
					Name:       fmt.Sprintf("%s-ctl", sp.Name),
					Protocol:   apiv1.ProtocolTCP,
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

// DestroyDeployment cleans up a specific deployment
func DestroyDeployment(client *kubernetes.Clientset, dp *appsv1.Deployment) error {
	deletePolicy := metav1.DeletePropagationForeground
	return client.AppsV1().Deployments(dp.Namespace).Delete(context.TODO(), dp.Name, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	})
}
