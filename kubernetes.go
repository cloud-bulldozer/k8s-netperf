package main

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// WaitForReady accepts the client and deployment params to determine which pods to watch.
// It will return a bool based on if the pods ever become ready before we move on.
func WaitForReady(c *kubernetes.Clientset, dp DeploymentParams) (bool, error) {
	fmt.Println("⏰ Checking for Pods to become ready...")
	dw, err := c.AppsV1().Deployments(dp.namespace).Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	defer dw.Stop()
	for event := range dw.ResultChan() {
		d, ok := event.Object.(*appsv1.Deployment)
		if !ok {
			fmt.Println("❌ Issue with the Deployment")
		}
		if d.Name == dp.name {
			if d.Status.ReadyReplicas == 1 {
				return true, nil
			}
		}
	}
	return false, fmt.Errorf("❌ Deployment had issues")
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
	d, err := client.AppsV1().Deployments(dp.namespace).Get(context.TODO(), dp.name, metav1.GetOptions{})
	if err == nil {
		if d.Status.ReadyReplicas > 0 {
			fmt.Println("♻️  Using existing Deployment")
			return d, nil
		}
	}
	fmt.Printf("🚀 Starting Deployment for %s in %s\n", dp.name, dp.namespace)
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
	pods, err := c.CoreV1().Pods(dp.namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: selector.String(), FieldSelector: "status.phase=Running"})
	if err != nil {
		return nil, fmt.Errorf("❌ Failure to capture pods")
	}
	return pods, nil
}

func CreateService(sp ServiceParams, client *kubernetes.Clientset) (*apiv1.Service, error) {
	s, err := client.CoreV1().Services(sp.namespace).Get(context.TODO(), sp.name, metav1.GetOptions{})
	if err == nil {
		fmt.Println("♻️  Using existing Deployment")
		return s, nil
	}
	fmt.Printf("🚀 Creating service for %s in %s\n", sp.name, sp.namespace)
	sc := client.CoreV1().Services(sp.namespace)
	service := &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sp.name,
			Namespace: sp.namespace,
		},
		Spec: apiv1.ServiceSpec{
			Ports: []apiv1.ServicePort{
				{
					Name:       fmt.Sprintf("%s-ctl", sp.name),
					Protocol:   apiv1.ProtocolTCP,
					TargetPort: intstr.Parse(fmt.Sprintf("%d", sp.ctlPort)),
					Port:       sp.ctlPort,
				},
				{
					Name:       fmt.Sprintf("%s-data-tcp", sp.name),
					Protocol:   apiv1.ProtocolTCP,
					TargetPort: intstr.Parse(fmt.Sprintf("%d", sp.dataPort)),
					Port:       sp.dataPort,
				},
				{
					Name:       fmt.Sprintf("%s-data-udp", sp.name),
					Protocol:   apiv1.ProtocolUDP,
					TargetPort: intstr.Parse(fmt.Sprintf("%d", sp.dataPort)),
					Port:       sp.dataPort,
				},
			},
			Type:     apiv1.ServiceType("ClusterIP"),
			Selector: sp.labels,
		},
	}
	return sc.Create(context.TODO(), service, metav1.CreateOptions{})
}
