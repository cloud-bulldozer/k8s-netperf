# Advanced Usage

## Using External Server
This enables k8s-netperf to use the IP address provided via the `--serverIP` option as server address and the client sends requests to this IP address. This allows dataplane testing between ocp internal client pod and external server.

> *Note: User has to create a server with the provided IP address and run the intented k8s-netperf driver (i.e uperf, iperf or netperf). User has to enable respective ports on this server to allow the traffic from the client*

Once the external server is ready to accept the traffic, users can orhestrate k8s-netperf by running:

```bash
k8s-netperf --serverIP=44.243.95.221
```

## Running with VMs
Running k8s-netperf against Virtual Machines (OpenShift CNV) requires

- OpenShift CNV must be deployed and users should be able to define VMIs
- SSH keys to be present in the home directory `(~/.ssh/id_rsa.pub)`
- OpenShift Routes - k8s-netperf uses this to reach the VMs (k8s-netperf will create the route for the user, but we need Routes)

If the two above are in place, users can orhestrate k8s-netperf to launch VMs by running:

```bash
k8s-netperf --vm
```

## Using User Defined Network - UDN (only on OCP 4.18 and above)
To run k8s-netperf using a UDN primary network for the test instead of the default network of OVN-k:

For a layer3 UDN:
```
$ k8s-netperf --udnl3
```

For a layer2 UDN:
```
$ k8s-netperf --udnl2
```

It works also with VMs:
```
$ k8s-netperf --udnl2 --vm --udnPluginBinding=l2bridge
```

> Warning! Support of k8s Services with UDN is not fully supported yet, you may faced inconsistent results when using a service in your tests. 

## Cluster User Defined Network - C-UDN
k8s-netperf is able to deploy a C-UDN and then attach network interfaces to the pods (or VMs) that allow use the C-UDN as a secondary network.
The subnet of the C-UDN is `20.0.0.0/16` and the driver will use this secondary interface for the test. C-UDN is automatically cleanup at the end of the execution.
```
# For a layer2 C-UDN:
$ k8s-netperf --cudn layer2

# For a layer3 C-UDN:
$ k8s-netperf --cudn layer3

# Both of the setup works with VMs too:
$ k8s-netperf --cudn layer2 --vm
# or
$ k8s-netperf --cudn layer3 --vm
```
## SR-IOV Network Testing
To run k8s-netperf over SR-IOV Virtual Functions, the SR-IOV Network Operator must be installed on the cluster. k8s-netperf will automatically create the required `SriovNetworkNodePolicy` and `SriovNetwork` CRs, and clean them up after the test.

### Pod SR-IOV
Pass the PF (Physical Function) interface name to the `--sriov` flag:
```bash
k8s-netperf --sriov ens2f0np0
```

On a single-node cluster:
```bash
k8s-netperf --sriov ens2f0np0 --local
```

By default, the SR-IOV policy targets nodes with the `node-role.kubernetes.io/worker` label. If SR-IOV NICs are only available on nodes with a different role label, use `--sriov-node-selector`:
```bash
k8s-netperf --sriov ens2f0np0 --sriov-node-selector cnf-worker
```

### VM SR-IOV
SR-IOV is also supported with KubeVirt VMs. When `--vm` is used with `--sriov`, VFs are created with `deviceType: vfio-pci` and passed through to the guest via PCI passthrough. The IP address assigned by whereabouts to the virt-launcher pod is configured inside the guest VM using `nmcli` over virtctl SSH.

```bash
k8s-netperf --sriov ens1f0 --vm --use-virtctl --pod=false
```

Requirements for VM SR-IOV:
- OpenShift CNV (KubeVirt) must be deployed
- The PF must use a driver supported by the VM containerdisk (e.g. Intel `ice`/`iavf` drivers are included in Fedora 39; Mellanox `mlx5_core` is not)
- SSH keys must be present in `~/.ssh/id_rsa.pub`
- `virtctl` is recommended (`--use-virtctl`) for SSH access into the guest VMs

> Note: Pod and VM SR-IOV tests cannot run in the same invocation because pods require `deviceType: netdevice` while VMs require `deviceType: vfio-pci`. Run them separately with `--pod=false` for VM-only or without `--vm` for pod-only.

> Note: `--sriov` is mutually exclusive with `--bridge`, `--macvlan`, `--ib-write-bw`, `--hostNet` and UDN flags (`--udnl2`, `--udnl3`, `--cudn`).

## MACVLAN Network Testing
To run k8s-netperf over a MACVLAN interface, pass the master (host) interface name to the `--macvlan` flag. k8s-netperf will automatically create a MACVLAN `NetworkAttachmentDefinition` with [whereabouts](https://github.com/k8snetworkplumbingwg/whereabouts) IPAM in the `netperf` namespace and clean it up after the test.

Prerequisites:
- [Multus CNI](https://github.com/k8snetworkplumbingwg/multus-cni) must be installed on the cluster
- The `macvlan` CNI plugin must be available on the nodes (`/opt/cni/bin/macvlan`)
- The whereabouts IPAM plugin must be installed

```bash
k8s-netperf --macvlan eth0
```

On a single-node cluster:
```bash
k8s-netperf --macvlan eth0 --local
```

> Note: `--macvlan` is mutually exclusive with `--bridge`, `--sriov`, `--ib-write-bw`, `--hostNet` and UDN flags (`--udnl2`, `--udnl3`, `--cudn`).

## Using a Linux Bridge Interface
When using `--bridge`, a NetworkAttachmentDefinition defining a bridge interface is attached to the VMs and is used for the test. It requires the name of the bridge as it is defined in the NetworkNodeConfigurationPolicy, NMstate operator is required.

For example:
```yaml
apiVersion: nmstate.io/v1alpha1
kind: NodeNetworkConfigurationPolicy
metadata:
  name: br0-eth1
spec:
  desiredState:
    interfaces:
      - name: br0
        description: Linux bridge with eno2 as a port
        type: linux-bridge
        state: up
        ipv4:
          dhcp: true
          enabled: true
        bridge:
          options:
            stp:
              enabled: false
          port:
            - name: eno2
```

Then you can launch a test using the bridge interface:
```bash
./bin/amd64/k8s-netperf --vm --bridge br0
```

By default, it will read the `bridgeNetwork.json` file from the git repository. If the default IP addresses (10.10.10.12/24 and 10.10.10.14/24) are not available for your setup, it is possible to change it by passing a JSON file as a parameter with `--bridgeNetwork`, like follow:
```bash
k8s-netperf --vm --bridge br0 --bridgeNetwork /path/to/my/bridgeConfig.json
```

## Privileged pods

If your use case requires running pods with privileged security context, use the `--privileged` flag:
```
$ k8s-netperf --privileged
```

## RoCEv2 testing

Using the `ib_write_bw` driver, your hardware should include RDMA devices:
```
$ k8s-netperf --ib-write-bw nic:gid --privileged --hostNet
```

### RoCEv2 local testing

On Fedora systems:
```bash
sudo modprobe rdma_rxe
sudo rdma link add rxe0 type rxe netdev eth0  # Name of your active interface
kind create cluster --config testing/kind-config-rdma.yaml
kubectl label node kind-control-plane node-role.kubernetes.io/worker=""
k8s-netperf --config config.yaml --hostNet --privileged --ib-write-bw rxe0:1 --local
```

Cleanup:
```bash
kind delete cluster
sudo rdma link delete rxe0
```
