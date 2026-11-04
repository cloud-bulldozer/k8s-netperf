# Cross-Cluster Mode

Cross-cluster mode runs the benchmark client on one cluster and the server on another, for example to measure traffic between two MicroShift clusters connected with Cluster-to-Cluster Connectivity (C2CC). The k8s-netperf binary talks to both API servers; the pods and the image are the same as in a same-cluster run, so results are comparable.

```shell
KUBECONFIG=~/.kube/kubeconfig.cluster-a k8s-netperf \
  --config full-run.yaml \
  --server-kubeconfig ~/.kube/kubeconfig.cluster-b \
  --server-domain cluster-b.remote
```

- The client cluster is the one of the normal kubeconfig (`$KUBECONFIG` or `~/.kube/config`): it gets the `netperf` namespace and the `client-across` Deployment.
- The server cluster is the current context of `--server-kubeconfig`: it gets the `netperf` namespace, the `server` Deployment and the Services of the requested drivers.

## Flags

| Flag | Meaning |
|------|---------|
| `--server-kubeconfig PATH` | Enables cross-cluster mode and places the server side on this cluster. |
| `--server-domain DOMAIN` | DNS suffix the client cluster uses for the server cluster's Services. Service entries connect to `<service>.netperf.svc.<DOMAIN>`. Lowercased, one trailing dot removed, validated as a DNS name (including the longest generated Service name). |
| `--server-prom URL` | Prometheus of the server cluster, for server-side metrics. Discovered automatically when the server cluster is OpenShift. |

## Target variants

Each config entry selects its variant through its `service` field, exactly like a same-cluster run selects pod or Service:

| `service` | Variant | Address given to the driver in the client pod |
|-----------|---------|-----------------------------------------------|
| `false` | Pod IP (`podIP`) | IP of the server pod, read from the server cluster's API |
| `true` | Service DNS (`serviceDNS`) | `<service>.netperf.svc.<DOMAIN>` |

A config with both kinds of entries, such as `full-run.yaml`, covers both variants in one invocation. The Service name is never resolved by k8s-netperf: each driver process in the client pod resolves it through the client cluster's DNS, so the run exercises DNS forwarding and ClusterIP routing between the clusters. With the default `ndots:5` and a short domain, the resolver tries the pod's search domains first; that happens before the measured interval.

Services used by the Service DNS variant: `netperf-service` (control 12865, data 42424-42439), `iperf-service` (22865), `uperf-service` (30000, 30001) and, for uperf `TCP_STREAM_LAT`, `uperf-histogram-service` (31000, 31001); every port is exposed for TCP and UDP. netperf runs one process and one data port per stream, so a netperf `service: true` entry may use at most 16 streams; pod IP entries have no such limit.

## Validation

The run stops before creating anything when:

- `--server-kubeconfig` is combined with `--serverIP`, `--vm`, `--pod=false`, `--udnl2`, `--udnl3`, `--cudn`, `--hostNet`, `--all`, `--local`, `--across`, `--bridge`, `--sriov`, `--macvlan`, `--localnet` or `--ib-write-bw`;
- `--server-domain` or `--server-prom` is given without `--server-kubeconfig`;
- a `service: true` entry exists without `--server-domain`, or a netperf `service: true` entry uses more than 16 streams;
- either cluster has no node labeled `node-role.kubernetes.io/worker=` (one worker per cluster is enough; `--local` is not needed for single-node clusters);
- with `--clean=false`, either cluster already has a `netperf` namespace.

`--privileged`, `--annotation` and `--label` apply to the pods on both clusters. If both kubeconfigs point at the same API server, the run warns and continues; this is useful to test the plumbing on one cluster with `--server-domain cluster.local`, but the traffic does not leave that cluster.

## Results

Every JSON document, OpenSearch document and CSV row carries the cross-cluster fields, in same-cluster runs too:

| JSON / OpenSearch | CSV column | Same-cluster run | Cross-cluster run |
|-------------------|------------|------------------|-------------------|
| `crossCluster` | Cross Cluster | `false` | `true` |
| `crossClusterTarget` | Cross Cluster Target | empty | `podIP` or `serviceDNS` |
| `serverCluster.apiServer` | Server Cluster | `serverCluster` is `null`, column empty | API server URL of `--server-kubeconfig` |
| `serverCluster.domain` | | | `--server-domain` |
| `serverCluster.metadata` | | | cluster metadata of the server cluster (same discovery as `metadata`) |

The other fields keep their meaning: `metadata` describes the client cluster, `clientNodeInfo` comes from the client cluster and `serverNodeInfo` (node name, host IP, kernel and OS versions) from the server cluster, `service` is the config entry's value, and `hostNetwork`, `local` and `externalServer` are `false`. `acrossAZ` is always `false` in this mode because zone labels of independent clusters are not comparable. `metadata.kernel` and `metadata.mtu` are not collected in this mode (they come from an unfiltered Prometheus query); per-node kernels are in the node infos.

The stdout tables are unchanged; a log line at the start of the tests names both API servers and nodes, and the `Service` column tells the two variants apart.

## Cleanup

With `--clean` (the default) the `netperf` namespace is deleted on both clusters before the run and after it, in parallel, each with its own 5 minute deadline. In cross-cluster mode it is also deleted on both clusters when the run fails (any fatal error) or receives SIGINT or SIGTERM; the run then exits with 1, 130 or 143. A second signal exits immediately without waiting for the cleanup.

The namespaces a run creates carry an owner annotation (`k8s-netperf.cloud-bulldozer.io/owner-token`). The start-of-run cleanup deletes any `netperf` namespace, as `--clean` always did. The cleanup at the end, after a failure or after a signal only deletes a namespace with this run's token, and only on a cluster this run started creating resources on, so a namespace another run created in the meantime is never removed. Deployment readiness is bounded at 10 minutes; a Deployment that cannot create pods or does not become ready fails the run with the reason (for example `Unschedulable` or `ImagePullBackOff`), which triggers the cleanup.

With `--clean=false` nothing is deleted on either cluster, and the run refuses to start on top of an existing `netperf` namespace.

## Metrics (`--prom`, `--server-prom`, `--metrics`)

- Client-side metrics (node CPU, top pods, vSwitch) come from the client cluster's Prometheus: `--prom`, or discovery when the client cluster is OpenShift, as in a same-cluster run.
- Server-side metrics come from the server cluster's Prometheus: `--server-prom`, or discovery when the server cluster is OpenShift. Without it, server-side metrics are not collected (`serverCPUCollected: false`, empty server pod lists) and a warning says so.
- One Prometheus that scrapes both clusters can be used by passing the same URL to `--prom` and `--server-prom`. Its series are told apart by node name (or node IP on plain Kubernetes), so before querying the run checks that neither selected node's name or IP also matches a node of the other cluster; if it does, no Prometheus metrics are collected for either side, with a warning.
- MicroShift does not ship Prometheus, so `--prom` and `--server-prom` must point at a Prometheus that scrapes the node(s), for example one on the controller host.
- The Prometheus CSV files (`cpu-`, `podcpu-`, `podmem-`, `vswitch-result-*.csv`) only contain rows for metrics that were collected, so a missing side never shows zeros that look measured. In JSON and OpenSearch, uncollected vSwitch values remain `0` (as in same-cluster runs); use `serverCPUCollected` and `clientCPUCollected` to tell collected node CPU apart.
- `--metrics` only prints the metric tables to stdout, as before.
