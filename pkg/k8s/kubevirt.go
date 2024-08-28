package k8s

import (
	"context"
	"fmt"
	"os"

	b64 "encoding/base64"

	kubevirtv1 "github.com/cloud-bulldozer/k8s-netperf/pkg/kubevirt/client-go/clientset/versioned/typed/core/v1"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "kubevirt.io/api/core/v1"
)

func CreateVMServer(client *kubevirtv1.KubevirtV1Client, name string) (*v1.VirtualMachineInstance, error) {
	dirname, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	ssh, err := os.ReadFile(fmt.Sprintf("%s/.ssh/id_rsa.pub", dirname))
	if err != nil {
		return nil, err
	}
	data := fmt.Sprintf(`#cloud-config
runcmd:
  - dnf install -y uperf iperf3 git ethtool
users:
  - name: fedora
    groups: sudo
    shell: /bin/bash
    ssh_authorized_keys:
      - %s
ssh_deletekeys: false
password: fedora
chpasswd: { expire: False }`, string(ssh))
	return CreateVMI(client, name, b64.StdEncoding.EncodeToString([]byte(data)))

}

func CreateVMI(client *kubevirtv1.KubevirtV1Client, name string, b64data string) (*v1.VirtualMachineInstance, error) {
	vmi, err := client.VirtualMachineInstances(namespace).Create(context.TODO(), &v1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1.VirtualMachineInstanceSpec{
			Domain: v1.DomainSpec{
				Resources: v1.ResourceRequirements{
					Requests: k8sv1.ResourceList{
						k8sv1.ResourceMemory: resource.MustParse("4096Mi"),
						k8sv1.ResourceCPU:    resource.MustParse("500m"),
					},
				},
				CPU: &v1.CPU{
					Sockets: 2,
					Cores:   2,
					Threads: 1,
				},
				Devices: v1.Devices{
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
				},
			},
			Volumes: []v1.Volume{
				v1.Volume{
					Name: "disk0",
					VolumeSource: v1.VolumeSource{
						ContainerDisk: &v1.ContainerDiskSource{
							Image: "kubevirt/fedora-cloud-container-disk-demo:latest",
						},
					},
				},
				v1.Volume{
					Name: "cloudinit",
					VolumeSource: v1.VolumeSource{
						CloudInitNoCloud: &v1.CloudInitNoCloudSource{
							UserDataBase64: b64data,
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

func WaitForVMI(client *kubevirtv1.KubevirtV1Client, name string) error {
	vmw, err := client.VirtualMachineInstances(namespace).Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	defer vmw.Stop()
	for event := range vmw.ResultChan() {
		d, ok := event.Object.(*v1.VirtualMachineInstance)
		if !ok {
			return fmt.Errorf("Unable to watch VMI %s", name)
		}
		if d.Name == name {
			if d.Status.Phase == "Running" {
				return nil
			}
		}
	}
	return nil
}
