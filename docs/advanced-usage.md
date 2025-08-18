# Advanced Usage

## Using External Server
This enables k8s-netperf to use the IP address provided via the `--serverIP` option as server address and the client sends requests to this IP address. This allows dataplane testing between ocp internal client pod and external server.

> *Note: User has to create a server with the provided IP address and run the intented k8s-netperf driver (i.e uperf, iperf or netperf). User has to enable respective ports on this server to allow the traffic from the client*

Once the external server is ready to accept the traffic, users can orhestrate k8s-netperf by running

`k8s-netperf --serverIP=44.243.95.221`

## Running with VMs
Running k8s-netperf against Virtual Machines (OpenShift CNV) requires

- OpenShift CNV must be deployed and users should be able to define VMIs
- SSH keys to be present in the home directory `(~/.ssh/id_rsa.pub)`
- OpenShift Routes - k8s-netperf uses this to reach the VMs (k8s-netperf will create the route for the user, but we need Routes)

If the two above are in place, users can orhestrate k8s-netperf to launch VMs by running

`k8s-netperf --vm`

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
```
./bin/amd64/k8s-netperf --vm --bridge br0
```

By default, it will read the `bridgeNetwork.json` file from the git repository. If the default IP addresses (10.10.10.12/24 and 10.10.10.14/24) are not available for your setup, it is possible to change it by passing a JSON file as a parameter with `--bridgeNetwork`, like follow:
```
k8s-netperf --vm --bridge br0 --bridgeNetwork /path/to/my/bridgeConfig.json
```
