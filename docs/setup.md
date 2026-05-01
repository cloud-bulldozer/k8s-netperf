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

The namespace can be created in three ways:

- **Automatically** (default) -- k8s-netperf creates it with default labels.
- **From a file** -- pass `--namespace-file namespace.yaml` to create it from your own YAML manifest (e.g. with custom labels or annotations). The `metadata.name` in the file must match `--namespace`.
- **Manually** -- create the namespace yourself before running k8s-netperf with `--clean=false` so it is not deleted on startup.

The service account and hostnetwork role binding are always created automatically.

> *Note: When `--clean=true` (the default), k8s-netperf deletes the namespace at startup to remove leftover resources from previous runs, then recreates it. Pass `--clean=false` to skip this.*

## Concurrent Runs in the Same Namespace

k8s-netperf scopes all deployments, services, pod labels, and pod-affinity selectors to a per-run ID derived from the run UUID. This means multiple k8s-netperf instances can run side by side in the same namespace without colliding on resource names or routing service traffic to the wrong pods.

Each run produces resources named like `client-<runID>`, `server-<runID>`, `netperf-service-<runID>`, etc. Pods carry a `k8s-netperf/run-id=<runID>` label, and service selectors plus PodAntiAffinity rules match on that label so each run only sees its own pods.

**Required for concurrent runs:** pass `--clean=false`. The default cleanup deletes the entire namespace at both startup and shutdown, which would terminate any other in-flight run sharing the namespace.

```shell
# Run A
$ k8s-netperf --namespace shared-perf --clean=false --tag baseline ...

# Run B in parallel
$ k8s-netperf --namespace shared-perf --clean=false --tag experimental ...
```

Use `--uuid` to fix the run ID if you want predictable resource names. Otherwise k8s-netperf generates a fresh UUID per run.

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
      --kubeconfig string           Path to the kubeconfig file (defaults to KUBECONFIG env or ~/.kube/config)
      --config string               K8s netperf Configuration File (default "netperf.yml")
      --netperf                     Use netperf as load driver (default true)
      --iperf                       Use iperf3 as load driver
      --uperf                       Use uperf as load driver
      --ib-write-bw string          Use ib_write_bw as load driver, requires nic:gid format (e.g., mlx5_0:0, requires --privileged and --hostNet)
      --clean                       Clean-up resources created by k8s-netperf (default true)
      --json                        Instead of human-readable output, return JSON to stdout
      --local                       Run network performance tests with Server-Pods/Client-Pods on the same Node
      --pod                         Run tests using pods (default true)
      --vm                          Run tests using Virtual Machines
      --vm-image string             Use specified VM image (default "quay.io/containerdisks/fedora:39")
      --use-virtctl                 Use virtctl ssh for VM connections instead of traditional SSH
      --sockets uint32              Number of Sockets for VM (default 2)
      --cores uint32                Number of cores for VM (default 2)
      --threads uint32              Number of threads for VM (default 1)
      --across                      Place the client and server across availability zones
      --all                         Run all tests scenarios - hostNet and podNetwork (if possible)
      --hostNet                     Run only hostNetwork tests (no podNetwork tests)
      --debug                       Enable debug log
      --udnl2                       Create and use a layer2 UDN as a primary network
      --udnl3                       Create and use a layer3 UDN as a primary network
      --cudn string                 Create and use a Cluster UDN as a secondary network
      --udnPluginBinding string     UDN with VMs only - the binding method of the UDN interface, select 'passt' or 'l2bridge' (default "passt")
      --bridge string               Name of the NetworkAttachmentDefinition to be used for bridge interface
      --bridgeNamespace string      Namespace of the NetworkAttachmentDefinition for bridge interface (default "default")
      --bridgeNetwork string        Json file for the VM network defined by the bridge interface (default "bridgeNetwork.json")
      --sriov string                SR-IOV PF interface name (e.g., ens1f0). Requires SR-IOV operator
      --sriov-node-selector string  Node role label for SR-IOV node selector (default "worker")
      --namespace string            Kubernetes namespace for netperf resources (default "netperf")
      --namespace-file string       Path to a YAML file defining the namespace to create (namespace name is read from the file)
      --node-selector strings       Node selector as key=value to control pod scheduling and node count checks (can be repeated)
      --toleration strings          Toleration key to add to netperf pods (can be repeated)
      --auto-tolerate               Automatically add tolerations for taints found on netperf=server and netperf=client nodes
      --tag strings                 Tags to attach to indexed results for filtering in dashboards (can be repeated)
      --prom string                 Prometheus URL
      --uuid string                 User provided UUID
      --search string               OpenSearch URL, if you have auth, pass in the format of https://user:pass@url:port
      --index string                OpenSearch Index to save the results to (default "k8s-netperf")
      --metrics                     Show all system metrics retrieved from prom
      --tcp-tolerance float         Allowed %diff from hostNetwork to podNetwork (default 10)
      --version                     k8s-netperf version
      --csv                         Archive results in CSV files (default true, disabled when --json or --search is used)
      --serverIP string             External Server IP Address
      --privileged                  Run pods with privileged security context
  -h, --help                        help for k8s-netperf
```

## Flag Details

- `--kubeconfig` path to the kubeconfig file. If not set, uses the `KUBECONFIG` environment variable or falls back to `~/.kube/config`.
- `--across` will force the client to be across availability zones from the server.
- `--json` will reduce all output to just the JSON result, allowing users to feed the result to `jq` or other tools. Only output to the screen will be the result JSON or errors. CSV files are not generated unless `--csv` is explicitly set.
- `--clean=true` will delete the namespace and all resources at startup and after tests complete. Pass `--clean=false` to preserve the namespace between runs.
- `--serverIP` accepts a string (IP Address). Example: `44.243.95.221`. k8s-netperf assumes this as server address and the client sends requests to this IP address.
- `--prom` accepts a string (URL). Example: `http://localhost:9090`. On non-OpenShift clusters, this is required to enable Prometheus metrics collection. On OpenShift, Prometheus is auto-discovered.
- `--metrics` will enable displaying prometheus captured metrics to stdout. By default they will be written to a csv file.
- `--iperf` will enable the iperf3 load driver for any stream test (TCP_STREAM, UDP_STREAM). iperf3 doesn't have a RR or CRR test-type.
- `--uperf` will enable the uperf load driver for any stream test (TCP_STREAM, UDP_STREAM). uperf doesn't have CRR test-type.
- `--ib-write-bw $NIC:$GID` will enable the ib-write-bw load driver for any stream UDP_STREAM tests. ib_write_bw doesn't have CRR test-type.
- `--namespace` sets the Kubernetes namespace where all netperf resources (deployments, services, service accounts) are created. Defaults to `netperf`.
- `--namespace-file` path to a YAML file defining the namespace. The namespace name is read from the file's `metadata.name` and used automatically. Allows custom labels, annotations, or other metadata on the namespace.
- `--node-selector key=value` sets node label requirements for pod scheduling. When provided, replaces the default `node-role.kubernetes.io/worker=` selector. Also used for node count validation and zone detection. Can be repeated for multiple selectors.
- `--toleration key` adds a `NoSchedule` toleration (with `Exists` operator) for the given taint key to all netperf pods. Can be repeated.
- `--auto-tolerate` queries nodes labeled `netperf=server` and `netperf=client`, collects their taints, and automatically adds matching tolerations to all pods. Disabled by default. Deduplicates against any manual `--toleration` keys.
- `--tag` attaches labels to indexed results (OpenSearch, JSON, CSV) for filtering and comparing different configurations in dashboards. Can be repeated (e.g. `--tag baseline --tag cluster-a`).
- `--csv` archives results in CSV files. Enabled by default in human-readable mode. Automatically disabled when `--json` or `--search` is used, unless `--csv` is explicitly passed.
- `--search` OpenSearch URL for indexing results. When set, CSV generation is skipped unless `--csv` is explicitly passed.

> *Note: On OpenShift, Prometheus is auto-discovered via the monitoring route. On non-OpenShift clusters, pass the Prometheus URL via `--prom`. If the OpenShift route is not reachable, it might be necessary to `port-forward` the service and pass the URL via `--prom`.*
