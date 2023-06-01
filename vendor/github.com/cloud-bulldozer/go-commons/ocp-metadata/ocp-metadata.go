// Copyright 2022 The go-commons Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ocpmetadata

import (
	"context"
	"encoding/json"
	"fmt"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/pointer"
)

// Metadata object
type Metadata struct {
	clientSet     *kubernetes.Clientset
	dynamicClient dynamic.Interface
}

// NewMetadata instantiates a new OCP metadata discovery agent
func NewMetadata(restConfig *rest.Config) (Metadata, error) {
	cs, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return Metadata{}, err
	}
	dc, err := dynamic.NewForConfig(restConfig)
	return Metadata{
		clientSet:     cs,
		dynamicClient: dc,
	}, err
}

// GetClusterMetadata returns a clusterMetadata object from the given OCP cluster
func (meta *Metadata) GetClusterMetadata() (ClusterMetadata, error) {
	metadata := ClusterMetadata{MetricName: metadataMetricName}
	infra, err := meta.getInfraDetails()
	if err != nil {
		return metadata, nil
	}
	metadata.ClusterName, metadata.Platform, metadata.Region = infra.Status.InfrastructureName, infra.Status.Platform, infra.Status.PlatformStatus.Aws.Region
	for _, v := range infra.Status.PlatformStatus.Aws.ResourceTags {
		if v.Key == "red-hat-clustertype" {
			metadata.Platform = v.Value
		}
	}
	metadata.SDNType, err = meta.getSDNInfo()
	if err != nil {
		return metadata, err
	}
	version, err := meta.getVersionInfo()
	if err != nil {
		return metadata, err
	}
	metadata.OCPVersion, metadata.K8SVersion = version.ocpVersion, version.k8sVersion
	nodeInfo, err := meta.getNodesInfo()
	if err != nil {
		return metadata, err
	}
	metadata.TotalNodes = nodeInfo.totalNodes
	metadata.MasterNodesCount = nodeInfo.masterCount
	metadata.WorkerNodesCount = nodeInfo.workerCount
	metadata.InfraNodesCount = nodeInfo.infraCount
	metadata.MasterNodesType = nodeInfo.masterType
	metadata.WorkerNodesType = nodeInfo.workerType
	metadata.InfraNodesType = nodeInfo.infraType
	return metadata, err
}

// GetPrometheus Returns Prometheus URL and a valid Bearer token
func (meta *Metadata) GetPrometheus() (string, string, error) {
	prometheusURL, err := getPrometheusURL(meta.dynamicClient)
	if err != nil {
		return prometheusURL, "", err
	}
	prometheusToken, err := getBearerToken(meta.clientSet)
	return prometheusURL, prometheusToken, err
}

// GetCurrentPodCount returns the number of current running pods across all worker nodes
func (meta *Metadata) GetCurrentPodCount() (int, error) {
	var podCount int
	nodeList, err := meta.clientSet.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: workerNodeSelector})
	if err != nil {
		return podCount, err
	}
	for _, node := range nodeList.Items {
		podList, err := meta.clientSet.CoreV1().Pods(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{FieldSelector: "status.phase=Running,spec.nodeName=" + node.Name})
		if err != nil {
			return podCount, err
		}
		podCount += len(podList.Items)
	}
	return podCount, nil
}

// GetDefaultIngressDomain returns default ingress domain of the default ingress controller
func (meta *Metadata) GetDefaultIngressDomain() (string, error) {
	ingressController, err := meta.dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "operator.openshift.io",
		Version:  "v1",
		Resource: "ingresscontrollers",
	}).Namespace("openshift-ingress-operator").Get(context.TODO(), "default", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	ingressDomain, found, err := unstructured.NestedString(ingressController.UnstructuredContent(), "status", "domain")
	if !found {
		return "", fmt.Errorf("domain field not found in operator.openshift.io/v1/namespaces/openshift-ingress-operator/ingresscontrollers/default status")
	}
	return ingressDomain, err
}

// getPrometheusURL Returns a valid prometheus endpoint from the openshift-monitoring/prometheus-k8s route
func getPrometheusURL(dynamicClient dynamic.Interface) (string, error) {
	route, err := dynamicClient.Resource(schema.GroupVersionResource{
		Group:    routeGroup,
		Version:  routeVersion,
		Resource: routeResource,
	}).Namespace(monitoringNs).Get(context.TODO(), "prometheus-k8s", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	prometheusHost, found, err := unstructured.NestedString(route.UnstructuredContent(), "spec", "host")
	if !found {
		return "", fmt.Errorf("host field not found in %s/prometheus-k8s route spec", monitoringNs)
	}
	if err != nil {
		return "", err
	}
	return "https://" + prometheusHost, nil
}

// getBearerToken returns a valid bearer token from the openshift-monitoring/prometheus-k8s service account
func getBearerToken(clientset *kubernetes.Clientset) (string, error) {
	request := authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: pointer.Int64(int64(tokenExpiration.Seconds())),
		},
	}
	response, err := clientset.CoreV1().ServiceAccounts(monitoringNs).CreateToken(context.TODO(), "prometheus-k8s", &request, metav1.CreateOptions{})
	return response.Status.Token, err
}

// getInfraDetails returns cluster name and platform
func (meta *Metadata) getInfraDetails() (infraObj, error) {
	var infraJSON infraObj
	infra, err := meta.dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "infrastructures",
	}).Get(context.TODO(), "cluster", metav1.GetOptions{})
	if err != nil {
		return infraJSON, err
	}
	infraData, _ := infra.MarshalJSON()
	err = json.Unmarshal(infraData, &infraJSON)
	return infraJSON, err
}

// getVersionInfo obtains OCP and k8s version information
func (meta *Metadata) getVersionInfo() (versionObj, error) {
	var cv clusterVersion
	var versionInfo versionObj
	version, err := meta.clientSet.ServerVersion()
	if err != nil {
		return versionInfo, err
	}
	versionInfo.k8sVersion = version.GitVersion
	clusterVersion, err := meta.dynamicClient.Resource(
		schema.GroupVersionResource{
			Group:    "config.openshift.io",
			Version:  "v1",
			Resource: "clusterversions",
		}).Get(context.TODO(), "version", metav1.GetOptions{})
	if err != nil {
		return versionInfo, err
	}
	clusterVersionBytes, _ := clusterVersion.MarshalJSON()
	err = json.Unmarshal(clusterVersionBytes, &cv)
	if err != nil {
		return versionInfo, err
	}
	for _, update := range cv.Status.History {
		if update.State == completedUpdate {
			// obtain the version from the last completed update
			versionInfo.ocpVersion = update.Version
			break
		}
	}
	return versionInfo, err
}

// getNodesInfo returns node information
func (meta *Metadata) getNodesInfo() (nodeInfo, error) {
	var nodeInfoData nodeInfo
	nodes, err := meta.clientSet.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nodeInfoData, err
	}
	nodeInfoData.totalNodes = len(nodes.Items)
	for _, node := range nodes.Items {
		for k := range node.Labels {
			switch k {
			case "node-role.kubernetes.io/master":
				nodeInfoData.masterCount++
				nodeInfoData.masterType = node.Labels["node.kubernetes.io/instance-type"]
			case "node-role.kubernetes.io/worker":
				// Discard nodes with infra or workload label
				for k := range node.Labels {
					if k != "node-role.kubernetes.io/infra" && k != "node-role.kubernetes.io/workload" {
						nodeInfoData.workerCount++
						nodeInfoData.workerType = node.Labels["node.kubernetes.io/instance-type"]
						break
					}
				}
			case "node-role.kubernetes.io/infra":
				nodeInfoData.infraCount++
				nodeInfoData.infraType = node.Labels["node.kubernetes.io/instance-type"]
			}
		}
	}
	return nodeInfoData, err
}

// getSDNInfo returns SDN type
func (meta *Metadata) getSDNInfo() (string, error) {
	networkData, err := meta.dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "networks",
	}).Get(context.TODO(), "cluster", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	networkType, found, err := unstructured.NestedString(networkData.UnstructuredContent(), "status", "networkType")
	if !found {
		return "", fmt.Errorf("networkType field not found in config.openshift.io/v1/network/networks/cluster status")
	}
	return networkType, err
}
