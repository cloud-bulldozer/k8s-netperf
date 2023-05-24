// Copyright 2022 The Kube-burner Authors.
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

import "time"

const (
	routeGroup         = "route.openshift.io"
	routeVersion       = "v1"
	routeResource      = "routes"
	completedUpdate    = "Completed"
	workerNodeSelector = "node-role.kubernetes.io/worker=,node-role.kubernetes.io/infra!=,node-role.kubernetes.io/workload!="
	monitoringNs       = "openshift-monitoring"
	metadataMetricName = "clusterMetadata"
	tokenExpiration    = 10 * time.Hour
)

// infraObj
// TODO at the moment can be used to decode some AWS platform specific information from the infrastructure object
// like region and resourceTags (which is actually used to detect if this is a ROSA cluster)
// similar information is not found for other platforms like GCP or Azure.
// To collect such information we shall use a different approach, i.e:
// Using well known node labels like topology.kubernetes.io/region to get the cloud region
type infraObj struct {
	Status struct {
		InfrastructureName string `json:"infrastructureName"`
		Platform           string `json:"platform"`
		Type               string `json:"type"`
		PlatformStatus     struct {
			Aws struct {
				Region       string `json:"region"`
				ResourceTags []struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"resourceTags"`
			} `json:"aws"`
			Type string `json:"type"`
		} `json:"platformStatus"`
	} `json:"status"`
}

type versionObj struct {
	ocpVersion string
	k8sVersion string
}

type clusterVersion struct {
	Status struct {
		History []struct {
			State   string `json:"state"`
			Version string `json:"version"`
		} `json:"history"`
	} `json:"status"`
}

type nodeInfo struct {
	workerCount int
	infraCount  int
	masterCount int
	totalNodes  int
	masterType  string
	workerType  string
	infraType   string
}

type ClusterMetadata struct {
	MetricName       string `json:"metricName,omitempty"`
	Platform         string `json:"platform"`
	OCPVersion       string `json:"ocpVersion"`
	K8SVersion       string `json:"k8sVersion"`
	MasterNodesType  string `json:"masterNodesType"`
	WorkerNodesType  string `json:"workerNodesType"`
	MasterNodesCount int    `json:"masterNodesCount"`
	InfraNodesType   string `json:"infraNodesType"`
	WorkerNodesCount int    `json:"workerNodesCount"`
	InfraNodesCount  int    `json:"infraNodesCount"`
	TotalNodes       int    `json:"totalNodes"`
	SDNType          string `json:"sdnType"`
	ClusterName      string `json:"clusterName"`
	Region           string `json:"region"`
}
