package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/k8s"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/metrics"
	result "github.com/cloud-bulldozer/k8s-netperf/pkg/results"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func validXCFlags() crossClusterFlags {
	return crossClusterFlags{serverKubeconfig: "/tmp/kc", pod: true}
}

func TestValidateCrossClusterFlags(t *testing.T) {
	testCases := []struct {
		name    string
		mutate  func(*crossClusterFlags)
		wantErr string
		domain  string
	}{
		{name: "minimal", mutate: func(*crossClusterFlags) {}},
		{name: "domain normalized", mutate: func(f *crossClusterFlags) { f.serverDomain = "NFV-AMD-5.Remote." }, domain: "nfv-amd-5.remote"},
		{name: "server prom allowed", mutate: func(f *crossClusterFlags) { f.serverProm = "http://p:9090" }},
		{name: "domain without kubeconfig", mutate: func(f *crossClusterFlags) { f.serverKubeconfig = ""; f.serverDomain = "x.remote" }, wantErr: "--server-domain requires --server-kubeconfig"},
		{name: "server prom without kubeconfig", mutate: func(f *crossClusterFlags) { f.serverKubeconfig = ""; f.serverProm = "http://p" }, wantErr: "--server-prom requires --server-kubeconfig"},
		{name: "nothing set", mutate: func(f *crossClusterFlags) { *f = crossClusterFlags{pod: true} }},
		{name: "serverIP", mutate: func(f *crossClusterFlags) { f.serverIP = "1.2.3.4" }, wantErr: "--serverIP"},
		{name: "vm", mutate: func(f *crossClusterFlags) { f.vm = true }, wantErr: "--vm"},
		{name: "pod false", mutate: func(f *crossClusterFlags) { f.pod = false }, wantErr: "--pod=false"},
		{name: "udnl2", mutate: func(f *crossClusterFlags) { f.udnl2 = true }, wantErr: "--udnl2"},
		{name: "udnl3", mutate: func(f *crossClusterFlags) { f.udnl3 = true }, wantErr: "--udnl3"},
		{name: "cudn", mutate: func(f *crossClusterFlags) { f.cudn = "layer2" }, wantErr: "--cudn"},
		{name: "hostNet", mutate: func(f *crossClusterFlags) { f.hostNet = true }, wantErr: "--hostNet"},
		{name: "all", mutate: func(f *crossClusterFlags) { f.all = true }, wantErr: "--all"},
		{name: "local", mutate: func(f *crossClusterFlags) { f.local = true }, wantErr: "--local"},
		{name: "across", mutate: func(f *crossClusterFlags) { f.across = true }, wantErr: "--across"},
		{name: "bridge", mutate: func(f *crossClusterFlags) { f.bridge = "br0" }, wantErr: "--bridge"},
		{name: "sriov", mutate: func(f *crossClusterFlags) { f.sriov = "ens1f0" }, wantErr: "--sriov"},
		{name: "macvlan", mutate: func(f *crossClusterFlags) { f.macvlan = "eth0" }, wantErr: "--macvlan"},
		{name: "localnet", mutate: func(f *crossClusterFlags) { f.localnet = "physnet" }, wantErr: "--localnet"},
		{name: "ib-write-bw", mutate: func(f *crossClusterFlags) { f.ibWriteBw = true }, wantErr: "--ib-write-bw"},
		{name: "several reported together", mutate: func(f *crossClusterFlags) { f.vm = true; f.local = true }, wantErr: "--local"},
		{name: "invalid domain", mutate: func(f *crossClusterFlags) { f.serverDomain = "bad_domain" }, wantErr: "not a valid DNS name"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			f := validXCFlags()
			tc.mutate(&f)
			domain, err := validateCrossClusterFlags(f)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if domain != tc.domain {
				t.Errorf("domain = %q, want %q", domain, tc.domain)
			}
		})
	}
}

func TestNormalizeServerDomain(t *testing.T) {
	label63 := strings.Repeat("a", 63)
	// Valid alone (<= 253) but too long once "uperf-histogram-service.netperf.svc." is prepended.
	longDomain := strings.Join([]string{label63, label63, label63, strings.Repeat("b", 40)}, ".")
	testCases := []struct {
		in, want, wantErr string
	}{
		{in: "nfv-amd-5.remote", want: "nfv-amd-5.remote"},
		{in: "cluster.local.", want: "cluster.local"},
		{in: " Upper.Case ", want: "upper.case"},
		{in: "", wantErr: "empty"},
		{in: ".", wantErr: "empty"},
		{in: "under_score.remote", wantErr: "not a valid DNS name"},
		{in: strings.Repeat("a", 64) + ".remote", wantErr: "invalid label"},
		{in: longDomain, wantErr: "too long"},
	}
	for _, tc := range testCases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := normalizeServerDomain(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("normalizeServerDomain(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
	if len(longDomain) > 253 {
		t.Fatalf("test domain itself is too long: %d", len(longDomain))
	}
}

func TestValidateCrossClusterConfigs(t *testing.T) {
	pod := config.Config{Profile: "TCP_STREAM", Parallelism: 32}
	svc := func(p int) config.Config { return config.Config{Profile: "TCP_STREAM", Parallelism: p, Service: true} }
	testCases := []struct {
		name    string
		cfgs    []config.Config
		domain  string
		netperf bool
		wantErr string
	}{
		{name: "pod only without domain", cfgs: []config.Config{pod}, netperf: true},
		{name: "service without domain", cfgs: []config.Config{pod, svc(1)}, netperf: true, wantErr: "needs --server-domain"},
		{name: "netperf service 16 streams", cfgs: []config.Config{svc(16)}, domain: "x.remote", netperf: true},
		{name: "netperf service 17 streams", cfgs: []config.Config{svc(17)}, domain: "x.remote", netperf: true, wantErr: "exposes 16 netperf data ports"},
		{name: "uperf or iperf3 service 32 streams", cfgs: []config.Config{svc(32)}, domain: "x.remote", netperf: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCrossClusterConfigs(tc.cfgs, tc.domain, tc.netperf)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("err = %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func svcObj(name string) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "netperf"}}
}

func TestCrossClusterServerAddress(t *testing.T) {
	s := config.PerfScenarios{
		ServerDomain:    "nfv-amd-5.remote",
		Server:          corev1.PodList{Items: []corev1.Pod{{Status: corev1.PodStatus{PodIP: "10.45.0.7"}}}},
		NetperfService:  svcObj("netperf-service"),
		IperfService:    svcObj("iperf-service"),
		UperfService:    svcObj("uperf-service"),
		UperfLatService: svcObj("uperf-histogram-service"),
	}
	testCases := []struct {
		driver, profile string
		service         bool
		want, target    string
	}{
		{"netperf", "TCP_STREAM", false, "10.45.0.7", result.CrossClusterTargetPodIP},
		{"uperf", "TCP_STREAM_LAT", false, "10.45.0.7", result.CrossClusterTargetPodIP},
		{"netperf", "TCP_CRR", true, "netperf-service.netperf.svc.nfv-amd-5.remote", result.CrossClusterTargetServiceDNS},
		{"iperf3", "UDP_STREAM", true, "iperf-service.netperf.svc.nfv-amd-5.remote", result.CrossClusterTargetServiceDNS},
		{"uperf", "TCP_RR", true, "uperf-service.netperf.svc.nfv-amd-5.remote", result.CrossClusterTargetServiceDNS},
		{"uperf", "TCP_STREAM_LAT", true, "uperf-histogram-service.netperf.svc.nfv-amd-5.remote", result.CrossClusterTargetServiceDNS},
	}
	for _, tc := range testCases {
		t.Run(tc.driver+"/"+tc.profile, func(t *testing.T) {
			got, target, err := crossClusterServerAddress(config.Config{Profile: tc.profile, Service: tc.service}, s, tc.driver)
			if err != nil || got != tc.want || target != tc.target {
				t.Fatalf("got %q %q %v, want %q %q", got, target, err, tc.want, tc.target)
			}
		})
	}
	errCases := map[string]func() (config.Config, config.PerfScenarios, string){
		"no server pod": func() (config.Config, config.PerfScenarios, string) {
			return config.Config{}, config.PerfScenarios{}, "netperf"
		},
		"missing service": func() (config.Config, config.PerfScenarios, string) {
			c := s
			c.IperfService = nil
			return config.Config{Service: true}, c, "iperf3"
		},
		"missing domain": func() (config.Config, config.PerfScenarios, string) {
			c := s
			c.ServerDomain = ""
			return config.Config{Service: true}, c, "netperf"
		},
		"unsupported driver": func() (config.Config, config.PerfScenarios, string) {
			return config.Config{Service: true}, s, "ib_write_bw"
		},
	}
	for name, f := range errCases {
		t.Run(name, func(t *testing.T) {
			nc, ps, driver := f()
			if _, _, err := crossClusterServerAddress(nc, ps, driver); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func nsObj(token string) *corev1.Namespace {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "netperf", UID: "u1"}}
	if token != "" {
		ns.Annotations = map[string]string{k8s.OwnerTokenAnnotation: token}
	}
	return ns
}

func TestCheckCrossClusterNodesAndLeftovers(t *testing.T) {
	ctx := context.Background()
	worker := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w", Labels: map[string]string{"node-role.kubernetes.io/worker": ""}}}
	if err := checkCrossClusterNodes(ctx, fake.NewClientset(worker), fake.NewClientset(worker.DeepCopy())); err != nil {
		t.Errorf("nodes on both: %v", err)
	}
	if err := checkCrossClusterNodes(ctx, fake.NewClientset(worker), fake.NewClientset()); err == nil || !strings.Contains(err.Error(), "server cluster has no node") {
		t.Errorf("no server worker: %v", err)
	}
	if err := checkNoLeftoverNamespaces(ctx, fake.NewClientset(), fake.NewClientset()); err != nil {
		t.Errorf("no leftovers: %v", err)
	}
	if err := checkNoLeftoverNamespaces(ctx, fake.NewClientset(), fake.NewClientset(nsObj("x"))); err == nil || !strings.Contains(err.Error(), "server cluster already has a netperf namespace") {
		t.Errorf("leftover on server: %v", err)
	}
}

// testLifecycle returns a lifecycle on two fake clusters with test-sized bounds.
func testLifecycle(client, server kubernetes.Interface) *crossClusterLifecycle {
	l := newCrossClusterLifecycle(client, server, "tok")
	l.cleanupTimeout = 2 * time.Second
	l.joinTimeout = 200 * time.Millisecond
	return l
}

func nsExists(t *testing.T, c kubernetes.Interface) bool {
	t.Helper()
	_, err := c.CoreV1().Namespaces().Get(context.Background(), "netperf", metav1.GetOptions{})
	return err == nil
}

func TestLifecyclePreCleanDoesNotStopCreation(t *testing.T) {
	client, server := fake.NewClientset(nsObj("old")), fake.NewClientset(nsObj("old"))
	l := testLifecycle(client, server)
	if err := l.PreClean(); err != nil {
		t.Fatal(err)
	}
	if nsExists(t, client) || nsExists(t, server) {
		t.Fatal("PreClean left a namespace")
	}
	if err := l.createInfra(); err != nil {
		t.Fatalf("createInfra after PreClean: %v", err)
	}
	if !nsExists(t, client) || !nsExists(t, server) {
		t.Fatal("createInfra did not create both namespaces")
	}
	if err := l.StopAndClean(); err != nil {
		t.Fatal(err)
	}
	if nsExists(t, client) || nsExists(t, server) {
		t.Fatal("StopAndClean left a namespace")
	}
	if err := l.create([]int{clientClusterIdx}, func(context.Context) error { return nil }); !errors.Is(err, errStopping) {
		t.Errorf("create after stop = %v, want errStopping", err)
	}
}

func TestLifecycleStopBetweenCreates(t *testing.T) {
	client, server := fake.NewClientset(), fake.NewClientset()
	l := testLifecycle(client, server)
	if err := l.create([]int{clientClusterIdx}, func(ctx context.Context) error {
		return k8s.CreateOwnedInfra(ctx, client, l.token)
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.StopAndClean(); err != nil {
		t.Fatal(err)
	}
	ran := false
	err := l.create([]int{serverClusterIdx}, func(context.Context) error { ran = true; return nil })
	if !errors.Is(err, errStopping) || ran {
		t.Fatalf("second create after stop: err=%v ran=%t", err, ran)
	}
	if nsExists(t, client) {
		t.Error("client namespace left behind")
	}
}

func TestLifecycleWaitsForInFlightCreate(t *testing.T) {
	client, server := fake.NewClientset(), fake.NewClientset()
	l := testLifecycle(client, server)
	l.joinTimeout = 2 * time.Second
	started := make(chan struct{})
	created := make(chan struct{})
	go func() {
		_ = l.create([]int{serverClusterIdx}, func(ctx context.Context) error {
			close(started)
			<-ctx.Done() // honors cancellation, then the request it had in flight lands
			_ = k8s.CreateOwnedInfra(context.Background(), server, l.token)
			close(created)
			return ctx.Err()
		})
	}()
	<-started
	if err := l.StopAndClean(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-created:
	default:
		t.Fatal("StopAndClean returned before the in-flight create finished")
	}
	if nsExists(t, server) {
		t.Fatal("namespace from the in-flight create survived")
	}
}

func TestLifecycleAbandonsCreateIgnoringCancellation(t *testing.T) {
	client, server := fake.NewClientset(), fake.NewClientset(nsObj("tok"))
	l := testLifecycle(client, server)
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})
	go func() {
		_ = l.create([]int{serverClusterIdx}, func(context.Context) error {
			close(started)
			<-release // ignores cancellation
			return nil
		})
	}()
	<-started
	start := time.Now()
	if err := l.StopAndClean(); err != nil {
		t.Fatal(err)
	}
	// The join bound plus the namespace deletion and its 2s confirmation lookup.
	if d := time.Since(start); d < l.joinTimeout || d > l.joinTimeout+5*time.Second {
		t.Errorf("StopAndClean took %s, want the %s join bound plus deletion", d, l.joinTimeout)
	}
	if nsExists(t, server) {
		t.Fatal("cleanup did not run after the join bound")
	}
}

func TestLifecycleRunsOnce(t *testing.T) {
	var calls int32
	l := testLifecycle(fake.NewClientset(), fake.NewClientset())
	l.attempted[clientClusterIdx] = true
	l.deleteOwned = func(context.Context, kubernetes.Interface, string) error {
		atomic.AddInt32(&calls, 1)
		time.Sleep(20 * time.Millisecond)
		return nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = l.StopAndClean() }()
	}
	wg.Wait()
	_ = l.StopAndClean()
	if calls != 1 {
		t.Errorf("cleanup ran %d times, want 1", calls)
	}
}

func TestLifecycleSkipsNeverAttemptedCluster(t *testing.T) {
	client, server := fake.NewClientset(nsObj("tok")), fake.NewClientset(nsObj("tok"))
	l := testLifecycle(client, server)
	l.attempted[clientClusterIdx] = true
	if err := l.StopAndClean(); err != nil {
		t.Fatal(err)
	}
	if nsExists(t, client) {
		t.Error("attempted cluster not cleaned")
	}
	if !nsExists(t, server) {
		t.Error("never-attempted cluster was touched")
	}
}

func TestLifecycleOneClusterFailingOrHanging(t *testing.T) {
	for _, mode := range []string{"error", "hang"} {
		t.Run(mode, func(t *testing.T) {
			client, server := fake.NewClientset(), fake.NewClientset(nsObj("tok"))
			l := testLifecycle(client, server)
			l.cleanupTimeout = 300 * time.Millisecond
			l.attempted[clientClusterIdx], l.attempted[serverClusterIdx] = true, true
			l.deleteOwned = func(ctx context.Context, c kubernetes.Interface, token string) error {
				if c == client {
					if mode == "hang" {
						<-ctx.Done()
						return ctx.Err()
					}
					return errors.New("boom")
				}
				return k8s.DeleteOwnedNamespace(ctx, c, token)
			}
			err := l.StopAndClean()
			if err == nil || !strings.Contains(err.Error(), "client cluster") {
				t.Fatalf("err = %v, want the client cluster failure", err)
			}
			if nsExists(t, server) {
				t.Error("server cluster not cleaned while the client cluster failed")
			}
		})
	}
}

func TestLifecycleRecoversNamespaceAfterServiceAccountFailure(t *testing.T) {
	client, server := fake.NewClientset(), fake.NewClientset()
	l := testLifecycle(client, server)
	err := l.create([]int{clientClusterIdx}, func(ctx context.Context) error {
		if err := k8s.CreateOwnedInfra(ctx, client, l.token); err != nil {
			return err
		}
		return errors.New("service account create failed after the namespace")
	})
	if err == nil {
		t.Fatal("want error")
	}
	if err := l.StopAndClean(); err != nil {
		t.Fatal(err)
	}
	if nsExists(t, client) {
		t.Error("namespace of a partially failed create survived")
	}
}

func TestLifecycleRecoversCommittedCreateWithLostResponse(t *testing.T) {
	client, server := fake.NewClientset(), fake.NewClientset()
	l := testLifecycle(client, server)
	err := l.create([]int{serverClusterIdx}, func(context.Context) error {
		// The API server committed the namespace but the client saw a cancellation.
		if _, err := server.CoreV1().Namespaces().Create(context.Background(), nsObj(l.token), metav1.CreateOptions{}); err != nil {
			return err
		}
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if err := l.StopAndClean(); err != nil {
		t.Fatal(err)
	}
	if nsExists(t, server) {
		t.Error("committed namespace without a returned UID survived")
	}
}

func TestLifecycleLeavesForeignNamespace(t *testing.T) {
	server := fake.NewClientset(nsObj("someone-else"))
	l := testLifecycle(fake.NewClientset(), server)
	l.attempted[serverClusterIdx] = true
	if err := l.StopAndClean(); err != nil {
		t.Fatal(err)
	}
	if !nsExists(t, server) {
		t.Error("foreign namespace deleted")
	}
}

func TestLifecycleJoinsInterruptedPreClean(t *testing.T) {
	l := testLifecycle(fake.NewClientset(), fake.NewClientset())
	clientRelease := make(chan struct{})
	var finished int32
	l.deleteAny = func(ctx context.Context, c kubernetes.Interface) error {
		if c == l.clusters[clientClusterIdx].client {
			<-clientRelease // blocked delete
		} else {
			time.Sleep(50 * time.Millisecond) // not done yet when the signal arrives
		}
		atomic.AddInt32(&finished, 1)
		return nil
	}
	preCleanDone := make(chan error, 1)
	go func() { preCleanDone <- l.PreClean() }()
	time.Sleep(10 * time.Millisecond)
	stopDone := make(chan struct{})
	go func() { _ = l.StopAndClean(); close(stopDone) }()
	select {
	case <-stopDone:
		t.Fatal("StopAndClean returned while PreClean deletions were still running")
	case <-time.After(150 * time.Millisecond):
	}
	close(clientRelease)
	<-stopDone
	if got := atomic.LoadInt32(&finished); got != 2 {
		t.Errorf("PreClean deletions finished = %d, want 2", got)
	}
	if err := <-preCleanDone; err != nil {
		t.Errorf("PreClean: %v", err)
	}
	if err := l.PreClean(); !errors.Is(err, errStopping) {
		t.Errorf("PreClean after stop = %v, want errStopping", err)
	}
}

func TestHandleCrossClusterSignals(t *testing.T) {
	for _, tc := range []struct {
		sig  os.Signal
		code int
	}{{os.Interrupt, 130}, {syscall.SIGTERM, 143}} {
		t.Run(tc.sig.String(), func(t *testing.T) {
			signals := make(chan os.Signal, 2)
			var marked, stopped int32
			markedBeforeStop := false
			code := make(chan int, 1)
			signals <- tc.sig
			handleCrossClusterSignals(signals,
				func() { atomic.AddInt32(&marked, 1) },
				func() error {
					markedBeforeStop = atomic.LoadInt32(&marked) == 1
					atomic.AddInt32(&stopped, 1)
					return nil
				},
				func(c int) { code <- c })
			if got := <-code; got != tc.code || stopped != 1 || !markedBeforeStop {
				t.Fatalf("exit %d after %d stops (marked before stop %t), want %d after 1", got, stopped, markedBeforeStop, tc.code)
			}
		})
	}
	t.Run("second signal exits without waiting", func(t *testing.T) {
		signals := make(chan os.Signal, 2)
		block := make(chan struct{})
		defer close(block)
		code := make(chan int, 1)
		signals <- os.Interrupt
		signals <- os.Interrupt
		done := make(chan struct{})
		go func() {
			handleCrossClusterSignals(signals, func() {}, func() error { <-block; return nil }, func(c int) { code <- c })
			close(done)
		}()
		select {
		case got := <-code:
			if got != 130 {
				t.Errorf("exit %d", got)
			}
		case <-time.After(time.Second):
			t.Fatal("second signal did not exit")
		}
		<-done
	})
}

func TestMarkSignaled(t *testing.T) {
	l := testLifecycle(fake.NewClientset(), fake.NewClientset())
	l.markSignaled()
	if !l.signaled.Load() {
		t.Fatal("markSignaled did not mark the lifecycle as signaled")
	}
}

const preCleanChildEnv = "K8S_NETPERF_TEST_PRECLEAN_CHILD"

// runPreCleanChild is TestPreCleanFailureExitCode's child: deleting either leftover namespace is denied after a delay,
// and in signal mode the first delete sends SIGTERM, so the signal always lands during PreClean.
func runPreCleanChild() {
	worker := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "w", Labels: map[string]string{"node-role.kubernetes.io/worker": ""}}}
	signalMode := os.Getenv(preCleanChildEnv) == "signal"
	var sendSignal sync.Once
	var clients []kubernetes.Interface
	for i := 0; i < 2; i++ {
		c := fake.NewClientset(worker.DeepCopy(), nsObj("old"))
		c.PrependReactor("delete", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
			if signalMode {
				sendSignal.Do(func() { _ = syscall.Kill(os.Getpid(), syscall.SIGTERM) })
			}
			time.Sleep(300 * time.Millisecond)
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "netperf", errors.New("denied by test"))
		})
		clients = append(clients, c)
	}
	startCrossCluster(clients[0], clients[1], true)
	os.Exit(0) // not reached: PreClean fails
}

// A failing start-of-run cleanup exits 1, or 143 when SIGTERM arrived during it.
func TestPreCleanFailureExitCode(t *testing.T) {
	if mode := os.Getenv(preCleanChildEnv); mode != "" {
		runPreCleanChild()
		return
	}
	for _, tc := range []struct {
		mode string
		want int
	}{{"signal", 143}, {"plain", 1}} {
		t.Run(tc.mode, func(t *testing.T) {
			for i := 0; i < 5; i++ {
				cmd := exec.Command(os.Args[0], "-test.run=^TestPreCleanFailureExitCode$", "-test.count=1")
				cmd.Env = append(os.Environ(), preCleanChildEnv+"="+tc.mode)
				out, err := cmd.CombinedOutput()
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != tc.want {
					t.Fatalf("run %d: err %v, want exit %d; output:\n%s", i, err, tc.want, out)
				}
				if !strings.Contains(string(out), "denied by test") {
					t.Fatalf("run %d: PreClean failure not reported:\n%s", i, out)
				}
			}
		})
	}
}

func TestPromAttribution(t *testing.T) {
	if !promPatternMatches("node-1", "node-1") || promPatternMatches("node-1", "node-10") || !promPatternMatches("a.b", "axb") {
		t.Fatal("promPatternMatches does not follow anchored regex semantics")
	}
	if !samePromEndpoint("http://p:9090/", "http://p:9090") || samePromEndpoint("", "") || samePromEndpoint("http://a", "http://b") {
		t.Fatal("samePromEndpoint")
	}
	clientNode := metrics.NodeInfo{NodeName: "client-a", IP: "192.0.2.1"}
	serverNode := metrics.NodeInfo{NodeName: "worker-0", IP: "198.51.100.1"}
	clientCluster := []k8s.NodeIdentity{{Name: "client-a", Addresses: []string{"192.0.2.1"}}}
	serverCluster := []k8s.NodeIdentity{{Name: "worker-0", Addresses: []string{"198.51.100.1"}}}
	if ok, why := sharedPromAttributable(clientNode, serverNode, clientCluster, serverCluster); !ok {
		t.Fatalf("distinct nodes rejected: %s", why)
	}
	testCases := map[string]struct {
		clientNodes, serverNodes []k8s.NodeIdentity
		client, server           metrics.NodeInfo
	}{
		"server node name also in client cluster": {
			clientNodes: append(clientCluster, k8s.NodeIdentity{Name: "worker-0"}), serverNodes: serverCluster, client: clientNode, server: serverNode},
		"regex dot matches another node": {
			clientNodes: []k8s.NodeIdentity{{Name: "n.a"}}, serverNodes: []k8s.NodeIdentity{{Name: "nxa"}},
			client: metrics.NodeInfo{NodeName: "n.a"}, server: metrics.NodeInfo{NodeName: "nxa"}},
		"same host IP": {
			clientNodes: clientCluster, serverNodes: []k8s.NodeIdentity{{Name: "worker-0", Addresses: []string{"192.0.2.1"}}},
			client: clientNode, server: metrics.NodeInfo{NodeName: "worker-0", IP: "192.0.2.1"}},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			if ok, _ := sharedPromAttributable(tc.client, tc.server, tc.clientNodes, tc.serverNodes); ok {
				t.Fatal("collision not detected")
			}
		})
	}
}

func TestCrossClusterPromEligibility(t *testing.T) {
	ctx := context.Background()
	node := func(name, ip string) *corev1.Node {
		return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}, Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: ip}}}}
	}
	clientNode := metrics.NodeInfo{NodeName: "a", IP: "192.0.2.1"}
	serverNode := metrics.NodeInfo{NodeName: "b", IP: "192.0.2.2"}
	clientCS, serverCS := fake.NewClientset(node("a", "192.0.2.1")), fake.NewClientset(node("b", "192.0.2.2"))
	collidingServer := fake.NewClientset(node("b", "192.0.2.2"), node("a", "192.0.2.9"))
	testCases := []struct {
		name                     string
		clientAvail, serverAvail bool
		clientURL, serverURL     string
		server                   kubernetes.Interface
		wantClient, wantServer   bool
	}{
		{"no server prom", true, false, "http://a", "", serverCS, true, false},
		{"no client prom", false, true, "", "http://b", serverCS, false, true},
		{"separate endpoints, even with colliding names", true, true, "http://a", "http://b", collidingServer, true, true},
		{"shared endpoint, distinct nodes", true, true, "http://p", "http://p/", serverCS, true, true},
		{"shared endpoint, client node name also on the server cluster", true, true, "http://p", "http://p", collidingServer, false, false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c, s := crossClusterPromEligibility(ctx, tc.clientAvail, tc.clientURL, tc.serverAvail, tc.serverURL, clientCS, tc.server, clientNode, serverNode)
			if c != tc.wantClient || s != tc.wantServer {
				t.Fatalf("eligibility = %t/%t, want %t/%t", c, s, tc.wantClient, tc.wantServer)
			}
		})
	}
}
