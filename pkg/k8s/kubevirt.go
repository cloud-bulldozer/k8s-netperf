package k8s

import (
	"context"
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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	v1 "kubevirt.io/api/core/v1"
)

var (
	sshPortAcross = uint(32022)
	sshPortLocal  = uint(32322)
	retry         = 30
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
	sshPort := sshPortAcross
	if conf.HostNetwork {
		sshPort = sshPortLocal
	}
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
func createCommService(client *kubernetes.Clientset, label map[string]string, name string, sshPort uint) error {
	log.Infof("🚀 Creating service for %s in namespace %s", name, namespace)
	sc := client.CoreV1().Services(namespace)
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
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
	gvr := schema.GroupVersionResource{
		Group:    "route.openshift.io",
		Version:  "v1",
		Resource: "routes",
	}

	route := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "route.openshift.io/v1",
			"kind":       "Route",
			"metadata": map[string]interface{}{
				"name":      fmt.Sprintf("svc-%s-route", svcName),
				"namespace": namespace,
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
	route, err := dynamicClient.Resource(gvr).Namespace(namespace).Create(context.TODO(), route, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create route: %v", err)
	}
	retrievedRoute, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(context.TODO(), route.GetName(), metav1.GetOptions{})
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
	dyn *dynamic.DynamicClient, name string, podAff *corev1.PodAntiAffinity, nodeAff *corev1.NodeAffinity, vmimage string, bridgeNetwork string, udn bool, udnPluginBinding string,
	cudn bool, sockets uint32, cores uint32, threads uint32) (string, error) {
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
	netData := "{}"
	data := fmt.Sprintf(`#cloud-config
users:
  - name: fedora
    groups: sudo
    shell: /bin/bash
    ssh_deletekeys: false
    ssh_authorized_keys:
      - %s
chpasswd:
  list: |
    fedora:fedora
  expire: False
runcmd:
  - export HOME=/home/fedora
  - dnf install -y --nodocs uperf iperf3 git ethtool automake gcc bc lksctp-tools-devel texinfo --enablerepo=*
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
	interfaces := []v1.Interface{
		{
			Name: "default",
			InterfaceBindingMethod: v1.InterfaceBindingMethod{
				Bridge: &v1.InterfaceBridge{},
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
	}
	_, err = CreateVMI(kclient, name, label, b64.StdEncoding.EncodeToString([]byte(data)), *podAff, *nodeAff, vmimage, interfaces, networks, b64.StdEncoding.EncodeToString([]byte(netData)), sockets, cores, threads)
	if err != nil {
		return "", err
	}
	if strings.Contains(name, "host") {
		err = createCommService(client, label, fmt.Sprintf("%s-svc", name), sshPortLocal)
	} else {
		err = createCommService(client, label, fmt.Sprintf("%s-svc", name), sshPortAcross)
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
	nodeAff corev1.NodeAffinity, vmimage string, bridgeNetwork string, udn bool, udnPluginBinding string, cudn bool,
	sockets uint32, cores uint32, threads uint32) (*v1.VirtualMachineInstance, error) {
	label := map[string]string{
		"app":  name,
		"role": role,
	}
	netData := "{}"
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
    ssh_authorized_keys:
      - %s
chpasswd:
  list: |
    fedora:fedora
  expire: False
runcmd:
  - dnf install -y --nodocs uperf iperf3 git ethtool
  - dnf install -y --nodocs automake gcc bc lksctp-tools-devel texinfo --enablerepo=*
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
	interfaces := []v1.Interface{
		{
			Name: "default",
			InterfaceBindingMethod: v1.InterfaceBindingMethod{
				Bridge: &v1.InterfaceBridge{},
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
	}
	return CreateVMI(client, name, label, b64.StdEncoding.EncodeToString([]byte(data)), podAff, nodeAff, vmimage, interfaces, networks, b64.StdEncoding.EncodeToString([]byte(netData)), sockets, cores, threads)
}

// CreateVMI creates the desired Virtual Machine instance with the cloud-init config with affinity.
func CreateVMI(client *kubevirtv1.KubevirtV1Client, name string, label map[string]string, b64data string, podAff corev1.PodAntiAffinity,
	nodeAff corev1.NodeAffinity, vmimage string, interfaces []v1.Interface, networks []v1.Network, netDatab64 string,
	sockets uint32, cores uint32, threads uint32) (*v1.VirtualMachineInstance, error) {
	delSeconds := int64(0)
	mutliQ := true
	vmi, err := client.VirtualMachineInstances(namespace).Create(context.TODO(), &v1.VirtualMachineInstance{
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
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("4096Mi"),
						corev1.ResourceCPU:    resource.MustParse("500m"),
					},
				},
				CPU: &v1.CPU{
					Sockets: sockets,
					Cores:   cores,
					Threads: threads,
				},
				Devices: v1.Devices{
					NetworkInterfaceMultiQueue: &mutliQ,
					Disks: []v1.Disk{
						v1.Disk{
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
				v1.Volume{
					Name: "disk0",
					VolumeSource: v1.VolumeSource{
						ContainerDisk: &v1.ContainerDiskSource{
							Image: vmimage,
						},
					},
				},
				v1.Volume{
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
	}, metav1.CreateOptions{})
	if err != nil {
		return vmi, err
	}
	return vmi, nil
}

// WaitForVMI will wait until the resource is in Running state.
func WaitForVMI(client *kubevirtv1.KubevirtV1Client, name string) error {
	log.Infof("⏰ Wating for VMI (%s) to be in state running", name)
	vmw, err := client.VirtualMachineInstances(namespace).Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	defer vmw.Stop()
	for event := range vmw.ResultChan() {
		d, ok := event.Object.(*v1.VirtualMachineInstance)
		if !ok {
			return fmt.Errorf("unable to watch VMI %s", name)
		}
		if d.Name == name {
			log.Debugf("Found in state (%s)", d.Status.Phase)
			if d.Status.Phase == "Running" {
				return nil
			}
		}
	}
	return nil
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
	virtctlPath, err := virtctl.GetVirtctlPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get virtctl binary: %v", err)
	}
	log.Debugf("Running command %s against %s", command, v.vmName)
	cmd := exec.Command(virtctlPath, "ssh", "--namespace", v.namespace, "--local-ssh-opts", "-o StrictHostKeyChecking=no", "-c", command, fmt.Sprintf("fedora@vmi/%s", v.vmName))
	log.Debugf("Command: %s", cmd.String())
	stdout, err := cmd.Output()
	if err != nil {
		return stdout, fmt.Errorf("failed to run command: %v", err)
	}
	log.Debugf("Output: %s", string(stdout))
	return stdout, nil
}

// Close is a no-op for compatibility with VMExecutor interface
func (v *VirtctlClient) Close() error {
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
