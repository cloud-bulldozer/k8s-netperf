package k8s

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

var nsResource = schema.GroupResource{Resource: "namespaces"}

// shortTimeouts makes the cross-cluster bounds test-sized and restores them afterwards.
func shortTimeouts(t *testing.T) {
	t.Helper()
	api, ready, poll := crossClusterAPITimeout, crossClusterReadyTimeout, crossClusterPollInterval
	crossClusterAPITimeout, crossClusterReadyTimeout, crossClusterPollInterval = time.Second, 300*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() {
		crossClusterAPITimeout, crossClusterReadyTimeout, crossClusterPollInterval = api, ready, poll
	})
}

func netperfNamespace(token string) *corev1.Namespace {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace, UID: "uid-1"}}
	if token != "" {
		ns.Annotations = map[string]string{OwnerTokenAnnotation: token}
	}
	return ns
}

type portKey struct {
	port     int32
	protocol corev1.Protocol
}

func servicePorts(svc corev1.Service) map[portKey]bool {
	ports := map[portKey]bool{}
	for _, p := range svc.Spec.Ports {
		if p.TargetPort.IntValue() != int(p.Port) {
			panic(fmt.Sprintf("service %s port %d targets %s", svc.Name, p.Port, p.TargetPort.String()))
		}
		ports[portKey{p.Port, p.Protocol}] = true
	}
	return ports
}

func tcpUDP(ports ...int32) map[portKey]bool {
	m := map[portKey]bool{}
	for _, p := range ports {
		m[portKey{p, corev1.ProtocolTCP}] = true
		m[portKey{p, corev1.ProtocolUDP}] = true
	}
	return m
}

// TestCreateServicesGolden pins every Service's name, selector and TCP/UDP ports, so the move out of BuildSUT is exact.
func TestCreateServicesGolden(t *testing.T) {
	netperfPorts := []int32{NetperfServerCtlPort}
	for i := int32(0); i < NetperfServiceDataPorts; i++ {
		netperfPorts = append(netperfPorts, NetperfServerDataPort+i)
	}
	want := map[string]struct {
		role  string
		ports map[portKey]bool
	}{
		"netperf-service":            {serverRole, tcpUDP(netperfPorts...)},
		"iperf-service":              {serverRole, tcpUDP(IperfServerCtlPort, IperfServerDataPort)},
		"uperf-service":              {serverRole, tcpUDP(UperfServerCtlPort, UperfServerDataPort)},
		"uperf-histogram-service":    {serverRole, tcpUDP(UperfLatServerCtlPort, UperfLatServerDataPort)},
		"netperf-vm-service":         {vmServerRole, tcpUDP(netperfPorts...)},
		"iperf-vm-service":           {vmServerRole, tcpUDP(IperfServerCtlPort, IperfVmServerDataPort)},
		"uperf-vm-service":           {vmServerRole, tcpUDP(UperfServerCtlPort, UperfVmServerDataPort)},
		"uperf-vm-histogram-service": {vmServerRole, tcpUDP(UperfLatServerCtlPort, UperfLatVmServerDataPort)},
	}
	testCases := []struct {
		name    string
		drivers []string
		pod, vm bool
		names   []string
	}{
		{name: "pod, all drivers", drivers: []string{"netperf", "uperf", "iperf3"}, pod: true, names: CrossClusterServiceNames},
		{name: "pod, netperf only", drivers: []string{"netperf"}, pod: true, names: []string{"netperf-service"}},
		{name: "pod, ib_write_bw only", drivers: []string{"ib_write_bw"}, pod: true},
		{name: "pod and vm, all drivers", drivers: []string{"netperf", "uperf", "iperf3"}, pod: true, vm: true, names: []string{
			"netperf-service", "iperf-service", "uperf-service", "uperf-histogram-service",
			"netperf-vm-service", "iperf-vm-service", "uperf-vm-service", "uperf-vm-histogram-service"}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientset()
			s := &config.PerfScenarios{RequestedDrivers: tc.drivers, Pod: tc.pod, VM: tc.vm}
			if err := createServices(context.Background(), c, s, 0); err != nil {
				t.Fatalf("createServices: %v", err)
			}
			list, err := c.CoreV1().Services(namespace).List(context.Background(), metav1.ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, svc := range list.Items {
				got = append(got, svc.Name)
				w, ok := want[svc.Name]
				if !ok {
					t.Errorf("unexpected service %s", svc.Name)
					continue
				}
				if svc.Spec.Selector["role"] != w.role || len(svc.Spec.Selector) != 1 {
					t.Errorf("%s selector = %v, want role=%s", svc.Name, svc.Spec.Selector, w.role)
				}
				if svc.Spec.Type != corev1.ServiceTypeClusterIP {
					t.Errorf("%s type = %s", svc.Name, svc.Spec.Type)
				}
				ports := servicePorts(svc)
				if len(ports) != len(w.ports) || len(svc.Spec.Ports) != len(w.ports) {
					t.Errorf("%s has %d ports, want %d", svc.Name, len(svc.Spec.Ports), len(w.ports))
				}
				for k := range w.ports {
					if !ports[k] {
						t.Errorf("%s missing %s/%d", svc.Name, k.protocol, k.port)
					}
				}
			}
			wantNames := append([]string(nil), tc.names...)
			sort.Strings(got)
			sort.Strings(wantNames)
			if strings.Join(got, ",") != strings.Join(wantNames, ",") {
				t.Errorf("services = %v, want %v", got, wantNames)
			}
		})
	}
}

func TestServerCommands(t *testing.T) {
	testCases := []struct {
		name    string
		drivers []string
		configs []config.Config
		want    []string
	}{
		{name: "netperf", drivers: []string{"netperf"}, want: []string{"netserver && sleep 10000000"}},
		{name: "iperf3", drivers: []string{"iperf3"}, want: []string{"iperf3 -s -p 22865 && sleep 10000000"}},
		{name: "uperf without histogram", drivers: []string{"uperf"}, configs: []config.Config{{Profile: "TCP_STREAM"}}, want: []string{"uperf -s -v -P 30000 && sleep 10000000"}},
		{name: "uperf with histogram", drivers: []string{"uperf"}, configs: []config.Config{{Profile: "TCP_STREAM_LAT"}}, want: []string{
			"/opt/uperf-histogram/bin/uperf -s -v -P 31000 && sleep 10000000",
			"uperf -s -v -P 30000 && sleep 10000000"}},
		{name: "all three", drivers: []string{"netperf", "uperf", "iperf3"}, want: []string{
			"netserver && sleep 10000000",
			"iperf3 -s -p 22865 && sleep 10000000",
			"uperf -s -v -P 30000 && sleep 10000000"}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := serverCommands(&config.PerfScenarios{RequestedDrivers: tc.drivers, Configs: tc.configs})
			if len(got) != len(tc.want) {
				t.Fatalf("serverCommands() = %v, want %v", got, tc.want)
			}
			for i, cmd := range got {
				if len(cmd) != 3 || cmd[0] != "/bin/bash" || cmd[1] != "-c" || cmd[2] != tc.want[i] {
					t.Errorf("command %d = %v, want bash -c %q", i, cmd, tc.want[i])
				}
			}
		})
	}
}

func TestCrossClusterDeploymentParams(t *testing.T) {
	s := &config.PerfScenarios{
		RequestedDrivers: []string{"netperf"},
		Privileged:       true,
		Annotations:      map[string]string{"example.com/a": "1"},
		Labels:           map[string]string{"example.com/l": "2"},
	}
	client, server := CrossClusterDeploymentParams(s)
	for _, tc := range []struct {
		dp      DeploymentParams
		name    string
		role    string
		prefers string
	}{{client, "client-across", clientAcrossRole, "client"}, {server, "server", serverRole, "server"}} {
		if tc.dp.Name != tc.name || tc.dp.Labels["role"] != tc.role || tc.dp.Namespace != namespace {
			t.Errorf("%s: name/role/namespace = %s/%s/%s", tc.name, tc.dp.Name, tc.dp.Labels["role"], tc.dp.Namespace)
		}
		if !tc.dp.Privileged || tc.dp.Annotations["example.com/a"] != "1" || tc.dp.WorkloadLabels["example.com/l"] != "2" {
			t.Errorf("%s: privileged, annotations or labels not propagated", tc.name)
		}
		if tc.dp.HostNetwork {
			t.Errorf("%s: hostNetwork set", tc.name)
		}
		if len(tc.dp.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 0 || len(tc.dp.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) != 0 {
			t.Errorf("%s: unexpected pod affinity", tc.name)
		}
		req := tc.dp.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
		if req == nil || len(req.NodeSelectorTerms) != 1 || req.NodeSelectorTerms[0].MatchExpressions[0].Key != "node-role.kubernetes.io/worker" {
			t.Errorf("%s: required worker node affinity missing: %+v", tc.name, req)
		}
		pref := tc.dp.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution
		if len(pref) != 1 || pref[0].Preference.MatchExpressions[0].Values[0] != tc.prefers {
			t.Errorf("%s: preferred node label = %+v, want netperf=%s", tc.name, pref, tc.prefers)
		}
	}
	if len(server.Commands) != 1 || server.Commands[0][2] != "netserver && sleep 10000000" {
		t.Errorf("server commands = %v", server.Commands)
	}
}

func TestCreateOwnedInfra(t *testing.T) {
	shortTimeouts(t)
	ctx := context.Background()
	t.Run("fresh", func(t *testing.T) {
		c := fake.NewClientset()
		if err := CreateOwnedInfra(ctx, c, "tok"); err != nil {
			t.Fatal(err)
		}
		ns, err := c.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if err != nil || ns.Annotations[OwnerTokenAnnotation] != "tok" || ns.Labels[namespace] != netperfNamespaceLabel {
			t.Fatalf("namespace = %+v, %v", ns, err)
		}
		if _, err := c.CoreV1().ServiceAccounts(namespace).Get(ctx, sa, metav1.GetOptions{}); err != nil {
			t.Errorf("service account: %v", err)
		}
		if _, err := c.RbacV1().RoleBindings(namespace).Get(ctx, sa, metav1.GetOptions{}); err != nil {
			t.Errorf("role binding: %v", err)
		}
	})
	t.Run("same run on a shared API server", func(t *testing.T) {
		c := fake.NewClientset()
		if err := CreateOwnedInfra(ctx, c, "tok"); err != nil {
			t.Fatal(err)
		}
		if err := CreateOwnedInfra(ctx, c, "tok"); err != nil {
			t.Fatalf("second create by the same run: %v", err)
		}
	})
	for _, token := range []string{"", "other"} {
		t.Run("foreign namespace token="+token, func(t *testing.T) {
			c := fake.NewClientset(netperfNamespace(token))
			err := CreateOwnedInfra(ctx, c, "tok")
			if !errors.Is(err, ErrNamespaceNotOwned) {
				t.Fatalf("err = %v, want ErrNamespaceNotOwned", err)
			}
			if _, err := c.CoreV1().ServiceAccounts(namespace).Get(ctx, sa, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
				t.Errorf("service account created in a foreign namespace: %v", err)
			}
		})
	}
	t.Run("service account failure", func(t *testing.T) {
		c := fake.NewClientset()
		c.PrependReactor("create", "serviceaccounts", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "serviceaccounts"}, sa, errors.New("denied"))
		})
		if err := CreateOwnedInfra(ctx, c, "tok"); err == nil {
			t.Fatal("want error")
		}
	})
}

func readyOnCreate(c *fake.Clientset) {
	c.PrependReactor("create", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		d := action.(k8stesting.CreateAction).GetObject().(*appsv1.Deployment)
		d.Status.ReadyReplicas = 1
		return false, nil, nil
	})
}

func TestWaitForDeploymentReady(t *testing.T) {
	shortTimeouts(t)
	dp := DeploymentParams{Name: "server", Namespace: namespace, Labels: map[string]string{"role": serverRole}}
	deployment := func(status appsv1.DeploymentStatus) *appsv1.Deployment {
		return &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: namespace}, Status: status}
	}
	pending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "server-abc", Namespace: namespace, Labels: map[string]string{"role": serverRole}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodPending,
			Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "0/1 nodes are available"}},
		},
	}
	testCases := []struct {
		name    string
		objects []runtime.Object
		cancel  bool
		wantErr string
	}{
		{name: "ready", objects: []runtime.Object{deployment(appsv1.DeploymentStatus{ReadyReplicas: 1})}},
		{name: "replica failure", objects: []runtime.Object{deployment(appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{
			{Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue, Reason: "FailedCreate", Message: "denied by policy"}}})}, wantErr: "denied by policy"},
		{name: "progress deadline", objects: []runtime.Object{deployment(appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{
			{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded"}}})}, wantErr: "ProgressDeadlineExceeded"},
		{name: "never ready reports pod reasons", objects: []runtime.Object{deployment(appsv1.DeploymentStatus{}), pending}, wantErr: "Unschedulable: 0/1 nodes are available"},
		{name: "missing deployment", wantErr: "not found"},
		{name: "cancelled", objects: []runtime.Object{deployment(appsv1.DeploymentStatus{})}, cancel: true, wantErr: "context canceled"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientset(tc.objects...)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			start := time.Now()
			err := waitForDeploymentReady(ctx, c, dp)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
			if time.Since(start) > 2*time.Second {
				t.Errorf("took %s", time.Since(start))
			}
		})
	}
}

func TestDeleteNamespaceAndWait(t *testing.T) {
	shortTimeouts(t)
	forbidden := apierrors.NewForbidden(nsResource, namespace, errors.New("denied"))
	transient := apierrors.NewServiceUnavailable("try later")
	testCases := []struct {
		name     string
		objects  []runtime.Object
		uid      string
		reactors func(c *fake.Clientset)
		wantErr  string
		gone     bool
	}{
		{name: "absent", gone: true},
		{name: "deleted", objects: []runtime.Object{netperfNamespace("")}, gone: true},
		{name: "delete conflict while terminating, then gone", objects: []runtime.Object{netperfNamespace("")}, gone: true, reactors: func(c *fake.Clientset) {
			c.PrependReactor("delete", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewConflict(nsResource, namespace, errors.New("terminating"))
			})
			// The first Get still sees the terminating namespace; it is gone by the second.
			var gets int32
			c.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
				if atomic.AddInt32(&gets, 1) == 2 {
					_ = c.Tracker().Delete(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, "", namespace)
				}
				return false, nil, nil
			})
		}},
		{name: "delete forbidden returns at once", objects: []runtime.Object{netperfNamespace("")}, wantErr: "forbidden", reactors: func(c *fake.Clientset) {
			c.PrependReactor("delete", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, forbidden })
		}},
		{name: "get forbidden after delete returns at once", objects: []runtime.Object{netperfNamespace("")}, wantErr: "forbidden", reactors: func(c *fake.Clientset) {
			c.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, forbidden })
		}},
		{name: "transient get errors then gone", objects: []runtime.Object{netperfNamespace("")}, gone: true, reactors: func(c *fake.Clientset) {
			var calls int32
			c.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
				if atomic.AddInt32(&calls, 1) <= 3 {
					return true, nil, transient
				}
				return false, nil, nil
			})
		}},
		{name: "transient delete errors then deleted", objects: []runtime.Object{netperfNamespace("")}, gone: true, reactors: func(c *fake.Clientset) {
			var calls int32
			c.PrependReactor("delete", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
				if atomic.AddInt32(&calls, 1) <= 2 {
					return true, nil, transient
				}
				return false, nil, nil
			})
		}},
		{name: "never goes away", objects: []runtime.Object{netperfNamespace("")}, wantErr: "timed out", reactors: func(c *fake.Clientset) {
			c.PrependReactor("delete", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, nil })
		}},
		{name: "uid mismatch leaves the other namespace", objects: []runtime.Object{netperfNamespace("")}, uid: "uid-other", reactors: func(c *fake.Clientset) {
			c.PrependReactor("delete", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewConflict(nsResource, namespace, errors.New("precondition failed: UID"))
			})
		}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientset(tc.objects...)
			if tc.reactors != nil {
				tc.reactors(c)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			start := time.Now()
			err := DeleteNamespaceAndWait(ctx, c, types.UID(tc.uid))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				if tc.wantErr == "forbidden" && time.Since(start) > 200*time.Millisecond {
					t.Errorf("permanent error took %s, want an immediate return", time.Since(start))
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			_, getErr := c.Tracker().Get(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, "", namespace)
			if gone := apierrors.IsNotFound(getErr); gone != tc.gone {
				t.Errorf("namespace gone = %t, want %t", gone, tc.gone)
			}
		})
	}
}

func TestDeleteOwnedNamespace(t *testing.T) {
	shortTimeouts(t)
	ctx := context.Background()
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	t.Run("owned is deleted", func(t *testing.T) {
		c := fake.NewClientset(netperfNamespace("tok"))
		if err := DeleteOwnedNamespace(ctx, c, "tok"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Tracker().Get(gvr, "", namespace); !apierrors.IsNotFound(err) {
			t.Fatalf("namespace still present: %v", err)
		}
	})
	t.Run("foreign is left alone", func(t *testing.T) {
		c := fake.NewClientset(netperfNamespace("other"))
		if err := DeleteOwnedNamespace(ctx, c, "tok"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Tracker().Get(gvr, "", namespace); err != nil {
			t.Fatalf("foreign namespace deleted: %v", err)
		}
	})
	t.Run("late create after the first lookup is removed", func(t *testing.T) {
		c := fake.NewClientset()
		var gets int32
		c.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
			if atomic.AddInt32(&gets, 1) == 1 {
				// The first lookup misses; a cancelled create lands right after it.
				_ = c.Tracker().Add(netperfNamespace("tok"))
				return true, nil, apierrors.NewNotFound(nsResource, namespace)
			}
			return false, nil, nil
		})
		if err := DeleteOwnedNamespace(ctx, c, "tok"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Tracker().Get(gvr, "", namespace); !apierrors.IsNotFound(err) {
			t.Fatalf("late namespace survived: %v", err)
		}
	})
	t.Run("permanent get error", func(t *testing.T) {
		c := fake.NewClientset(netperfNamespace("tok"))
		c.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewUnauthorized("no")
		})
		if err := DeleteOwnedNamespace(ctx, c, "tok"); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestBuildCrossClusterSUT(t *testing.T) {
	shortTimeouts(t)
	node := func(name string) *corev1.Node {
		return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{KernelVersion: "k-" + name}}}
	}
	pod := func(name, role, nodeName, podIP, hostIP string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{"role": role}},
			Spec:       corev1.PodSpec{NodeName: nodeName},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning, PodIP: podIP, HostIP: hostIP},
		}
	}
	clientCS := fake.NewClientset(node("node-a"), pod("client-across-1", clientAcrossRole, "node-a", "10.42.0.5", "192.0.2.4"))
	serverCS := fake.NewClientset(node("node-b"), pod("server-1", serverRole, "node-b", "10.45.0.7", "192.0.2.5"))
	readyOnCreate(clientCS)
	readyOnCreate(serverCS)
	s := &config.PerfScenarios{Pod: true, RequestedDrivers: []string{"netperf", "iperf3"}}
	if err := BuildCrossClusterSUT(context.Background(), clientCS, serverCS, s); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := serverCS.AppsV1().Deployments(namespace).Get(ctx, "server", metav1.GetOptions{}); err != nil {
		t.Errorf("server deployment not on the server cluster: %v", err)
	}
	if _, err := clientCS.AppsV1().Deployments(namespace).Get(ctx, "server", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("server deployment on the client cluster: %v", err)
	}
	if _, err := clientCS.AppsV1().Deployments(namespace).Get(ctx, "client-across", metav1.GetOptions{}); err != nil {
		t.Errorf("client deployment not on the client cluster: %v", err)
	}
	if svcs, _ := clientCS.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{}); len(svcs.Items) != 0 {
		t.Errorf("services created on the client cluster: %d", len(svcs.Items))
	}
	if s.NetperfService == nil || s.IperfService == nil || s.UperfService != nil {
		t.Errorf("services = netperf %v iperf %v uperf %v", s.NetperfService != nil, s.IperfService != nil, s.UperfService != nil)
	}
	if s.ServerNodeInfo.NodeName != "node-b" || s.ServerNodeInfo.KernelVersion != "k-node-b" || s.ServerNodeInfo.IP != "192.0.2.5" {
		t.Errorf("server node info = %+v", s.ServerNodeInfo)
	}
	if s.ClientNodeInfo.NodeName != "node-a" || s.ClientNodeInfo.IP != "192.0.2.4" {
		t.Errorf("client node info = %+v", s.ClientNodeInfo)
	}
	if s.Server.Items[0].Status.PodIP != "10.45.0.7" || s.ClientAcross.Items[0].Name != "client-across-1" {
		t.Errorf("pods = %v / %v", s.Server.Items, s.ClientAcross.Items)
	}
	if got := ServiceDNSName(s.NetperfService, "nfv-amd-5.remote"); got != "netperf-service.netperf.svc.nfv-amd-5.remote" {
		t.Errorf("ServiceDNSName = %s", got)
	}
}

func TestBuildCrossClusterSUTRejectsVM(t *testing.T) {
	if err := BuildCrossClusterSUT(context.Background(), fake.NewClientset(), fake.NewClientset(), &config.PerfScenarios{Pod: true, VM: true}); err == nil {
		t.Fatal("want error for VM workloads")
	}
}

func TestServiceDNSNameFor(t *testing.T) {
	if got := ServiceDNSNameFor("uperf-histogram-service", "x.remote"); got != "uperf-histogram-service.netperf.svc.x.remote" {
		t.Errorf("ServiceDNSNameFor = %s", got)
	}
}

func TestListNodeIdentities(t *testing.T) {
	c := fake.NewClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{
			{Type: corev1.NodeInternalIP, Address: "192.0.2.1"},
			{Type: corev1.NodeHostName, Address: "n1.example"},
			{Type: corev1.NodeExternalIP, Address: "198.51.100.1"},
		}},
	})
	ids, err := ListNodeIdentities(context.Background(), c)
	if err != nil || len(ids) != 1 || ids[0].Name != "n1" || strings.Join(ids[0].Addresses, ",") != "192.0.2.1,198.51.100.1" {
		t.Fatalf("ListNodeIdentities = %+v, %v", ids, err)
	}
}

func TestNamespaceExistsAndCountWorkerNodes(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientset(netperfNamespace(""),
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w", Labels: map[string]string{"node-role.kubernetes.io/worker": ""}}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "m", Labels: map[string]string{"node-role.kubernetes.io/master": ""}}})
	if ok, err := NamespaceExists(ctx, c); !ok || err != nil {
		t.Errorf("NamespaceExists = %t, %v", ok, err)
	}
	if ok, err := NamespaceExists(ctx, fake.NewClientset()); ok || err != nil {
		t.Errorf("NamespaceExists on empty = %t, %v", ok, err)
	}
	denied := fake.NewClientset()
	denied.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(nsResource, namespace, errors.New("denied"))
	})
	if _, err := NamespaceExists(ctx, denied); err == nil {
		t.Error("NamespaceExists hid a Forbidden error")
	}
	if n, err := CountWorkerNodes(ctx, c); n != 1 || err != nil {
		t.Errorf("CountWorkerNodes = %d, %v", n, err)
	}
}

func TestIsPermanentAPIError(t *testing.T) {
	permanent := []error{
		apierrors.NewForbidden(nsResource, "x", errors.New("no")),
		apierrors.NewUnauthorized("no"),
		apierrors.NewBadRequest("no"),
	}
	transient := []error{
		apierrors.NewServiceUnavailable("later"),
		apierrors.NewTooManyRequests("later", 1),
		apierrors.NewTimeoutError("slow", 1),
		apierrors.NewInternalError(errors.New("boom")),
		context.DeadlineExceeded,
		errors.New("connection refused"),
	}
	for _, err := range permanent {
		if !IsPermanentAPIError(err) {
			t.Errorf("%v classified transient", err)
		}
	}
	for _, err := range transient {
		if IsPermanentAPIError(err) {
			t.Errorf("%v classified permanent", err)
		}
	}
}

// deadlineProbe records whether each request carried a deadline, then fails it without network access.
type deadlineProbe struct {
	mu      sync.Mutex
	total   int
	missing []string
}

func (p *deadlineProbe) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.total++
	if _, ok := r.Context().Deadline(); !ok {
		p.missing = append(p.missing, r.Method+" "+r.URL.Path)
	}
	return nil, errors.New("deadline probe: no network")
}

// The caller's context is only cancellable, so any deadline on a request must come from the per-call bound.
func TestCrossClusterAPICallsHaveDeadlines(t *testing.T) {
	shortTimeouts(t)
	probe := &deadlineProbe{}
	c, err := kubernetes.NewForConfig(&rest.Config{Host: "http://deadline.invalid", Transport: probe, QPS: 1e6, Burst: 1e6})
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]func(ctx context.Context){
		"BuildCrossClusterSUT": func(ctx context.Context) {
			_ = BuildCrossClusterSUT(ctx, c, c, &config.PerfScenarios{Pod: true, RequestedDrivers: []string{"netperf", "iperf3", "uperf"}})
		},
		"CreateOwnedInfra":       func(ctx context.Context) { _ = CreateOwnedInfra(ctx, c, "tok") },
		"DeleteNamespaceAndWait": func(ctx context.Context) { _ = DeleteNamespaceAndWait(ctx, c, "") },
		"DeleteOwnedNamespace":   func(ctx context.Context) { _ = DeleteOwnedNamespace(ctx, c, "tok") },
		"NamespaceExists":        func(ctx context.Context) { _, _ = NamespaceExists(ctx, c) },
		"CountWorkerNodes":       func(ctx context.Context) { _, _ = CountWorkerNodes(ctx, c) },
		"ListNodeIdentities":     func(ctx context.Context) { _, _ = ListNodeIdentities(ctx, c) },
		"deployAndWait": func(ctx context.Context) {
			_, _ = deployAndWait(ctx, c, DeploymentParams{Name: "server", Namespace: namespace, Labels: map[string]string{"role": serverRole}})
		},
		"waitForDeploymentReady": func(ctx context.Context) {
			_ = waitForDeploymentReady(ctx, c, DeploymentParams{Name: "server", Namespace: namespace, Labels: map[string]string{"role": serverRole}})
		},
		"podNodeInfo": func(ctx context.Context) { _, _ = podNodeInfo(ctx, c, "role=server") },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			before := probe.total
			ctx, cancel := context.WithCancel(context.Background())
			stop := time.AfterFunc(100*time.Millisecond, cancel)
			defer stop.Stop()
			defer cancel()
			call(ctx)
			if probe.total == before {
				t.Fatalf("%s sent no request", name)
			}
		})
	}
	if len(probe.missing) > 0 {
		t.Fatalf("%d of %d requests had no deadline: %v", len(probe.missing), probe.total, probe.missing)
	}
}
