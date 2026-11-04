package k8s

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	log "github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/metrics"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// OwnerTokenAnnotation marks the netperf namespace with the invocation that created it.
const OwnerTokenAnnotation = "k8s-netperf.cloud-bulldozer.io/owner-token"

// CrossClusterServiceNames lists every pod Service createServices can create, for <name>.netperf.svc.<domain>.
var CrossClusterServiceNames = []string{"netperf-service", "iperf-service", "uperf-service", "uperf-histogram-service"}

// NetperfServiceDataPorts is how many data ports (42424 up) netperf-service exposes: the most netperf streams it carries.
const NetperfServiceDataPorts = 16

// ErrNamespaceNotOwned is returned when the netperf namespace exists but was not created by this run.
var ErrNamespaceNotOwned = errors.New("namespace was not created by this run")

// Bounds for the cross-cluster path. Variables so tests can shorten them.
var (
	crossClusterAPITimeout   = 2 * time.Minute
	crossClusterReadyTimeout = 10 * time.Minute
	crossClusterPollInterval = 2 * time.Second
)

// IsPermanentAPIError reports API errors that retrying cannot fix.
func IsPermanentAPIError(err error) bool {
	return apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) || apierrors.IsBadRequest(err) ||
		apierrors.IsInvalid(err) || apierrors.IsMethodNotSupported(err) || apierrors.IsNotAcceptable(err) ||
		apierrors.IsUnsupportedMediaType(err)
}

func withAPITimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, crossClusterAPITimeout)
}

// sleepCtx waits for d and reports false if ctx ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// CountWorkerNodes returns the number of nodes labeled node-role.kubernetes.io/worker=.
func CountWorkerNodes(ctx context.Context, c kubernetes.Interface) (int, error) {
	callCtx, cancel := withAPITimeout(ctx)
	defer cancel()
	nodes, err := c.CoreV1().Nodes().List(callCtx, metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/worker="})
	if err != nil {
		return 0, fmt.Errorf("listing worker nodes: %w", err)
	}
	return len(nodes.Items), nil
}

// NamespaceExists reports whether the netperf namespace exists. Errors other than NotFound are returned.
func NamespaceExists(ctx context.Context, c kubernetes.Interface) (bool, error) {
	callCtx, cancel := withAPITimeout(ctx)
	defer cancel()
	_, err := c.CoreV1().Namespaces().Get(callCtx, namespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking namespace %s: %w", namespace, err)
	}
	return true, nil
}

// getNamespace returns nil if the namespace does not exist; transient errors are retried until ctx ends.
func getNamespace(ctx context.Context, c kubernetes.Interface) (*corev1.Namespace, error) {
	for {
		callCtx, cancel := withAPITimeout(ctx)
		ns, err := c.CoreV1().Namespaces().Get(callCtx, namespace, metav1.GetOptions{})
		cancel()
		switch {
		case err == nil:
			return ns, nil
		case apierrors.IsNotFound(err):
			return nil, nil
		case IsPermanentAPIError(err):
			return nil, fmt.Errorf("getting namespace %s: %w", namespace, err)
		}
		if !sleepCtx(ctx, crossClusterPollInterval) {
			return nil, fmt.Errorf("getting namespace %s: %w", namespace, err)
		}
	}
}

func ownedBy(ns *corev1.Namespace, token string) bool {
	return token != "" && ns.Annotations[OwnerTokenAnnotation] == token
}

// CreateOwnedInfra creates the netperf namespace annotated with token, its ServiceAccount and RoleBinding. An existing
// namespace is accepted only with the same token, as when both kubeconfigs point at one API server.
func CreateOwnedInfra(ctx context.Context, c kubernetes.Interface, token string) error {
	nsObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:        namespace,
		Labels:      map[string]string{namespace: netperfNamespaceLabel},
		Annotations: map[string]string{OwnerTokenAnnotation: token},
	}}
	log.Infof("🔨 Creating namespace: %s", namespace)
	callCtx, cancel := withAPITimeout(ctx)
	_, err := c.CoreV1().Namespaces().Create(callCtx, nsObj, metav1.CreateOptions{})
	cancel()
	if apierrors.IsAlreadyExists(err) {
		existing, getErr := getNamespace(ctx, c)
		if getErr != nil {
			return getErr
		}
		if existing == nil || !ownedBy(existing, token) {
			return fmt.Errorf("namespace %s already exists; rerun with --clean to recreate it: %w", namespace, ErrNamespaceNotOwned)
		}
		log.Infof("♻️ Namespace %s was already created by this run, reusing it", namespace)
	} else if err != nil {
		return fmt.Errorf("😥 Unable to create namespace: %w", err)
	}
	callCtx, cancel = withAPITimeout(ctx)
	_, err = c.CoreV1().ServiceAccounts(namespace).Create(callCtx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: sa}}, metav1.CreateOptions{})
	cancel()
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("😥 Unable to create service account: %w", err)
	}
	callCtx, cancel = withAPITimeout(ctx)
	_, err = c.RbacV1().RoleBindings(namespace).Create(callCtx, netperfRoleBinding(), metav1.CreateOptions{})
	cancel()
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("😥 Unable to create role-binding: %w", err)
	}
	return nil
}

// DeleteNamespaceAndWait deletes the namespace (only that object when uid is set) and waits until it is gone. NotFound and
// Conflict (terminating, uid mismatch) are not errors; transient errors are retried until ctx ends.
func DeleteNamespaceAndWait(ctx context.Context, c kubernetes.Interface, uid types.UID) error {
	opts := metav1.DeleteOptions{}
	if uid != "" {
		opts.Preconditions = &metav1.Preconditions{UID: &uid}
	}
	for {
		callCtx, cancel := withAPITimeout(ctx)
		err := c.CoreV1().Namespaces().Delete(callCtx, namespace, opts)
		cancel()
		if err == nil || apierrors.IsNotFound(err) || apierrors.IsConflict(err) {
			break
		}
		if IsPermanentAPIError(err) {
			return fmt.Errorf("deleting namespace %s: %w", namespace, err)
		}
		if !sleepCtx(ctx, crossClusterPollInterval) {
			return fmt.Errorf("timed out deleting namespace %s: %w", namespace, err)
		}
	}
	log.Infof("⏰ Waiting for %s Namespace to be deleted...", namespace)
	for {
		callCtx, cancel := withAPITimeout(ctx)
		ns, err := c.CoreV1().Namespaces().Get(callCtx, namespace, metav1.GetOptions{})
		cancel()
		var last error
		switch {
		case apierrors.IsNotFound(err):
			return nil
		case err == nil && uid != "" && ns.UID != uid:
			return nil
		case err != nil && IsPermanentAPIError(err):
			return fmt.Errorf("waiting for namespace %s deletion: %w", namespace, err)
		case err != nil:
			last = err
		default:
			last = fmt.Errorf("namespace %s still present (phase %s)", namespace, ns.Status.Phase)
		}
		if !sleepCtx(ctx, crossClusterPollInterval) {
			return fmt.Errorf("timed out waiting for namespace %s deletion: %w", namespace, last)
		}
	}
}

// DeleteOwnedNamespace deletes the namespace only if it carries token, then checks once more after a short delay
// so a create the API server applied late is removed too.
func DeleteOwnedNamespace(ctx context.Context, c kubernetes.Interface, token string) error {
	for lookup := 0; lookup < 2; lookup++ {
		if lookup > 0 && !sleepCtx(ctx, crossClusterPollInterval) {
			return nil
		}
		ns, err := getNamespace(ctx, c)
		if err != nil {
			return err
		}
		if ns == nil {
			continue
		}
		if !ownedBy(ns, token) {
			log.Warnf("Namespace %s was not created by this run; leaving it in place", namespace)
			return nil
		}
		log.Info("Cleaning resources created by k8s-netperf")
		if err := DeleteNamespaceAndWait(ctx, c, ns.UID); err != nil {
			return err
		}
	}
	return nil
}

// workerNodeSelector is the required node selector BuildSUT uses: worker, not infra, not workload.
func workerNodeSelector() *corev1.NodeSelector {
	return &corev1.NodeSelector{
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
}

func crossClusterPodParams(s *config.PerfScenarios, name, role, preferredNode string, commands [][]string) DeploymentParams {
	return DeploymentParams{
		Name:           name,
		Namespace:      namespace,
		Replicas:       1,
		Image:          k8sNetperfImage,
		Labels:         map[string]string{"role": role},
		Commands:       commands,
		Port:           NetperfServerCtlPort,
		Privileged:     s.Privileged,
		Annotations:    s.Annotations,
		WorkloadLabels: s.Labels,
		NodeAffinity: corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: workerNodeSelector(),
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
				{
					Weight: 100,
					Preference: corev1.NodeSelectorTerm{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{Key: "netperf", Operator: corev1.NodeSelectorOpIn, Values: []string{preferredNode}},
						},
					},
				},
			},
		},
	}
}

// CrossClusterDeploymentParams returns the client and server Deployments; pod-level options belong here so both sides get them.
func CrossClusterDeploymentParams(s *config.PerfScenarios) (client DeploymentParams, server DeploymentParams) {
	client = crossClusterPodParams(s, "client-across", clientAcrossRole, "client", [][]string{{"/bin/bash", "-c", "sleep 10000000"}})
	server = crossClusterPodParams(s, "server", serverRole, "server", serverCommands(s))
	return client, server
}

func deploymentFailure(d *appsv1.Deployment) error {
	for _, condition := range d.Status.Conditions {
		switch {
		case condition.Type == appsv1.DeploymentReplicaFailure && condition.Status == corev1.ConditionTrue:
			return fmt.Errorf("deployment %q failed to create pods (%s): %s", d.Name, condition.Reason, condition.Message)
		case condition.Type == appsv1.DeploymentProgressing && condition.Status == corev1.ConditionFalse:
			return fmt.Errorf("deployment %q did not progress (%s): %s", d.Name, condition.Reason, condition.Message)
		}
	}
	return nil
}

// podWaitingReasons summarizes why the pods of dp are not ready, for error messages.
func podWaitingReasons(c kubernetes.Interface, dp DeploymentParams) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pods, err := c.CoreV1().Pods(dp.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labels.Set(dp.Labels).String()})
	if err != nil || len(pods.Items) == 0 {
		return ""
	}
	var reasons []string
	for _, pod := range pods.Items {
		var details []string
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
				details = append(details, fmt.Sprintf("%s: %s", cond.Reason, cond.Message))
			}
		}
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				details = append(details, fmt.Sprintf("%s: %s", cs.State.Waiting.Reason, cs.State.Waiting.Message))
			}
		}
		reasons = append(reasons, fmt.Sprintf("%s %s [%s]", pod.Name, pod.Status.Phase, strings.Join(details, "; ")))
	}
	sort.Strings(reasons)
	return "; pods: " + strings.Join(reasons, ", ")
}

// waitForDeploymentReady polls until a replica is ready, a failure condition appears, ctx ends or crossClusterReadyTimeout passes.
func waitForDeploymentReady(ctx context.Context, c kubernetes.Interface, dp DeploymentParams) error {
	log.Infof("⏰ Checking for %s Pods to become ready...", dp.Name)
	waitCtx, cancel := context.WithTimeout(ctx, crossClusterReadyTimeout)
	defer cancel()
	for {
		callCtx, callCancel := withAPITimeout(waitCtx)
		d, err := c.AppsV1().Deployments(dp.Namespace).Get(callCtx, dp.Name, metav1.GetOptions{})
		callCancel()
		switch {
		case err == nil && d.Status.ReadyReplicas >= 1:
			return nil
		case err == nil:
			if failure := deploymentFailure(d); failure != nil {
				return fmt.Errorf("%w%s", failure, podWaitingReasons(c, dp))
			}
		case apierrors.IsNotFound(err) || IsPermanentAPIError(err):
			return fmt.Errorf("waiting for deployment %q: %w", dp.Name, err)
		}
		if !sleepCtx(waitCtx, crossClusterPollInterval) {
			if ctx.Err() != nil {
				return fmt.Errorf("waiting for deployment %q: %w", dp.Name, ctx.Err())
			}
			return fmt.Errorf("deployment %q in namespace %q not ready after %s%s", dp.Name, dp.Namespace, crossClusterReadyTimeout, podWaitingReasons(c, dp))
		}
	}
}

func listRunningPods(ctx context.Context, c kubernetes.Interface, label string) (corev1.PodList, error) {
	callCtx, cancel := withAPITimeout(ctx)
	defer cancel()
	log.Infof("Looking for pods with label %s", label)
	pods, err := c.CoreV1().Pods(namespace).List(callCtx, metav1.ListOptions{LabelSelector: label, FieldSelector: "status.phase=Running"})
	if err != nil {
		return corev1.PodList{}, fmt.Errorf("❌ Failure to capture pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return *pods, fmt.Errorf("no running pods with label %s", label)
	}
	return *pods, nil
}

func podNodeInfo(ctx context.Context, c kubernetes.Interface, label string) (metrics.NodeInfo, error) {
	var info metrics.NodeInfo
	pods, err := listRunningPods(ctx, c, label)
	if err != nil {
		return info, err
	}
	info.NodeName = pods.Items[0].Spec.NodeName
	info.IP = pods.Items[0].Status.HostIP
	callCtx, cancel := withAPITimeout(ctx)
	defer cancel()
	node, err := c.CoreV1().Nodes().Get(callCtx, info.NodeName, metav1.GetOptions{})
	if err != nil {
		return info, fmt.Errorf("getting node %s: %w", info.NodeName, err)
	}
	info.NodeSystemInfo = node.Status.NodeInfo
	return info, nil
}

func deployAndWait(ctx context.Context, c kubernetes.Interface, dp DeploymentParams) (corev1.PodList, error) {
	log.Infof("🚀 Starting Deployment for: %s in namespace: %s", dp.Name, dp.Namespace)
	callCtx, cancel := withAPITimeout(ctx)
	_, err := c.AppsV1().Deployments(dp.Namespace).Create(callCtx, newDeployment(dp), metav1.CreateOptions{})
	cancel()
	if err != nil {
		return corev1.PodList{}, fmt.Errorf("😥 Unable to create deployment %q: %w", dp.Name, err)
	}
	if err := waitForDeploymentReady(ctx, c, dp); err != nil {
		return corev1.PodList{}, err
	}
	return listRunningPods(ctx, c, labels.Set(dp.Labels).String())
}

// BuildCrossClusterSUT deploys the server side and the client on their clusters, recording each node from its own cluster.
func BuildCrossClusterSUT(ctx context.Context, clientCS, serverCS kubernetes.Interface, s *config.PerfScenarios) error {
	if !s.Pod || s.VM {
		return fmt.Errorf("cross-cluster mode supports pod workloads only")
	}
	if err := createServices(ctx, serverCS, s, crossClusterAPITimeout); err != nil {
		return fmt.Errorf("server cluster: %w", err)
	}
	clientDP, serverDP := CrossClusterDeploymentParams(s)
	var err error
	if s.Server, err = deployAndWait(ctx, serverCS, serverDP); err != nil {
		return fmt.Errorf("server cluster: %w", err)
	}
	if s.ServerNodeInfo, err = podNodeInfo(ctx, serverCS, labels.Set(serverDP.Labels).String()); err != nil {
		return fmt.Errorf("server cluster: %w", err)
	}
	if s.ClientAcross, err = deployAndWait(ctx, clientCS, clientDP); err != nil {
		return fmt.Errorf("client cluster: %w", err)
	}
	if s.ClientNodeInfo, err = podNodeInfo(ctx, clientCS, labels.Set(clientDP.Labels).String()); err != nil {
		return fmt.Errorf("client cluster: %w", err)
	}
	return nil
}

// ServiceDNSName is svc's name from another cluster, where domain is the DNS suffix for svc's cluster.
func ServiceDNSName(svc *corev1.Service, domain string) string {
	return fmt.Sprintf("%s.%s.svc.%s", svc.Name, svc.Namespace, domain)
}

// ServiceDNSNameFor builds the same name from a Service name, for validation before any Service exists.
func ServiceDNSNameFor(serviceName, domain string) string {
	return fmt.Sprintf("%s.%s.svc.%s", serviceName, namespace, domain)
}

// NodeIdentity is a node's name and addresses, the values Prometheus series are matched on.
type NodeIdentity struct {
	Name      string
	Addresses []string
}

func ListNodeIdentities(ctx context.Context, c kubernetes.Interface) ([]NodeIdentity, error) {
	callCtx, cancel := withAPITimeout(ctx)
	defer cancel()
	nodes, err := c.CoreV1().Nodes().List(callCtx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}
	ids := make([]NodeIdentity, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		id := NodeIdentity{Name: n.Name}
		for _, a := range n.Status.Addresses {
			if a.Type == corev1.NodeInternalIP || a.Type == corev1.NodeExternalIP {
				id.Addresses = append(id.Addresses, a.Address)
			}
		}
		ids = append(ids, id)
	}
	return ids, nil
}
