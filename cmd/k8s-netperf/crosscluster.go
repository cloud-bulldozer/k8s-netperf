package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	ocpmetadata "github.com/cloud-bulldozer/go-commons/v2/ocp-metadata"
	"github.com/cloud-bulldozer/go-commons/v2/prometheus"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/k8s"
	log "github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/metrics"
	result "github.com/cloud-bulldozer/k8s-netperf/pkg/results"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type crossClusterFlags struct {
	serverKubeconfig string
	serverDomain     string
	serverProm       string
	serverIP         string
	vm               bool
	pod              bool
	udnl2            bool
	udnl3            bool
	cudn             string
	hostNet          bool
	all              bool
	local            bool
	across           bool
	bridge           string
	sriov            string
	macvlan          string
	localnet         string
	ibWriteBw        bool
}

// validateCrossClusterFlags rejects unsupported combinations and returns the normalized --server-domain.
func validateCrossClusterFlags(f crossClusterFlags) (string, error) {
	if f.serverKubeconfig == "" {
		var errs []error
		if f.serverDomain != "" {
			errs = append(errs, fmt.Errorf("--server-domain requires --server-kubeconfig"))
		}
		if f.serverProm != "" {
			errs = append(errs, fmt.Errorf("--server-prom requires --server-kubeconfig"))
		}
		return "", errors.Join(errs...)
	}
	rejected := []struct {
		set    bool
		flag   string
		reason string
	}{
		{f.serverIP != "", "--serverIP", "it targets an external server, while cross-cluster mode deploys its own server on the --server-kubeconfig cluster"},
		{f.vm, "--vm", "VM workloads are not supported in cross-cluster mode"},
		{!f.pod, "--pod=false", "cross-cluster mode runs pod workloads only, so nothing would run"},
		{f.udnl2, "--udnl2", "user defined networks are not supported in cross-cluster mode"},
		{f.udnl3, "--udnl3", "user defined networks are not supported in cross-cluster mode"},
		{f.cudn != "", "--cudn", "user defined networks are not supported in cross-cluster mode"},
		{f.hostNet, "--hostNet", "host network tests are not supported in cross-cluster mode"},
		{f.all, "--all", "host network tests are not supported in cross-cluster mode"},
		{f.local, "--local", "it places client and server on one node, while cross-cluster mode places them on different clusters"},
		{f.across, "--across", "it selects availability zones inside one cluster"},
		{f.bridge != "", "--bridge", "secondary networks are not supported in cross-cluster mode"},
		{f.sriov != "", "--sriov", "secondary networks are not supported in cross-cluster mode"},
		{f.macvlan != "", "--macvlan", "secondary networks are not supported in cross-cluster mode"},
		{f.localnet != "", "--localnet", "secondary networks are not supported in cross-cluster mode"},
		{f.ibWriteBw, "--ib-write-bw", "ib_write_bw requires host networking, which cross-cluster mode does not support"},
	}
	var errs []error
	for _, r := range rejected {
		if r.set {
			errs = append(errs, fmt.Errorf("%s cannot be used with --server-kubeconfig: %s", r.flag, r.reason))
		}
	}
	domain := ""
	if f.serverDomain != "" {
		var err error
		if domain, err = normalizeServerDomain(f.serverDomain); err != nil {
			errs = append(errs, err)
		}
	}
	return domain, errors.Join(errs...)
}

// normalizeServerDomain lowercases, strips one trailing dot and checks every Service DNS name built from it.
func normalizeServerDomain(domain string) (string, error) {
	d := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if d == "" {
		return "", fmt.Errorf("--server-domain %q is empty", domain)
	}
	if errs := validation.IsDNS1123Subdomain(d); len(errs) > 0 {
		return "", fmt.Errorf("--server-domain %q is not a valid DNS name: %s", domain, strings.Join(errs, "; "))
	}
	for _, label := range strings.Split(d, ".") {
		if errs := validation.IsDNS1123Label(label); len(errs) > 0 {
			return "", fmt.Errorf("--server-domain %q has an invalid label %q: %s", domain, label, strings.Join(errs, "; "))
		}
	}
	for _, name := range k8s.CrossClusterServiceNames {
		fqdn := k8s.ServiceDNSNameFor(name, d)
		if len(fqdn) > validation.DNS1123SubdomainMaxLength {
			return "", fmt.Errorf("--server-domain %q is too long: Service name %s is %d characters, the DNS limit is %d", domain, fqdn, len(fqdn), validation.DNS1123SubdomainMaxLength)
		}
	}
	return d, nil
}

// validateCrossClusterConfigs checks the configs against what the cross-cluster Services can carry.
func validateCrossClusterConfigs(cfgs []config.Config, domain string, netperfEnabled bool) error {
	var errs []error
	for _, c := range cfgs {
		if !c.Service {
			continue
		}
		if domain == "" {
			errs = append(errs, fmt.Errorf("config entry %s (service: true) targets a Service on the server cluster and needs --server-domain", describeConfig(c)))
		}
		if netperfEnabled && c.Parallelism > k8s.NetperfServiceDataPorts {
			errs = append(errs, fmt.Errorf("config entry %s (service: true) uses parallelism %d, but netperf-service exposes %d netperf data ports", describeConfig(c), c.Parallelism, k8s.NetperfServiceDataPorts))
		}
	}
	return errors.Join(errs...)
}

func describeConfig(c config.Config) string {
	return fmt.Sprintf("%s/messagesize=%d/parallelism=%d", c.Profile, c.MessageSize, c.Parallelism)
}

// crossClusterServerAddress returns the server pod IP or Service DNS name, and the target variant.
func crossClusterServerAddress(nc config.Config, s config.PerfScenarios, driver string) (string, string, error) {
	if !nc.Service {
		if len(s.Server.Items) == 0 || s.Server.Items[0].Status.PodIP == "" {
			return "", "", fmt.Errorf("no server pod IP available on the server cluster")
		}
		return s.Server.Items[0].Status.PodIP, result.CrossClusterTargetPodIP, nil
	}
	var svc *corev1.Service
	switch driver {
	case "netperf":
		svc = s.NetperfService
	case "iperf3":
		svc = s.IperfService
	case "uperf":
		svc = s.UperfService
		if nc.Profile == "TCP_STREAM_LAT" {
			svc = s.UperfLatService
		}
	default:
		return "", "", fmt.Errorf("driver %s is not supported in cross-cluster mode", driver)
	}
	if svc == nil {
		return "", "", fmt.Errorf("no %s Service was created on the server cluster", driver)
	}
	if s.ServerDomain == "" {
		return "", "", fmt.Errorf("service entries need --server-domain")
	}
	return k8s.ServiceDNSName(svc, s.ServerDomain), result.CrossClusterTargetServiceDNS, nil
}

// serverCluster is the cluster of --server-kubeconfig.
type serverCluster struct {
	restConfig    *rest.Config
	clientSet     *kubernetes.Clientset
	metadataAgent *ocpmetadata.Metadata
	info          ocpmetadata.ClusterInfo
	infoDegraded  bool
}

func connectServerCluster(path string) (*serverCluster, error) {
	rules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: path}
	rc, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading --server-kubeconfig %s: %w", path, err)
	}
	cs, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("server cluster client: %w", err)
	}
	sc := &serverCluster{restConfig: rc, clientSet: cs}
	if meta, err := ocpmetadata.NewMetadata(rc); err == nil {
		sc.metadataAgent = &meta
		info, infoErr := meta.GetClusterInfo()
		sc.info = info
		if infoErr != nil {
			sc.infoDegraded = true
			log.Warnf("Server cluster info discovery degraded: %v", infoErr)
		}
	} else {
		log.Warnf("Server cluster metadata client unavailable: %v", err)
	}
	return sc, nil
}

// checkCrossClusterNodes requires a worker node on each cluster.
func checkCrossClusterNodes(ctx context.Context, client, server kubernetes.Interface) error {
	var errs []error
	for _, c := range []struct {
		name   string
		client kubernetes.Interface
	}{{"client cluster", client}, {"server cluster", server}} {
		n, err := k8s.CountWorkerNodes(ctx, c.client)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.name, err))
		} else if n == 0 {
			errs = append(errs, fmt.Errorf("%s has no node labeled node-role.kubernetes.io/worker=", c.name))
		}
	}
	return errors.Join(errs...)
}

// checkNoLeftoverNamespaces is the --clean=false preflight: no netperf namespace may exist on either cluster.
func checkNoLeftoverNamespaces(ctx context.Context, client, server kubernetes.Interface) error {
	var errs []error
	for _, c := range []struct {
		name   string
		client kubernetes.Interface
	}{{"client cluster", client}, {"server cluster", server}} {
		exists, err := k8s.NamespaceExists(ctx, c.client)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.name, err))
		} else if exists {
			errs = append(errs, fmt.Errorf("%s already has a netperf namespace; rerun with --clean to recreate it", c.name))
		}
	}
	return errors.Join(errs...)
}

// Cluster indexes in a crossClusterLifecycle.
const (
	clientClusterIdx = 0
	serverClusterIdx = 1
)

var errStopping = errors.New("cross-cluster run is stopping")

type lifecycleCluster struct {
	name   string
	client kubernetes.Interface
}

// crossClusterLifecycle coordinates creation and cleanup on both clusters. Nothing in it calls Fatal,
// because StopAndClean also runs as a Fatal exit handler.
type crossClusterLifecycle struct {
	clusters     []lifecycleCluster
	token        string
	createCtx    context.Context
	cancelCreate context.CancelFunc

	mu        sync.Mutex
	stopping  bool
	attempted []bool
	preClean  chan struct{} // open while PreClean runs
	active    sync.WaitGroup

	stopOnce sync.Once
	stopErr  error
	// signaled is set when a signal started the cleanup; that path owns the exit code.
	signaled atomic.Bool

	cleanupTimeout time.Duration
	joinTimeout    time.Duration
	deleteAny      func(ctx context.Context, c kubernetes.Interface) error
	deleteOwned    func(ctx context.Context, c kubernetes.Interface, token string) error
}

func newCrossClusterLifecycle(client, server kubernetes.Interface, token string) *crossClusterLifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	return &crossClusterLifecycle{
		clusters:       []lifecycleCluster{{"client cluster", client}, {"server cluster", server}},
		token:          token,
		createCtx:      ctx,
		cancelCreate:   cancel,
		attempted:      make([]bool, 2),
		cleanupTimeout: 5 * time.Minute,
		joinTimeout:    30 * time.Second,
		deleteAny: func(ctx context.Context, c kubernetes.Interface) error {
			return k8s.DeleteNamespaceAndWait(ctx, c, "")
		},
		deleteOwned: k8s.DeleteOwnedNamespace,
	}
}

// eachCluster runs fn on each cluster in parallel with its own deadline; one failing or hanging cluster does not stop the others.
func (l *crossClusterLifecycle) eachCluster(idx []int, fn func(ctx context.Context, c lifecycleCluster) error) error {
	errs := make([]error, len(idx))
	var wg sync.WaitGroup
	for n, i := range idx {
		wg.Add(1)
		go func(n int, c lifecycleCluster) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), l.cleanupTimeout)
			defer cancel()
			if err := fn(ctx, c); err != nil {
				log.Errorf("Cleanup on the %s failed: %v", c.name, err)
				errs[n] = fmt.Errorf("%s: %w", c.name, err)
			}
		}(n, l.clusters[i])
	}
	wg.Wait()
	return errors.Join(errs...)
}

// PreClean deletes any netperf namespace on both clusters (start of a --clean run) without blocking later creates.
func (l *crossClusterLifecycle) PreClean() error {
	l.mu.Lock()
	if l.stopping {
		l.mu.Unlock()
		return errStopping
	}
	done := make(chan struct{})
	l.preClean = done
	l.mu.Unlock()
	defer close(done)
	return l.eachCluster([]int{clientClusterIdx, serverClusterIdx}, func(ctx context.Context, c lifecycleCluster) error {
		return l.deleteAny(ctx, c.client)
	})
}

// create runs fn unless the run is stopping, marking its clusters as attempted before any request is sent.
func (l *crossClusterLifecycle) create(idx []int, fn func(ctx context.Context) error) error {
	l.mu.Lock()
	if l.stopping {
		l.mu.Unlock()
		return errStopping
	}
	for _, i := range idx {
		l.attempted[i] = true
	}
	l.active.Add(1)
	l.mu.Unlock()
	defer l.active.Done()
	return fn(l.createCtx)
}

// StopAndClean blocks and cancels creates, waits within bounds for in-flight creates and PreClean, then deletes the
// namespace with this run's owner token on each attempted cluster. It runs once; repeated and concurrent callers get its result.
func (l *crossClusterLifecycle) StopAndClean() error {
	l.stopOnce.Do(func() { l.stopErr = l.stopAndClean() })
	return l.stopErr
}

func (l *crossClusterLifecycle) stopAndClean() error {
	l.mu.Lock()
	l.stopping = true
	preClean := l.preClean
	l.mu.Unlock()

	l.cancelCreate()
	if !waitGroupTimeout(&l.active, l.joinTimeout) {
		log.Warnf("In-flight create calls did not return within %s; cleaning up anyway", l.joinTimeout)
	}
	if preClean != nil {
		select {
		case <-preClean:
		case <-time.After(l.cleanupTimeout + l.joinTimeout):
			log.Warn("Start-of-run cleanup did not finish in time")
		}
	}

	l.mu.Lock()
	var idx []int
	for i, a := range l.attempted {
		if a {
			idx = append(idx, i)
		}
	}
	l.mu.Unlock()
	if len(idx) == 0 {
		return nil
	}
	return l.eachCluster(idx, func(ctx context.Context, c lifecycleCluster) error {
		return l.deleteOwned(ctx, c.client, l.token)
	})
}

func waitGroupTimeout(wg *sync.WaitGroup, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func signalExitCode(sig os.Signal) int {
	if sig == syscall.SIGTERM {
		return 143
	}
	return 130
}

// handleCrossClusterSignals calls onSignal at once, so other exit paths defer to the signal's exit code,
// then runs stop and exits. A second signal exits without waiting for stop.
func handleCrossClusterSignals(signals <-chan os.Signal, onSignal func(), stop func() error, exit func(int)) {
	sig := <-signals
	onSignal()
	code := signalExitCode(sig)
	log.Warnf("⚠️ Received %v: cleaning up both clusters (send it again to exit without waiting)", sig)
	done := make(chan struct{})
	go func() {
		if err := stop(); err != nil {
			log.Errorf("Cleanup after %v failed: %v", sig, err)
		}
		close(done)
	}()
	select {
	case <-done:
	case again := <-signals:
		log.Warnf("Received %v again: exiting without waiting for cleanup", again)
	}
	exit(code)
}

func installCrossClusterSignalHandler(l *crossClusterLifecycle) {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go handleCrossClusterSignals(signals, l.markSignaled, l.StopAndClean, os.Exit)
}

func (l *crossClusterLifecycle) markSignaled() {
	l.signaled.Store(true)
}

// waitIfSignaled blocks forever once a signal owns the exit, so its handler exits with 130 or 143.
func (l *crossClusterLifecycle) waitIfSignaled() {
	if l.signaled.Load() {
		select {}
	}
}

// fatalExitHandler cleans both clusters on log.Fatal, leaving the exit to a signal handler that owns it.
func (l *crossClusterLifecycle) fatalExitHandler() {
	_ = l.StopAndClean()
	l.waitIfSignaled()
}

// createInfra creates the owned namespace, ServiceAccount and RoleBinding, client cluster first.
func (l *crossClusterLifecycle) createInfra() error {
	for _, idx := range []int{clientClusterIdx, serverClusterIdx} {
		c := l.clusters[idx]
		err := l.create([]int{idx}, func(ctx context.Context) error {
			return k8s.CreateOwnedInfra(ctx, c.client, l.token)
		})
		if err != nil {
			return fmt.Errorf("%s: %w", c.name, err)
		}
	}
	return nil
}

func crossClusterFlagsFromGlobals(ibWriteBwEnabled bool) crossClusterFlags {
	return crossClusterFlags{
		serverKubeconfig: serverKubeconfig,
		serverDomain:     serverDomain,
		serverProm:       serverPromURL,
		serverIP:         serverIPAddr,
		vm:               vm,
		pod:              pod,
		udnl2:            udnl2,
		udnl3:            udnl3,
		cudn:             cudn,
		hostNet:          hostNetOnly,
		all:              full,
		local:            nl,
		across:           acrossAZ,
		bridge:           bridge,
		sriov:            sriov,
		macvlan:          macvlan,
		localnet:         localnet,
		ibWriteBw:        ibWriteBwEnabled,
	}
}

// startCrossCluster runs the two-cluster checks; with --clean it installs the signal handler, pre-cleans
// and registers the Fatal cleanup handler.
func startCrossCluster(client, serverClient kubernetes.Interface, clean bool) *crossClusterLifecycle {
	ctx := context.Background()
	if err := checkCrossClusterNodes(ctx, client, serverClient); err != nil {
		log.Fatal(err)
	}
	if !clean {
		if err := checkNoLeftoverNamespaces(ctx, client, serverClient); err != nil {
			log.Fatal(err)
		}
	}
	l := newCrossClusterLifecycle(client, serverClient, uuid.New().String())
	if !clean {
		return l
	}
	installCrossClusterSignalHandler(l)
	if err := l.PreClean(); err != nil {
		// A signal during PreClean owns the exit code; its handler exits once the deletions finish.
		l.waitIfSignaled()
		log.Fatalf("Start-of-run cleanup failed: %v", err)
	}
	log.RegisterExitHandler(l.fatalExitHandler)
	return l
}

// connectPrometheus connects one cluster's Prometheus; an empty scope is the client cluster, with the old messages.
func connectPrometheus(metadataAgent *ocpmetadata.Metadata, distribution string, infoDegraded bool, url, scope string) (metrics.PromConnect, bool) {
	var pcon metrics.PromConnect
	if shouldDiscoverPrometheus(distribution, url, metadataAgent != nil, infoDegraded) {
		pcon, _ = metrics.Discover(metadataAgent)
	}
	if url != "" {
		pcon.URL = url
	}
	applyClusterDistribution(&pcon, distribution)
	unavailable := "😥 Prometheus is not available"
	if scope != "" {
		unavailable += " for the " + scope
	}
	if pcon.URL == "" {
		log.Warn(unavailable)
		return pcon, false
	}
	var err error
	pcon.Client, err = prometheus.NewClient(pcon.URL, pcon.Token, "", "", pcon.SkipTLSVerify)
	if err != nil {
		log.Warn(unavailable)
		return pcon, false
	}
	return pcon, true
}

func samePromEndpoint(a, b string) bool {
	a, b = strings.TrimRight(a, "/"), strings.TrimRight(b, "/")
	return a != "" && a == b
}

// promPatternMatches applies a PromQL =~ matcher (fully anchored RE2); an unusable pattern counts as a match.
func promPatternMatches(pattern, value string) bool {
	re, err := regexp.Compile("^(?:" + pattern + ")$")
	if err != nil {
		return true
	}
	return re.MatchString(value)
}

// nodeSeriesCollide reports whether node's queries could match another cluster's node, by name or by IP (instance=~"IP:.*").
func nodeSeriesCollide(node metrics.NodeInfo, others []k8s.NodeIdentity) (bool, string) {
	for _, o := range others {
		if node.NodeName != "" && promPatternMatches(node.NodeName, o.Name) {
			return true, o.Name
		}
		for _, addr := range o.Addresses {
			if node.IP != "" && (node.IP == addr || promPatternMatches(node.IP+":.*", addr+":0")) {
				return true, fmt.Sprintf("%s (%s)", o.Name, addr)
			}
		}
	}
	return false, ""
}

func sharedPromAttributable(clientNode, serverNode metrics.NodeInfo, clientNodes, serverNodes []k8s.NodeIdentity) (bool, string) {
	if hit, other := nodeSeriesCollide(clientNode, serverNodes); hit {
		return false, fmt.Sprintf("client node %s (%s) also matches server cluster node %s", clientNode.NodeName, clientNode.IP, other)
	}
	if hit, other := nodeSeriesCollide(serverNode, clientNodes); hit {
		return false, fmt.Sprintf("server node %s (%s) also matches client cluster node %s", serverNode.NodeName, serverNode.IP, other)
	}
	return true, ""
}

// crossClusterPromEligibility permits queries to a shared Prometheus only when node identities cannot collide across clusters.
func crossClusterPromEligibility(ctx context.Context, clientAvail bool, clientURL string, serverAvail bool, serverURL string, clientCS, serverCS kubernetes.Interface, clientNode, serverNode metrics.NodeInfo) (bool, bool) {
	if !clientAvail || !serverAvail || !samePromEndpoint(clientURL, serverURL) {
		if !serverAvail {
			log.Warn("Server-side metrics will not be collected: no Prometheus for the server cluster (set --server-prom)")
		}
		return clientAvail, serverAvail
	}
	clientNodes, err := k8s.ListNodeIdentities(ctx, clientCS)
	if err == nil {
		var serverNodes []k8s.NodeIdentity
		if serverNodes, err = k8s.ListNodeIdentities(ctx, serverCS); err == nil {
			ok, why := sharedPromAttributable(clientNode, serverNode, clientNodes, serverNodes)
			if ok {
				return true, true
			}
			log.Warnf("Prometheus metrics will not be collected: one Prometheus serves both clusters and %s, so its series cannot be attributed", why)
			return false, false
		}
	}
	log.Warnf("Prometheus metrics will not be collected: cannot check node identities for the shared Prometheus: %v", err)
	return false, false
}
