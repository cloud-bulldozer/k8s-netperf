# Setup and Installation

## Build from Source

```shell
$ git clone http://github.com/cloud-bulldozer/k8s-netperf
$ cd k8s-netperf
$ make build
```

## Build Container Image

```shell
$ git clone http://github.com/cloud-bulldozer/k8s-netperf
$ cd k8s-netperf
$ make container-build
```

## Testing locally with kind

```shell
$ kind create cluster --config testing/kind-config.yaml
$ kubectl label node kind-worker  node-role.kubernetes.io/worker=""
$ kubectl label node kind-worker2 node-role.kubernetes.io/worker=""
$ k8s-netperf --config netperf.yml
```

## Label nodes
k8s-netperf will make the best decision it can to schedule the client and server in your cluster. However,
you can provide hints to ensure the client and server lands on specific nodes.

To do this, apply a label to the nodes you want the client and server running

```shell
$ oc label nodes node-name netperf=client
$ oc label nodes node-name netperf=server
```

## Running with Pods
Ensure your `kubeconfig` is properly set to the cluster you would like to run `k8s-netperf` against.

also be sure to create a `netperf` namespace. (Not over-writable yet)

```shell
$ kubectl create ns netperf
$ kubectl create sa -n netperf netperf
```

If you run with `--all`, you will need to allow `hostNetwork` for the netperf sa.

Example
```shell
$ oc adm policy add-scc-to-user hostnetwork -z netperf
```

Additional setup:
```shell
$ kubectl create ns netperf
$ kubectl create sa netperf -n netperf
```

Additional setup for the `--ib-write-bw` flag:
```shell
$ oc adm policy add-scc-to-user privileged -z netperf
$ oc adm policy add-scc-to-group privileged system:serviceaccounts:netperf
```


## Basic Usage

```shell
$ ./bin/amd64/k8s-netperf --help
A tool to run network performance tests in Kubernetes cluster

Usage:
  k8s-netperf [flags]

Flags:
      --config string             K8s netperf Configuration File (default "netperf.yml")
      --netperf                   Use netperf as load driver (default true)
      --iperf                     Use iperf3 as load driver
      --uperf                     Use uperf as load driver
      --ib-write-bw string        Use ib_write_bw as load driver, requires nic:gid format (e.g., mlx5_0:0, requires --privileged and --hostNet)
      --clean                     Clean-up resources created by k8s-netperf (default true)
      --json                      Instead of human-readable output, return JSON to stdout
      --local                     Run network performance tests with Server-Pods/Client-Pods on the same Node
      --vm                        Launch Virtual Machines instead of pods for client/servers
      --vm-image string           Use specified VM image (default "quay.io/containerdisks/fedora:39")
      --sockets uint32            Number of Sockets for VM (default 2)
      --cores uint32              Number of cores for VM (default 2)
      --threads uint32            Number of threads for VM (default 1)
      --across                    Place the client and server across availability zones
      --all                       Run all tests scenarios - hostNet and podNetwork (if possible)
      --hostNet                   Run only hostNetwork tests (no podNetwork tests)
      --debug                     Enable debug log
      --udnl2                     Create and use a layer2 UDN as a primary network.
      --udnl3                     Create and use a layer3 UDN as a primary network.
      --udnPluginBinding string   UDN with VMs only - the binding method of the UDN interface, select 'passt' or 'l2bridge' (default "passt")
      --bridge string             Name of the NetworkAttachmentDefinition to be used for bridge interface
      --bridgeNamespace string    Namespace of the NetworkAttachmentDefinition for bridge interface (default "default")
      --bridgeNetwork string      Json file for the VM network defined by the bridge interface - bridge should be enabled (default "bridgeNetwork.json")
      --prom string               Prometheus URL
      --uuid string               User provided UUID
      --search string             OpenSearch URL, if you have auth, pass in the format of https://user:pass@url:port
      --index string              OpenSearch Index to save the results to, defaults to k8s-netperf
      --metrics                   Show all system metrics retrieved from prom
      --tcp-tolerance float       Allowed %diff from hostNetwork to podNetwork, anything above tolerance will result in k8s-netperf exiting 1. (default 10)
      --version                   k8s-netperf version
      --csv                       Archive results, cluster and benchmark metrics in CSV files (default true)
      --serverIP string           External Server IP Address
      --privileged                Run pods with privileged security context
  -h, --help                      help for k8s-netperf
```

## Flag Details

- `--across` will force the client to be across availability zones from the server
- `--json` will reduce all output to just the JSON result, allowing users to feed the result to `jq` or other tools. Only output to the screen will be the result JSON or errors.
- `--clean=true` will delete all the resources the project creates (deployments and services)
- `--serverIP` accepts a string (IP Address). Example  44.243.95.221. k8s-netperf assumes this as server address and the client sends requests to this IP address.
- `--prom` accepts a string (URL). Example  http://localhost:9090
  - When using `--prom` with a non-openshift cluster, it will be necessary to pass the prometheus URL.
- `--metrics` will enable displaying prometheus captured metrics to stdout. By default they will be written to a csv file.
- `--iperf` will enable the iperf3 load driver for any stream test (TCP_STREAM, UDP_STREAM). iperf3 doesn't have a RR or CRR test-type.
- `--uperf` will enable the uperf load driver for any stream test (TCP_STREAM, UDP_STREAM). uperf doesn't have CRR test-type.
- `--ib-write-bw $NIC:$GID` will enable the ib-write-bw load driver for any stream UDP_STREAM tests. ib_write_bw doesn't have CRR test-type.

> *Note: With OpenShift, we attempt to discover the OpenShift route. If that route is not reachable, it might be required to `port-forward` the service and pass that via the `--prom` option.*
