# k8s-netperf - Kubernetes Network Performance
Running Networking Performance Tests against K8s

[![asciicast](https://asciinema.org/a/524925.svg)](https://asciinema.org/a/524925)

### Benchmark Tool and Tests

| Tool | Test | Status | Pass/Fail Context |
|------|------|--------|-------------------|
| netperf | TCP_STREAM | Working | Yes |
| netperf | UDP_STREAM | Working | No |
| netperf | TCP_RR | Working | No |
| netperf | TCP_CRR | Working | No|

## Setup
```shell
$ git clone http://github.com/jtaleric/k8s-netperf
$ cd k8s-netperf
$ make build
```

## Build Container Image

```shell
$ git clone http://github.com/jtaleric/k8s-netperf
$ cd k8s-netperf 
$ make docker-build
```

## Running
Ensure your `kubeconfig` is properly set to the cluster you would like to run `k8s-netperf` against.

also be sure to create a `netperf` namespace. (Not over-writable yet)

```shell
$ kubectl create ns netperf
$ kubectl create sa -n netperf netperf
```

If you run with `-all`, you will need to allow `hostNetwork` for the netperf sa.

Example
```shell
$ oc adm policy add-scc-to-user hostnetwork -z netperf
```


```shell
$ kubectl create ns netperf
$ kubectl create sa netperf -n netperf
$ ./bin/amd64/k8s-netperf --help
A tool to run network performance tests in Kubernetes cluster

Usage:
  k8s-netperf [flags]

Flags:
      --all                   Run all tests scenarios - hostNet and podNetwork (if possible)
      --clean                 Clean-up resources created by k8s-netperf
      --config string         K8s netperf Configuration File (default "netperf.yml")
      --debug                 Enable debug log
  -h, --help                  help for k8s-netperf
      --iperf                 Use iperf3 as load driver (along with netperf)
      --json                  Instead of human-readable output, return JSON to stdout
      --local                 Run network performance tests with pod/server on the same node
      --metrics               Show all system metrics retrieved from prom
      --prom string           Prometheus URL
      --search string         OpenSearch URL, if you have auth, pass in the format of https://user:pass@url:port
      --tcp-tolerance float   Allowed %diff from hostNetwork to podNetwork, anything above tolerance will result in k8s-netperf exiting 1. (default 10)
      --uuid string           User provided UUID
```

Running with `--json` will reduce all output to just the JSON result, allowing users to feed the result to `jq` or other tools. Only output to the screen will be the result JSON or errors. 

`--clean=true` will delete all the resources the project creates (deployments and services)

`--prom` accepts a string (URL). Example  http://localhost:9090

When using `--prom` with a non-openshift clsuter, it will be necessary to pass the prometheus URL.

With OpenShift, we attempt to discover the OpenShift route. If that route is not reachable, it might be required to `port-forward` the service and pass that via the `--prom` option.

`--metrics` will enable displaying prometheus captured metrics to stdout. By default they will be written to a csv file.

`--iperf` will enable the iperf3 load driver for any stream test (TCP_STREAM, UDP_STREAM). iperf3 doesn't have a RR or CRR test-type.

### Config file
`netperf.yml` contains a default set of tests.

Description of each field in the YAML.
```yml
TCPStream:                 # Place-holder of a test name
   parallelism: 1          # Number of concurrent netperf processes to run.
   profile: "TCP_STREAM"   # Netperf profile to execute. This can be [TCP,UDP]_STREAM, [TCP,UDP]_RR, TCP_CRR
   duration: 3             # How long to run the test
   samples: 1              # Iterations to run specified test
   messagesize: 1024       # Size of the data-gram
   service: false          # If we should test with the server pod behind a service
```

#### parallelism
In most cases setting parallelism greater than 1 is OK, however through a `service` we only support a single process of netperf, since we bind to a specific port.

## Pass / Fail
`k8s-netperf` has a cli option for `--tcp-tolerance` which defaults to 10%.

In order to have `k8s-netperf` determine pass/fail the user must pass the `--all` flag. `k8s-netperf` must be able to run with hostNetwork and podNetwork across nodes.

```shell
$ ./k8s-netperf --tcp-tolerance 1
+-------------------+---------+------------+-------------+--------------+---------+--------------+-----------+----------+---------+--------------------+
|    RESULT TYPE    | DRIVER  |  SCENARIO  | PARALLELISM | HOST NETWORK | SERVICE | MESSAGE SIZE | SAME NODE | DURATION | SAMPLES |     AVG VALUE      |
+-------------------+---------+------------+-------------+--------------+---------+--------------+-----------+----------+---------+--------------------+
| 📊 Stream Results | netperf | TCP_STREAM | 1           | true         | false   | 1024         | false     | 10       | 3       | 2661.006667 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | true         | false   | 1024         | false     | 10       | 3       | 2483.078229 (Mb/s) |
| 📊 Stream Results | netperf | TCP_STREAM | 1           | false        | false   | 1024         | false     | 10       | 3       | 2702.230000 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | false        | false   | 1024         | false     | 10       | 3       | 2523.434069 (Mb/s) |
| 📊 Stream Results | netperf | TCP_STREAM | 1           | true         | false   | 8192         | false     | 10       | 3       | 2697.276667 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | true         | false   | 8192         | false     | 10       | 3       | 2542.793728 (Mb/s) |
| 📊 Stream Results | netperf | TCP_STREAM | 1           | false        | false   | 8192         | false     | 10       | 3       | 2707.076667 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | false        | false   | 8192         | false     | 10       | 3       | 2604.067072 (Mb/s) |
| 📊 Stream Results | netperf | UDP_STREAM | 1           | true         | false   | 1024         | false     | 10       | 3       | 1143.926667 (Mb/s) |
| 📊 Stream Results | iperf3  | UDP_STREAM | 1           | true         | false   | 1024         | false     | 10       | 3       | 1202.428288 (Mb/s) |
| 📊 Stream Results | netperf | UDP_STREAM | 1           | false        | false   | 1024         | false     | 10       | 3       | 1145.066667 (Mb/s) |
| 📊 Stream Results | iperf3  | UDP_STREAM | 1           | false        | false   | 1024         | false     | 10       | 3       | 1239.580672 (Mb/s) |
+-------------------+---------+------------+-------------+--------------+---------+--------------+-----------+----------+---------+--------------------+
+---------------+---------+----------+-------------+--------------+---------+--------------+-----------+----------+---------+---------------------+
|  RESULT TYPE  | DRIVER  | SCENARIO | PARALLELISM | HOST NETWORK | SERVICE | MESSAGE SIZE | SAME NODE | DURATION | SAMPLES |      AVG VALUE      |
+---------------+---------+----------+-------------+--------------+---------+--------------+-----------+----------+---------+---------------------+
| 📊 Rr Results | netperf | TCP_CRR  | 1           | true         | true    | 1024         | false     | 10       | 3       | 2370.196667 (OP/s)  |
| 📊 Rr Results | netperf | TCP_CRR  | 1           | false        | true    | 1024         | false     | 10       | 3       | 3046.126667 (OP/s)  |
| 📊 Rr Results | netperf | TCP_RR   | 1           | true         | false   | 1024         | false     | 10       | 3       | 16849.056667 (OP/s) |
| 📊 Rr Results | netperf | TCP_RR   | 1           | false        | false   | 1024         | false     | 10       | 3       | 17101.856667 (OP/s) |
| 📊 Rr Results | netperf | TCP_CRR  | 1           | true         | false   | 1024         | false     | 10       | 3       | 3166.136667 (OP/s)  |
| 📊 Rr Results | netperf | TCP_CRR  | 1           | false        | false   | 1024         | false     | 10       | 3       | 1787.530000 (OP/s)  |
+---------------+---------+----------+-------------+--------------+---------+--------------+-----------+----------+---------+---------------------+
+---------------------------+---------+------------+-------------+--------------+---------+--------------+-----------+----------+---------+-------------------+
|        RESULT TYPE        | DRIVER  |  SCENARIO  | PARALLELISM | HOST NETWORK | SERVICE | MESSAGE SIZE | SAME NODE | DURATION | SAMPLES |   99%TILE VALUE   |
+---------------------------+---------+------------+-------------+--------------+---------+--------------+-----------+----------+---------+-------------------+
| 📊 Stream Latency Results | netperf | TCP_STREAM | 1           | true         | false   | 1024         | false     | 10       | 3       | 71.333333 (usec)  |
| 📊 Stream Latency Results | netperf | TCP_STREAM | 1           | false        | false   | 1024         | false     | 10       | 3       | 2.333333 (usec)   |
| 📊 Stream Latency Results | netperf | TCP_STREAM | 1           | true         | false   | 8192         | false     | 10       | 3       | 276.000000 (usec) |
| 📊 Stream Latency Results | netperf | TCP_STREAM | 1           | false        | false   | 8192         | false     | 10       | 3       | 124.333333 (usec) |
| 📊 Stream Latency Results | netperf | UDP_STREAM | 1           | true         | false   | 1024         | false     | 10       | 3       | 14.666667 (usec)  |
| 📊 Stream Latency Results | netperf | UDP_STREAM | 1           | false        | false   | 1024         | false     | 10       | 3       | 14.666667 (usec)  |
+---------------------------+---------+------------+-------------+--------------+---------+--------------+-----------+----------+---------+-------------------+
+-----------------------+---------+----------+-------------+--------------+---------+--------------+-----------+----------+---------+-------------------+
|      RESULT TYPE      | DRIVER  | SCENARIO | PARALLELISM | HOST NETWORK | SERVICE | MESSAGE SIZE | SAME NODE | DURATION | SAMPLES |   99%TILE VALUE   |
+-----------------------+---------+----------+-------------+--------------+---------+--------------+-----------+----------+---------+-------------------+
| 📊 Rr Latency Results | netperf | TCP_CRR  | 1           | true         | true    | 1024         | false     | 10       | 3       | 817.333333 (usec) |
| 📊 Rr Latency Results | netperf | TCP_CRR  | 1           | false        | true    | 1024         | false     | 10       | 3       | 647.666667 (usec) |
| 📊 Rr Latency Results | netperf | TCP_RR   | 1           | true         | false   | 1024         | false     | 10       | 3       | 125.333333 (usec) |
| 📊 Rr Latency Results | netperf | TCP_RR   | 1           | false        | false   | 1024         | false     | 10       | 3       | 119.666667 (usec) |
| 📊 Rr Latency Results | netperf | TCP_CRR  | 1           | true         | false   | 1024         | false     | 10       | 3       | 621.000000 (usec) |
| 📊 Rr Latency Results | netperf | TCP_CRR  | 1           | false        | false   | 1024         | false     | 10       | 3       | 539.666667 (usec) |
+-----------------------+---------+----------+-------------+--------------+---------+--------------+-----------+----------+---------+-------------------+
😥 TCP Stream percent difference when comparing hostNetwork to podNetwork is greater than 1.0 percent (2.7 percent)
$ echo $?
1
```

| Tool | Test | pass/fail|
|-----------|------|----------|
| netperf | TCP_STREAM | working (default:10%) |

## Indexing to OpenSearch
`k8s-netperf` can store results in OpenSearch, if the user provides the OpenSearch URL. 
```shell
rhino in ~ $ ./k8s-netperf --config test.yml --search https://admin:pass@my-es:443
... <trimmed output>
INFO[2023-03-02 16:38:48] Connected to : [https://admin:pass@my-es:443] 
INFO[2023-03-02 16:38:48] Attempting to index 2 documents              
```

Document format can be seen in `pkg/archive/archive.go`

## Output
`k8s-netperf` will provide updates to stdout of the operations it is running, such as creating the server/client deployments and the execution of the workload in the container.

Same node refers to how the pods were deployed. If the cluster has > 2 nodes with nodes which have `worker=` there will be a cross-node throughput test.
```shell
+-------------------+---------+------------+-------------+--------------+---------+--------------+-----------+----------+---------+--------------------+
|    RESULT TYPE    | DRIVER  |  SCENARIO  | PARALLELISM | HOST NETWORK | SERVICE | MESSAGE SIZE | SAME NODE | DURATION | SAMPLES |     AVG VALUE      |
+-------------------+---------+------------+-------------+--------------+---------+--------------+-----------+----------+---------+--------------------+
| 📊 Stream Results | netperf | TCP_STREAM | 1           | true         | false   | 1024         | false     | 10       | 3       | 2661.006667 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | true         | false   | 1024         | false     | 10       | 3       | 2483.078229 (Mb/s) |
| 📊 Stream Results | netperf | TCP_STREAM | 1           | false        | false   | 1024         | false     | 10       | 3       | 2702.230000 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | false        | false   | 1024         | false     | 10       | 3       | 2523.434069 (Mb/s) |
| 📊 Stream Results | netperf | TCP_STREAM | 1           | true         | false   | 8192         | false     | 10       | 3       | 2697.276667 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | true         | false   | 8192         | false     | 10       | 3       | 2542.793728 (Mb/s) |
| 📊 Stream Results | netperf | TCP_STREAM | 1           | false        | false   | 8192         | false     | 10       | 3       | 2707.076667 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | false        | false   | 8192         | false     | 10       | 3       | 2604.067072 (Mb/s) |
| 📊 Stream Results | netperf | UDP_STREAM | 1           | true         | false   | 1024         | false     | 10       | 3       | 1143.926667 (Mb/s) |
| 📊 Stream Results | iperf3  | UDP_STREAM | 1           | true         | false   | 1024         | false     | 10       | 3       | 1202.428288 (Mb/s) |
| 📊 Stream Results | netperf | UDP_STREAM | 1           | false        | false   | 1024         | false     | 10       | 3       | 1145.066667 (Mb/s) |
| 📊 Stream Results | iperf3  | UDP_STREAM | 1           | false        | false   | 1024         | false     | 10       | 3       | 1239.580672 (Mb/s) |
+-------------------+---------+------------+-------------+--------------+---------+--------------+-----------+----------+---------+--------------------+
```

### Output to CSV
`k8s-netperf` will write a csv file, after it has completed the desired performance tests.

Example output
```csv
Profile,Same node,Host Network,Service,Duration,Parallelism,# of Samples,Message Size,Avg Throughput,Throughput Metric,99%tile Observed Latency,Latency Metric
TCP_STREAM,false,false,false,10,1,3,1024,1131.150000,Mb/s,23,usec
TCP_STREAM,false,false,false,10,2,3,1024,1710.150000,Mb/s,34,usec
TCP_STREAM,false,false,false,10,1,3,8192,4437.520000,Mb/s,30,usec
UDP_STREAM,false,false,false,10,1,3,1024,1159.790000,Mb/s,14,usec
TCP_CRR,false,false,false,10,1,3,1024,5954.940000,OP/s,456,usec
TCP_CRR,false,false,true,10,1,3,1024,1455.470000,OP/s,248,usec
TCP_RR,false,false,false,10,1,3,1024,41330.000000,OP/s,85,usec
```
