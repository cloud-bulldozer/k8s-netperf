package k8s

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	b64 "encoding/base64"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	kubevirtv1 "github.com/cloud-bulldozer/k8s-netperf/pkg/kubevirt/client-go/clientset/versioned/typed/core/v1"
	log "github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/virtctl"
	"github.com/melbahja/goph"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	v1 "kubevirt.io/api/core/v1"
)

var (
	sshPort = uint(32022)
	retry   = 30
	// vmiStartTimeout bounds all VMI startup waits, including asynchronous PVC cloning.
	vmiStartTimeout  = 10 * time.Minute
	vmiPVCBackoff    = time.Second
	vmiPVCMaxBackoff = 30 * time.Second

	offlineVMPrerequisiteTimeout    = 10 * time.Minute
	offlineVMPrerequisiteBackoff    = time.Second
	offlineVMPrerequisiteMaxBackoff = 30 * time.Second
	virtctlSSHRetries               = 3
	virtctlSSHRetryDelay            = 5 * time.Second
)

// connect will attempt to connect via ssh to the guest. The VM can take a while for sshkeys to be injected
func connect(config *goph.Config) (*goph.Client, error) {
	for i := 0; i < retry; i++ {
		client, err := goph.NewConn(config)
		if err != nil {
			log.Debug("Waiting for ssh access to be available")
			log.Debug(err)
			time.Sleep(10 * time.Second)
			continue
		} else {
			return client, nil
		}
	}
	return nil, fmt.Errorf("unable to connect via ssh after %d attempts", retry)
}

// SSHConnect sets up the ssh config, then attempts to connect to the VM.
func SSHConnect(conf *config.PerfScenarios) (*goph.Client, error) {
	dir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve users homedir. %s", err)
	}
	key := fmt.Sprintf("%s/.ssh/id_rsa", dir)
	keyd, err := os.ReadFile(key)
	if err != nil {
		return nil, fmt.Errorf("unable to read key. Error : %s", err)
	}
	auth, err := goph.RawKey(string(keyd), "")
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve sshkey. Error : %s", err)
	}
	user := "fedora"
	addr := conf.VMHost
	sshPort := sshPort
	log.Debugf("Attempting to connect with : %s@%s", user, addr)

	config := goph.Config{
		User:     user,
		Addr:     addr,
		Port:     sshPort,
		Auth:     auth,
		Callback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := connect(&config)
	if err != nil {
		return nil, fmt.Errorf("unable to connect via ssh. Error: %s", err)
	}

	return client, nil
}

// createCommService creates a SSH nodeport service using port 32022 -> 22
func createCommService(client *kubernetes.Clientset, label map[string]string, name string) error {
	log.Infof("🚀 Creating service for %s in namespace %s", name, namespace)
	sc := client.CoreV1().Services(namespace)
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    benchmarkResourceLabels(label),
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       name,
					Protocol:   corev1.ProtocolTCP,
					NodePort:   int32(sshPort),
					TargetPort: intstr.Parse(fmt.Sprintf("%d", 22)),
					Port:       22,
				},
			},
			Type:     corev1.ServiceType("NodePort"),
			Selector: label,
		},
	}
	_, err := sc.Create(context.TODO(), service, metav1.CreateOptions{})
	return err
}

// exposeService will create a route for the ssh nodeport service.
func exposeService(client *kubernetes.Clientset, dynamicClient *dynamic.DynamicClient, svcName string) (string, error) {
	route := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "route.openshift.io/v1",
			"kind":       "Route",
			"metadata": map[string]interface{}{
				"name":      fmt.Sprintf("svc-%s-route", svcName),
				"namespace": namespace,
				"labels":    map[string]interface{}{benchmarkManagedLabel: "true"},
			},
			"spec": map[string]interface{}{
				"port": map[string]interface{}{
					"targetPort": 22,
				},
				"to": map[string]interface{}{
					"kind":   "Service",
					"name":   svcName,
					"weight": 100,
				},
				"wildcardPolicy": "None",
			},
		},
	}
	route, err := dynamicClient.Resource(routeGVR).Namespace(namespace).Create(context.TODO(), route, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create route: %v", err)
	}
	retrievedRoute, err := dynamicClient.Resource(routeGVR).Namespace(namespace).Get(context.TODO(), route.GetName(), metav1.GetOptions{})
	if err != nil {
		log.Fatalf("error retrieving route: %v", err)
	}
	spec, ok := retrievedRoute.Object["spec"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("error extracting spec from route")
	}
	host, ok := spec["host"].(string)
	if !ok {
		return "", fmt.Errorf("host not found in route spec")
	}
	return host, nil
}

// CreateVMClient takes in the affinity rules and deploys the VMI
func CreateVMClient(kclient *kubevirtv1.KubevirtV1Client, client *kubernetes.Clientset,
	dyn *dynamic.DynamicClient, name string, podAff *corev1.PodAntiAffinity, nodeAff *corev1.NodeAffinity, vmimage, diskDataVolume, bridgeNetwork string, udn bool, udnPluginBinding string,
	cudn bool, localnet bool, localnetNetwork string, sriovNetwork string, sockets uint32, cores uint32, threads uint32, requestedDrivers []string, configs []config.Config) (string, error) {
	log.Debugf("CreateVMClient: localnet=%v, localnetNetwork=%s", localnet, localnetNetwork)
	label := map[string]string{
		"app":  name,
		"role": name,
	}
	dirname, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	ssh, err := os.ReadFile(fmt.Sprintf("%s/.ssh/id_rsa.pub", dirname))
	if err != nil {
		return "", err
	}
	netData := ""
	data := fmt.Sprintf(`#cloud-config
users:
  - name: fedora
    groups: sudo
    shell: /bin/bash
    sudo: ['ALL=(ALL) NOPASSWD:ALL']
    ssh_deletekeys: false
    ssh_authorized_keys:
      - %s
chpasswd:
  list: |
    fedora:fedora
  expire: False
runcmd:
  - export HOME=/home/fedora
  - until dnf install -y --nodocs uperf iperf3 git ethtool automake gcc bc lksctp-tools-devel texinfo --enablerepo=*; do sleep 3; done
  - git clone https://github.com/HewlettPackard/netperf.git
  - cd netperf
  - git reset --hard 3bc455b23f901dae377ca0a558e1e32aa56b31c4
  - curl -o netperf.diff https://raw.githubusercontent.com/cloud-bulldozer/k8s-netperf/main/containers/netperf.diff
  - git apply netperf.diff
  - ./autogen.sh
  - ./configure --enable-sctp=yes --enable-demo=yes
  - make && make install
  - cd
  - curl -o /usr/bin/super-netperf https://raw.githubusercontent.com/cloud-bulldozer/k8s-netperf/main/containers/super-netperf
  - chmod 0777 /usr/bin/super-netperf
`, ssh)
	if diskDataVolume != "" {
		data = offlineClientCloudInit(string(ssh), requestedDrivers, configs)
	}
	interfaces := []v1.Interface{
		{
			Name: "default",
			InterfaceBindingMethod: v1.InterfaceBindingMethod{
				Masquerade: &v1.InterfaceMasquerade{},
			},
		},
	}
	networks := []v1.Network{
		{
			Name: "default",
			NetworkSource: v1.NetworkSource{
				Pod: &v1.PodNetwork{},
			},
		},
	}
	if bridgeNetwork != "" {
		interfaces = append(interfaces, v1.Interface{
			Name: "br-netperf",
			InterfaceBindingMethod: v1.InterfaceBindingMethod{
				Bridge: &v1.InterfaceBridge{},
			},
		})
		networks = append(networks, v1.Network{
			Name: "br-netperf",
			NetworkSource: v1.NetworkSource{
				Multus: &v1.MultusNetwork{
					NetworkName: "netperf/br-netperf",
				},
			},
		})
		netData = fmt.Sprintf(`version: 2
ethernets:
  eth1:
    addresses: [ %s ]`, bridgeNetwork)
	} else if udn {
		interfaces = []v1.Interface{
			{
				Name: "udn-primary-netperf",
				Binding: &v1.PluginBinding{
					Name: udnPluginBinding,
				},
			},
		}
		networks = []v1.Network{
			{
				Name: "udn-primary-netperf",
				NetworkSource: v1.NetworkSource{
					Pod: &v1.PodNetwork{},
				},
			},
		}
		netData = `version: 2
ethernets:
  eth0:
    dhcp4: true`
		label["kubevirt.io/udn-binding-method"] = udnPluginBinding
	} else if cudn {
		interfaces = append(interfaces, v1.Interface{
			Name: "secondary",
			InterfaceBindingMethod: v1.InterfaceBindingMethod{
				Bridge: &v1.InterfaceBridge{},
			},
		})
		networks = append(networks, v1.Network{
			Name: "secondary",
			NetworkSource: v1.NetworkSource{
				Multus: &v1.MultusNetwork{
					NetworkName: namespace + "/" + CudnName,
				},
			},
		})
		netData = `version: 2
ethernets:
  eth1:
    dhcp4: true`
	} else if localnet {
		interfaces = append(interfaces, v1.Interface{
			Name: "secondary-localnet",
			InterfaceBindingMethod: v1.InterfaceBindingMethod{
				Bridge: &v1.InterfaceBridge{},
			},
		})
		networks = append(networks, v1.Network{
			Name: "secondary-localnet",
			NetworkSource: v1.NetworkSource{
				Multus: &v1.MultusNetwork{
					NetworkName: namespace + "/" + LocalnetCudnName,
				},
			},
		})
		netData = fmt.Sprintf(`version: 2
ethernets:
  eth1:
    addresses: [ %s ]`, localnetNetwork)
	} else if sriovNetwork != "" {
		interfaces = append(interfaces, v1.Interface{
			Name: "sriov-netperf",
			InterfaceBindingMethod: v1.InterfaceBindingMethod{
				SRIOV: &v1.InterfaceSRIOV{},
			},
		})
		networks = append(networks, v1.Network{
			Name: "sriov-netperf",
			NetworkSource: v1.NetworkSource{
				Multus: &v1.MultusNetwork{
					NetworkName: namespace + "/" + SriovNadName,
				},
			},
		})
	}
	_, err = CreateVMI(kclient, name, benchmarkResourceLabels(label), b64.StdEncoding.EncodeToString([]byte(data)), *podAff, *nodeAff, vmimage, diskDataVolume, interfaces, networks, networkDataBase64(netData), sriovNetwork, sockets, cores, threads)
	if err != nil {
		return "", err
	}
	if strings.Contains(name, "host") {
		err = createCommService(client, label, fmt.Sprintf("%s-svc", name))
	} else {
		err = createCommService(client, label, fmt.Sprintf("%s-svc", name))
	}
	if err != nil {
		return "", err
	}
	host, err := exposeService(client, dyn, fmt.Sprintf("%s-svc", name))
	if err != nil {
		return "", err
	}
	return host, nil
}

// CreateVMServer will take the pod and node affinity and deploy the VMI
func CreateVMServer(client *kubevirtv1.KubevirtV1Client, name string, role string, podAff corev1.PodAntiAffinity,
	nodeAff corev1.NodeAffinity, vmimage, diskDataVolume, bridgeNetwork string, udn bool, udnPluginBinding string, cudn bool,
	localnet bool, localnetNetwork string,
	sriovNetwork string, sockets uint32, cores uint32, threads uint32, requestedDrivers []string, configs []config.Config) (*v1.VirtualMachineInstance, error) {
	log.Debugf("CreateVMServer: localnet=%v, localnetNetwork=%s", localnet, localnetNetwork)
	label := map[string]string{
		"app":  name,
		"role": role,
	}
	netData := ""
	dirname, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	ssh, err := os.ReadFile(fmt.Sprintf("%s/.ssh/id_rsa.pub", dirname))
	if err != nil {
		return nil, err
	}
	data := fmt.Sprintf(`#cloud-config
users:
  - name: fedora
    ssh_deletekeys: false
    groups: sudo
    shell: /bin/bash
    sudo: ['ALL=(ALL) NOPASSWD:ALL']
    ssh_authorized_keys:
      - %s
chpasswd:
  list: |
    fedora:fedora
  expire: False
runcmd:
  - export HOME=/home/fedora
  - until dnf install -y --nodocs uperf iperf3 git ethtool automake gcc bc lksctp-tools-devel texinfo --enablerepo=*; do sleep 3; done
  - git clone https://github.com/HewlettPackard/netperf.git
  - cd netperf
  - git reset --hard 3bc455b23f901dae377ca0a558e1e32aa56b31c4
  - curl -o netperf.diff https://raw.githubusercontent.com/cloud-bulldozer/k8s-netperf/main/containers/netperf.diff
  - git apply netperf.diff 
  - ./autogen.sh 
  - ./configure --enable-sctp=yes --enable-demo=yes 
  - make && make install
  - cd
  - uperf -s -v -P %d &
  - iperf3 -s -p %d &
  - netserver &
`, string(ssh), UperfServerCtlPort, IperfServerCtlPort)
	if diskDataVolume != "" {
		data = offlineServerCloudInit(string(ssh), requestedDrivers, configs)
	}
	interfaces := []v1.Interface{
		{
			Name: "default",
			InterfaceBindingMethod: v1.InterfaceBindingMethod{
				Masquerade: &v1.InterfaceMasquerade{},
			},
		},
	}
	networks := []v1.Network{
		{
			Name: "default",
			NetworkSource: v1.NetworkSource{
				Pod: &v1.PodNetwork{},
			},
		},
	}
	if bridgeNetwork != "" {
		interfaces = append(interfaces, v1.Interface{
			Name: "br-netperf",
			InterfaceBindingMethod: v1.InterfaceBindingMethod{
				Bridge: &v1.InterfaceBridge{},
			},
		})
		networks = append(networks, v1.Network{
			Name: "br-netperf",
			NetworkSource: v1.NetworkSource{
				Multus: &v1.MultusNetwork{
					NetworkName: "netperf/br-netperf",
				},
			},
		})
		netData = fmt.Sprintf(`version: 2
ethernets:
  eth1:
    addresses: [ %s ]`, bridgeNetwork)
	} else if udn {
		interfaces = []v1.Interface{
			{
				Name: "primary-l2-net",
				Binding: &v1.PluginBinding{
					Name: udnPluginBinding,
				},
			},
		}
		networks = []v1.Network{
			{
				Name: "primary-l2-net",
				NetworkSource: v1.NetworkSource{
					Pod: &v1.PodNetwork{},
				},
			},
		}
		netData = `version: 2
ethernets:
  eth0:
    dhcp4: true`
		label["kubevirt.io/udn-binding-method"] = udnPluginBinding
	} else if cudn {
		interfaces = append(interfaces, v1.Interface{
			Name: "secondary",
			InterfaceBindingMethod: v1.InterfaceBindingMethod{
				Bridge: &v1.InterfaceBridge{},
			},
		})
		networks = append(networks, v1.Network{
			Name: "secondary",
			NetworkSource: v1.NetworkSource{
				Multus: &v1.MultusNetwork{
					NetworkName: namespace + "/" + CudnName,
				},
			},
		})
		netData = `version: 2
ethernets:
  eth1:
    dhcp4: true`
	} else if localnet {
		interfaces = append(interfaces, v1.Interface{
			Name: "secondary-localnet",
			InterfaceBindingMethod: v1.InterfaceBindingMethod{
				Bridge: &v1.InterfaceBridge{},
			},
		})
		networks = append(networks, v1.Network{
			Name: "secondary-localnet",
			NetworkSource: v1.NetworkSource{
				Multus: &v1.MultusNetwork{
					NetworkName: namespace + "/" + LocalnetCudnName,
				},
			},
		})
		netData = fmt.Sprintf(`version: 2
ethernets:
  eth1:
    addresses: [ %s ]`, localnetNetwork)
	} else if sriovNetwork != "" {
		interfaces = append(interfaces, v1.Interface{
			Name: "sriov-netperf",
			InterfaceBindingMethod: v1.InterfaceBindingMethod{
				SRIOV: &v1.InterfaceSRIOV{},
			},
		})
		networks = append(networks, v1.Network{
			Name: "sriov-netperf",
			NetworkSource: v1.NetworkSource{
				Multus: &v1.MultusNetwork{
					NetworkName: namespace + "/" + SriovNadName,
				},
			},
		})
	}
	return CreateVMI(client, name, benchmarkResourceLabels(label), b64.StdEncoding.EncodeToString([]byte(data)), podAff, nodeAff, vmimage, diskDataVolume, interfaces, networks, networkDataBase64(netData), sriovNetwork, sockets, cores, threads)
}

func networkDataBase64(networkData string) string {
	if strings.TrimSpace(networkData) == "" || strings.TrimSpace(networkData) == "{}" {
		return ""
	}
	return b64.StdEncoding.EncodeToString([]byte(networkData))
}

func offlineClientCloudInit(sshKey string, drivers []string, configs []config.Config) string {
	return fmt.Sprintf(`#cloud-config
ssh_pwauth: false
disable_root: true
users:
  - name: fedora
    groups: sudo
    shell: /bin/bash
    sudo: ['ALL=(ALL) NOPASSWD:ALL']
    lock_passwd: true
    ssh_authorized_keys:
      - %s
runcmd:
  - sh -c 'install -d /etc/ssh/sshd_config.d; printf "PasswordAuthentication no\\nPermitRootLogin no\\n" > /etc/ssh/sshd_config.d/99-k8s-netperf-security.conf; systemctl reload sshd || true'
  - sh -c '%s'
`, sshKey, offlinePrerequisiteCommand(false, drivers, configs))
}

func offlineServerCloudInit(sshKey string, drivers []string, configs []config.Config) string {
	commands := []string{
		`sh -c 'install -d /etc/ssh/sshd_config.d; printf "PasswordAuthentication no\nPermitRootLogin no\n" > /etc/ssh/sshd_config.d/99-k8s-netperf-security.conf; systemctl reload sshd || true'`,
		fmt.Sprintf("sh -c '%s'", offlinePrerequisiteCommand(true, drivers, configs)),
	}
	if containsDriver(drivers, "uperf") {
		if requiresUperfHistogram(configs) {
			commands = append(commands, fmt.Sprintf("/opt/uperf-histogram/bin/uperf -s -v -P %d &", UperfLatServerCtlPort))
		}
		if requiresRegularUperf(configs) {
			commands = append(commands, fmt.Sprintf("uperf -s -v -P %d &", UperfServerCtlPort))
		}
	}
	if containsDriver(drivers, "iperf3") {
		commands = append(commands, fmt.Sprintf("iperf3 -s -p %d &", IperfServerCtlPort))
	}
	if containsDriver(drivers, "netperf") {
		commands = append(commands, "netserver &")
	}
	return fmt.Sprintf(`#cloud-config
ssh_pwauth: false
disable_root: true
users:
  - name: fedora
    groups: sudo
    shell: /bin/bash
    sudo: ['ALL=(ALL) NOPASSWD:ALL']
    lock_passwd: true
    ssh_authorized_keys:
      - %s
%s`, sshKey, cloudInitCommands(commands))
}

func offlinePrerequisiteCommand(server bool, drivers []string, configs []config.Config) string {
	tools := offlineVMTools(server, drivers, configs)
	checks := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool == "/opt/uperf-histogram/bin/uperf" {
			checks = append(checks, fmt.Sprintf("test -x %s || { echo offline-prerequisite-missing:%s; exit 1; }", tool, tool))
			continue
		}
		checks = append(checks, fmt.Sprintf("command -v %s || { echo offline-prerequisite-missing:%s; exit 1; }", tool, tool))
	}
	return strings.Join(checks, "; ")
}

func cloudInitCommands(commands []string) string {
	var b strings.Builder
	b.WriteString("runcmd:\n")
	for _, command := range commands {
		fmt.Fprintf(&b, "  - %s\n", command)
	}
	return b.String()
}

func offlineVMTools(server bool, drivers []string, configs []config.Config) []string {
	tools := make([]string, 0, len(drivers)+1)
	for _, driver := range drivers {
		switch driver {
		case "netperf":
			if server {
				tools = append(tools, "netserver")
			} else {
				tools = append(tools, "netperf", "super-netperf")
			}
		case "iperf3":
			tools = append(tools, "iperf3")
		case "uperf":
			if requiresUperfHistogram(configs) {
				tools = append(tools, "/opt/uperf-histogram/bin/uperf")
			}
			if requiresRegularUperf(configs) {
				tools = append(tools, "uperf")
			}
		}
	}
	return tools
}

func containsDriver(drivers []string, wanted string) bool {
	for _, driver := range drivers {
		if driver == wanted {
			return true
		}
	}
	return false
}

func requiresUperfHistogram(configs []config.Config) bool {
	for _, cfg := range configs {
		if cfg.Profile == "TCP_STREAM_LAT" {
			return true
		}
	}
	return false
}

func requiresRegularUperf(configs []config.Config) bool {
	if len(configs) == 0 {
		return true
	}
	for _, cfg := range configs {
		if cfg.Profile != "TCP_STREAM_LAT" {
			return true
		}
	}
	return false
}

// CreateVMI creates the desired Virtual Machine instance with the cloud-init config with affinity.
func CreateVMI(client *kubevirtv1.KubevirtV1Client, name string, label map[string]string, b64data string, podAff corev1.PodAntiAffinity,
	nodeAff corev1.NodeAffinity, vmimage, diskDataVolume string, interfaces []v1.Interface, networks []v1.Network, netDatab64 string,
	sriovNetwork string, sockets uint32, cores uint32, threads uint32) (*v1.VirtualMachineInstance, error) {
	vmi := newVMI(name, label, b64data, podAff, nodeAff, vmimage, diskDataVolume, interfaces, networks, netDatab64, sriovNetwork, sockets, cores, threads)
	vmi, err := client.VirtualMachineInstances(namespace).Create(context.TODO(), vmi, metav1.CreateOptions{})
	if err != nil {
		return vmi, err
	}
	return vmi, nil
}

func newVMI(name string, label map[string]string, b64data string, podAff corev1.PodAntiAffinity,
	nodeAff corev1.NodeAffinity, vmimage, diskDataVolume string, interfaces []v1.Interface, networks []v1.Network, netDatab64 string,
	sriovNetwork string, sockets uint32, cores uint32, threads uint32) *v1.VirtualMachineInstance {
	delSeconds := int64(0)
	mutliQ := true
	resourceRequests := corev1.ResourceList{
		corev1.ResourceMemory: resource.MustParse("4096Mi"),
		corev1.ResourceCPU:    resource.MustParse("500m"),
	}
	if sriovNetwork != "" {
		resourceRequests[corev1.ResourceName("openshift.io/"+sriovNetwork)] = resource.MustParse("1")
	}
	diskSource := v1.VolumeSource{ContainerDisk: &v1.ContainerDiskSource{Image: vmimage}}
	if diskDataVolume != "" {
		diskSource = v1.VolumeSource{DataVolume: &v1.DataVolumeSource{Name: diskDataVolume}}
	}
	vmi := &v1.VirtualMachineInstance{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1.GroupVersion.String(),
			Kind:       "VirtualMachineInstance",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    label,
		},
		Spec: v1.VirtualMachineInstanceSpec{
			Affinity: &corev1.Affinity{
				PodAntiAffinity: &podAff,
				NodeAffinity:    &nodeAff,
			},
			TerminationGracePeriodSeconds: &delSeconds,
			Domain: v1.DomainSpec{
				Resources: v1.ResourceRequirements{
					Requests: resourceRequests,
				},
				CPU: &v1.CPU{
					Sockets: sockets,
					Cores:   cores,
					Threads: threads,
				},
				Devices: v1.Devices{
					NetworkInterfaceMultiQueue: &mutliQ,
					Disks: []v1.Disk{
						{
							Name: "disk0",
							DiskDevice: v1.DiskDevice{
								Disk: &v1.DiskTarget{
									Bus: "virtio",
								},
							},
						},
					},
					Interfaces: interfaces,
				},
			},
			Networks: networks,
			Volumes: []v1.Volume{
				{
					Name:         "disk0",
					VolumeSource: diskSource,
				},
				{
					Name: "cloudinit",
					VolumeSource: v1.VolumeSource{
						CloudInitNoCloud: &v1.CloudInitNoCloudSource{
							UserDataBase64:    b64data,
							NetworkDataBase64: netDatab64,
						},
					},
				},
			},
		},
	}
	return vmi
}

// WaitForVMI will wait until the resource is in Running state.
func WaitForVMI(client kubevirtv1.KubevirtV1Interface, name string) error {
	log.Infof("⏰ Wating for VMI (%s) to be in state running", name)
	ctx, cancel := context.WithTimeout(context.Background(), vmiStartTimeout)
	defer cancel()
	vmiClient := client.VirtualMachineInstances(namespace)
	vmi, err := vmiClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get VMI %s: %w", name, err)
	}
	if vmi.Status.Phase == v1.Running {
		return nil
	}
	if err := vmiStartError(vmi); err != nil {
		return err
	}
	if hasFailedPVCNotFound(vmi) {
		return waitForFailedPVCNotFound(ctx, name, func(ctx context.Context) (*v1.VirtualMachineInstance, error) {
			return vmiClient.Get(ctx, name, metav1.GetOptions{})
		}, sleepWithContext)
	}

	vmw, err := vmiClient.Watch(ctx, metav1.ListOptions{ResourceVersion: vmi.ResourceVersion})
	if err != nil {
		return err
	}
	defer vmw.Stop()
	for {
		select {
		case event, ok := <-vmw.ResultChan():
			if !ok {
				if err := ctx.Err(); err != nil {
					return fmt.Errorf("timed out waiting for VMI %s to run: %w", name, err)
				}
				return fmt.Errorf("VMI watch closed before %s was running", name)
			}
			if event.Type == watch.Error {
				return fmt.Errorf("watching VMI %q: %w", name, apierrors.FromObject(event.Object))
			}
			if event.Type == watch.Deleted {
				return fmt.Errorf("VMI %q was deleted before it was running", name)
			}
			d, ok := event.Object.(*v1.VirtualMachineInstance)
			if !ok {
				return fmt.Errorf("unable to watch VMI %s", name)
			}
			if d.Name == name {
				log.Debugf("Found in state (%s)", d.Status.Phase)
				if d.Status.Phase == v1.Running {
					return nil
				}
				if err := vmiStartError(d); err != nil {
					return err
				}
				if hasFailedPVCNotFound(d) {
					return waitForFailedPVCNotFound(ctx, name, func(ctx context.Context) (*v1.VirtualMachineInstance, error) {
						return vmiClient.Get(ctx, name, metav1.GetOptions{})
					}, sleepWithContext)
				}
			}
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for VMI %s to run: %w", name, ctx.Err())
		}
	}
}

// waitForFailedPVCNotFound polls after a missing clone PVC is reported. KubeVirt
// retries this condition asynchronously, so watching alone can leave us waiting
// on an update that never arrives. The caller owns the context deadline.
func waitForFailedPVCNotFound(ctx context.Context, name string, get func(context.Context) (*v1.VirtualMachineInstance, error), sleep func(context.Context, time.Duration) error) error {
	delay := vmiPVCBackoff
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("timed out waiting for VMI %s to run: %w", name, err)
		}
		vmi, err := get(ctx)
		if err != nil {
			return fmt.Errorf("get VMI %s: %w", name, err)
		}
		if vmi.Status.Phase == v1.Running {
			return nil
		}
		if err := vmiStartError(vmi); err != nil {
			return err
		}
		if err := sleep(ctx, delay); err != nil {
			return fmt.Errorf("timed out waiting for VMI %s to run: %w", name, err)
		}

		if delay < vmiPVCMaxBackoff {
			delay *= 2
			if delay > vmiPVCMaxBackoff {
				delay = vmiPVCMaxBackoff
			}
		}
	}
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func vmiStartError(vmi *v1.VirtualMachineInstance) error {
	if vmi.Status.Phase == v1.Failed {
		if vmi.Status.Reason != "" {
			return fmt.Errorf("VMI %s failed to start: %s", vmi.Name, vmi.Status.Reason)
		}
		return fmt.Errorf("VMI %s failed to start", vmi.Name)
	}
	for _, condition := range vmi.Status.Conditions {
		if condition.Type == v1.VirtualMachineInstanceSynchronized && condition.Status == corev1.ConditionFalse {
			if strings.EqualFold(condition.Reason, "FailedPvcNotfound") {
				continue
			}
			return fmt.Errorf("VMI %s failed to synchronize: %s: %s", vmi.Name, condition.Reason, condition.Message)
		}
	}
	return nil
}

func hasFailedPVCNotFound(vmi *v1.VirtualMachineInstance) bool {
	for _, condition := range vmi.Status.Conditions {
		if condition.Type == v1.VirtualMachineInstanceSynchronized &&
			condition.Status == corev1.ConditionFalse &&
			strings.EqualFold(condition.Reason, "FailedPvcNotfound") {
			return true
		}
	}
	return false
}

// VirtctlClient implements VMExecutor interface using virtctl ssh
type VirtctlClient struct {
	vmName    string
	namespace string
}

// SSHClientWrapper wraps the existing goph.Client to implement VMExecutor interface
type SSHClientWrapper struct {
	Client *goph.Client
}

// Run executes a command using the wrapped SSH client
func (s *SSHClientWrapper) Run(command string) ([]byte, error) {
	return s.Client.Run(command)
}

// Close closes the SSH connection
func (s *SSHClientWrapper) Close() error {
	return s.Client.Close()
}

// NewVirtctlClient creates a new virtctl client for VM access
func NewVirtctlClient(vmName, namespace string) *VirtctlClient {
	return &VirtctlClient{
		vmName:    vmName,
		namespace: namespace,
	}
}

// Run executes a command on the VM using virtctl ssh
func (v *VirtctlClient) Run(command string) ([]byte, error) {
	return v.RunContext(context.Background(), command)
}

// RunContext executes a command on the VM using virtctl ssh until ctx is canceled.
func (v *VirtctlClient) RunContext(ctx context.Context, command string) ([]byte, error) {
	virtctlPath, err := virtctl.GetVirtctlPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get virtctl binary: %v", err)
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("unable to retrieve users homedir: %v", err)
	}
	identityFile := fmt.Sprintf("%s/.ssh/id_rsa", dir)
	log.Debugf("Running virtctl SSH command against VMI %s in namespace %s: %s", v.vmName, v.namespace, command)
	args := virtctlSSHArgs(v.namespace, identityFile, command, v.vmName)
	stdout, err := runVirtctlSSHWithRetries(ctx, func() ([]byte, error) {
		cmd := exec.CommandContext(ctx, virtctlPath, args...)
		log.Debugf("Command: %s", cmd.String())
		stdout, err := cmd.Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return stdout, &virtctlSSHError{err: exitErr, stderr: string(exitErr.Stderr)}
			}
			return stdout, fmt.Errorf("failed to run command: %v", err)
		}
		return stdout, nil
	}, sleepWithContext)
	if err != nil {
		return stdout, err
	}
	log.Debugf("Output: %s", string(stdout))
	return stdout, nil
}

type virtctlSSHError struct {
	err    error
	stderr string
}

func (e *virtctlSSHError) Error() string {
	return fmt.Sprintf("failed to run command: %v, stderr: %s", e.err, e.stderr)
}

func (e *virtctlSSHError) Unwrap() error {
	return e.err
}

func virtctlSSHArgs(namespace, identityFile, command, vmName string) []string {
	return []string{
		"ssh", "--namespace", namespace,
		"--known-hosts", "/dev/null",
		"--local-ssh-opts", "-o BatchMode=yes",
		"--local-ssh-opts", "-o StrictHostKeyChecking=no",
		"--local-ssh-opts", "-o UserKnownHostsFile=/dev/null",
		"--local-ssh-opts", "-o GlobalKnownHostsFile=/dev/null",
		"--identity-file", identityFile,
		"-c", command,
		fmt.Sprintf("fedora@vmi/%s", vmName),
	}
}

func runVirtctlSSHWithRetries(ctx context.Context, run func() ([]byte, error), sleep func(context.Context, time.Duration) error) ([]byte, error) {
	var stdout []byte
	var err error
	for attempt := 0; attempt <= virtctlSSHRetries; attempt++ {
		if err = ctx.Err(); err != nil {
			return stdout, err
		}
		log.Debugf("Starting virtctl ssh attempt %d/%d", attempt+1, virtctlSSHRetries+1)
		stdout, err = run()
		if err == nil {
			log.Debugf("virtctl ssh attempt %d/%d succeeded", attempt+1, virtctlSSHRetries+1)
			return stdout, nil
		}
		if attempt == virtctlSSHRetries {
			break
		}
		log.Debugf("virtctl ssh attempt %d/%d failed: %v; retrying in %s", attempt+1, virtctlSSHRetries+1, err, virtctlSSHRetryDelay)
		if sleepErr := sleep(ctx, virtctlSSHRetryDelay); sleepErr != nil {
			return stdout, sleepErr
		}
	}
	return stdout, fmt.Errorf("virtctl ssh failed after %d retries: %w", virtctlSSHRetries, err)
}

// Close is a no-op for compatibility with VMExecutor interface
func (v *VirtctlClient) Close() error {
	return nil
}

// ConfigureVMSriovIP extracts the SR-IOV IP from the virt-launcher pod's network-status
// annotation and configures it inside the guest VM via virtctl ssh.
func ConfigureVMSriovIP(vmName string, pod corev1.Pod) error {
	ip, err := ExtractSriovIp(pod)
	if err != nil {
		return fmt.Errorf("failed to extract SR-IOV IP for VM %s: %v", vmName, err)
	}
	log.Infof("Configuring SR-IOV IP %s/24 on VM %s", ip, vmName)
	vc := NewVirtctlClient(vmName, namespace)
	// Wait for cloud-init to finish and SSH to be available
	for i := 0; i < retry; i++ {
		_, err = vc.Run("echo ready")
		if err == nil {
			break
		}
		log.Debugf("Waiting for SSH on VM %s (%d/%d)", vmName, i+1, retry)
		time.Sleep(10 * time.Second)
	}
	if err != nil {
		return fmt.Errorf("SSH not available on VM %s after %d attempts: %v", vmName, retry, err)
	}
	// Find the SR-IOV interface name (non-default, non-loopback)
	// With VFIO passthrough it typically appears as enp<X>s0 or eth1
	out, err := vc.Run("ls /sys/class/net | grep -v -e lo -e eth0 | head -1")
	if err != nil {
		return fmt.Errorf("failed to find SR-IOV interface on VM %s: %v, output: %s", vmName, err, string(out))
	}
	iface := strings.TrimSpace(string(out))
	if iface == "" {
		return fmt.Errorf("no SR-IOV interface found on VM %s", vmName)
	}
	log.Infof("Found SR-IOV interface %s on VM %s", iface, vmName)
	// Configure the IP address on the SR-IOV interface using nmcli for persistence
	cmd := fmt.Sprintf("sudo nmcli con add type ethernet ifname %s con-name sriov-netperf ip4 %s/24 && sudo nmcli con up sriov-netperf", iface, ip)
	out, err = vc.Run(cmd)
	if err != nil {
		return fmt.Errorf("failed to configure SR-IOV IP on VM %s: %v, output: %s", vmName, err, string(out))
	}
	log.Infof("Successfully configured SR-IOV IP %s on %s in VM %s", ip, iface, vmName)
	return nil
}

// ConnectToVM creates either SSH or virtctl connection based on configuration
func ConnectToVM(conf *config.PerfScenarios) (config.VMExecutor, error) {
	if conf.UseVirtctl && conf.VMName != "" {
		log.Debugf("Connecting to VM %s using virtctl", conf.VMName)
		return NewVirtctlClient(conf.VMName, namespace), nil
	} else {
		log.Debugf("Connecting to VM %s using SSH", conf.VMHost)
		sshClient, err := SSHConnect(conf)
		if err != nil {
			return nil, err
		}
		return &SSHClientWrapper{Client: sshClient}, nil
	}
}

// ValidateOfflineVMPrerequisites confirms that requested benchmark client tools are present in an offline guest.
// It never installs missing software; the prepared DataVolume is the sole source of guest dependencies.
func ValidateOfflineVMPrerequisites(executor config.VMExecutor, drivers []string, configs []config.Config) error {
	return validateOfflineVMPrerequisites(executor, "client", false, drivers, configs)
}

// ValidateOfflineVMServerPrerequisites confirms that requested benchmark server tools are present in an offline guest.
func ValidateOfflineVMServerPrerequisites(executor config.VMExecutor, drivers []string, configs []config.Config) error {
	if virtctlExecutor, ok := executor.(*VirtctlClient); ok {
		ctx, cancel := context.WithTimeout(context.Background(), offlineVMPrerequisiteTimeout)
		defer cancel()
		return waitForOfflineVMServerPrerequisites(ctx, virtctlExecutor.RunContext, drivers, configs, sleepWithContext)
	}
	return validateOfflineVMPrerequisites(executor, "server", true, drivers, configs)
}

func validateOfflineVMPrerequisites(executor config.VMExecutor, role string, server bool, drivers []string, configs []config.Config) error {
	return validateOfflineVMPrerequisitesWithRun(func(_ context.Context, command string) ([]byte, error) {
		return executor.Run(command)
	}, context.Background(), role, server, drivers, configs)
}

func validateOfflineVMPrerequisitesWithRun(run func(context.Context, string) ([]byte, error), ctx context.Context, role string, server bool, drivers []string, configs []config.Config) error {
	tools := offlineVMTools(server, drivers, configs)
	if len(tools) == 0 {
		return nil
	}
	command := offlinePrerequisiteCommand(server, drivers, configs)
	if output, err := run(ctx, command); err != nil {
		if tool := offlineMissingTool(string(output)); tool != "" {
			return fmt.Errorf("offline VM %s prerequisite missing: %s", role, tool)
		}
		return fmt.Errorf("offline VM %s prerequisite validation failed; prepare the source DataVolume with required benchmark software: %w (output: %s)", role, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// waitForOfflineVMServerPrerequisites waits for cloud-final through virtctl before
// checking server tools. A VMI can report Running while cloud-init is still
// executing the commands that install or start those tools.
func waitForOfflineVMServerPrerequisites(ctx context.Context, run func(context.Context, string) ([]byte, error), drivers []string, configs []config.Config, sleep func(context.Context, time.Duration) error) error {
	if len(offlineVMTools(true, drivers, configs)) == 0 {
		return nil
	}
	delay := offlineVMPrerequisiteBackoff
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("offline VM server prerequisite validation timed out waiting for cloud-init: %w", err)
		}

		if output, err := run(ctx, "cloud-init status --wait"); err == nil || cloudInitRecoverableError(output, err) {
			if err != nil {
				log.Warnf("cloud-init completed with recoverable errors on offline VM server: %v", err)
			}
			return validateOfflineVMPrerequisitesWithRun(run, ctx, "server", true, drivers, configs)
		} else if tool := offlineMissingTool(string(output)); tool != "" {
			return fmt.Errorf("offline VM server prerequisite missing: %s", tool)
		} else if ctx.Err() != nil {
			return fmt.Errorf("offline VM server prerequisite validation timed out waiting for cloud-init: %w", ctx.Err())
		} else {
			log.Debugf("Waiting for cloud-init on offline VM server: %v", err)
		}

		if err := sleep(ctx, delay); err != nil {
			return fmt.Errorf("offline VM server prerequisite validation timed out waiting for cloud-init: %w", err)
		}
		if delay < offlineVMPrerequisiteMaxBackoff {
			delay *= 2
			if delay > offlineVMPrerequisiteMaxBackoff {
				delay = offlineVMPrerequisiteMaxBackoff
			}
		}
	}
}

func cloudInitRecoverableError(output []byte, err error) bool {
	var virtctlErr *virtctlSSHError
	if !errors.As(err, &virtctlErr) {
		return false
	}
	var exitErr interface{ ExitCode() int }
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 1 &&
		(strings.Contains(string(output), "exit status 2") || strings.Contains(virtctlErr.stderr, "exit status 2"))
}

func offlineMissingTool(output string) string {
	const marker = "offline-prerequisite-missing:"
	_, tool, found := strings.Cut(strings.TrimSpace(output), marker)
	if !found || len(strings.Fields(tool)) == 0 {
		return ""
	}
	return strings.Fields(tool)[0]
}
