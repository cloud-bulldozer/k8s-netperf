# k8s-netperf - Kubernetes Network Performance
Running Networking Performance Tests against K8s

[![asciicast](https://asciinema.org/a/524925.svg)](https://asciinema.org/a/524925)

## Status
Currently a work-in-progress.

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
$ go build .
```

## Running
Ensure your `kubeconfig` is properly set to the cluster you would like to run `k8s-netperf` against.

also be sure to create a `netperf` namespace. (Not over-writable yet)

```shell
$ kubectl create ns netperf
$ ./k8s-netperf -help
Usage of ./k8s-netperf:
  -config string
        K8s netperf Configuration File (default "netperf.yml")
  -local
        Run Netperf with pod/server on the same node
  -tcp-tolerance float
        Allowed %diff from hostNetwork to podNetwork, anything above tolerance will result in k8s-netperf exiting 1. (default 10)
```

### Config file
`netperf.yml` contains a default set of tests.

Description of each field in the YAML.
```yml
TCPStream:                 # Place-holder of a test name
   profile: "TCP_STREAM"   # Netperf profile to execute. This can be [TCP,UDP]_STREAM, [TCP,UDP]_RR, TCP_CRR
   duration: 3             # How long to run the test
   samples: 1              # Iterations to run specified test
   messagesize: 1024       # Size of the data-gram
   service: false          # If we should test with the server pod behind a service
```

## Pass / Fail
`k8s-netperf` has a cli option for `-tcp-tolerance` which defaults to 10%.

In order to have `k8s-netperf` determine pass/fail the user must pass the `-all` flag. `k8s-netperf` must be able to run with hostNetwork and podNetwork across nodes.

```shell
$ ./k8s-netperf -tcp-tolerance 1
--------------------------------------------------------------------- Stream Results ---------------------------------------------------------------------
Scenario           | Host Network    |Service         | Message Size    | Same node       | Duration        | Samples         | Avg value
-----------------------------------------------------------------------------------------------------------------------------------------------------------
📊 TCP_STREAM      | true            |false           | 16384           | false           | 10              | 3               | 923.180000      (Mb/s)
📊 TCP_STREAM      | false           |false           | 16384           | false           | 10              | 3               | 881.770000      (Mb/s)
-----------------------------------------------------------------------------------------------------------------------------------------------------------
😥 TCP Stream percent difference when comparing hostNetwork to podNetwork is greater than 1.0 percent (4.6 percent)
$ echo $?
1
```

| Tool | Test | pass/fail|
|-----------|------|----------|
| netperf | TCP_STREAM | working (default:10%) |

## Output
`k8s-netperf` will provide updates to stdout of the operations it is running, such as creating the server/client deployments and the execution of the workload in the container.

Same node refers to how the pods were deployed. If the cluster has > 2 nodes with nodes which have `worker=` there will be a cross-node throughput test.
```
--------------------------------------------------------------------- Stream Results ---------------------------------------------------------------------
Scenario           | Host Network    |Service         | Message Size    | Same node       | Duration        | Samples         | Avg value
-----------------------------------------------------------------------------------------------------------------------------------------------------------
📊 TCP_STREAM      | true            |false           | 16384           | false           | 10              | 3               | 923.360000      (Mb/s)
📊 TCP_STREAM      | false           |false           | 16384           | false           | 10              | 3               | 882.310000      (Mb/s)
📊 UDP_STREAM      | true            |false           | 16384           | false           | 10              | 3               | 943.440000      (Mb/s)
📊 UDP_STREAM      | false           |false           | 16384           | false           | 10              | 3               | 3472.660000     (Mb/s)
-----------------------------------------------------------------------------------------------------------------------------------------------------------
------------------------------------------------------------------------ RR Results ------------------------------------------------------------------------
Scenario           | Host Network    |Service         | Message Size    | Same node       | Duration        | Samples         | Avg value
-----------------------------------------------------------------------------------------------------------------------------------------------------------
📊 TCP_CRR         | false           |true            | 16384           | false           | 10              | 3               | 1969.860000     (OP/s)
📊 TCP_RR          | true            |false           | 16384           | false           | 10              | 3               | 14952.600000    (OP/s)
📊 TCP_RR          | false           |false           | 16384           | false           | 10              | 3               | 8862.240000     (OP/s)
📊 TCP_CRR         | true            |false           | 16384           | false           | 10              | 3               | 3454.080000     (OP/s)
📊 TCP_CRR         | false           |false           | 16384           | false           | 10              | 3               | 2332.390000     (OP/s)
-----------------------------------------------------------------------------------------------------------------------------------------------------------
```

### Output to CSV
`k8s-netperf` will write a csv file, after it has completed the desired performance tests.

Example output
```csv
Profile,Same node,Host Network,Service,Duration,# of Samples,Avg Throughput,Metric
TCP_RR,false,true,false,10,3,15000.820000,OP/s
TCP_RR,false,false,false,10,3,8120.030000,OP/s
TCP_STREAM,false,true,false,10,3,926.450000,Mb/s
TCP_STREAM,false,false,false,10,3,884.090000,Mb/s
UDP_STREAM,false,true,false,10,3,933.370000,Mb/s
UDP_STREAM,false,false,false,10,3,3459.930000,Mb/s
TCP_CRR,false,true,false,10,3,3316.510000,OP/s
TCP_CRR,false,false,false,10,3,2031.390000,OP/s
TCP_CRR,false,false,true,10,3,19.200000,OP/s
```
