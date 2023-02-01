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
$ kubectl create sa -n netperf netperf
```

If you run with `-all`, you will need to allow `hostNetwork` for the netperf sa.


```shell
$ kubectl create ns netperf
$ kubectl create sa netperf -n netperf
$ ./k8s-netperf -help
Usage of ./k8s-netperf:
  -all
    	Run all tests scenarios - hostNet and podNetwork (if possible)
  -config string
    	K8s netperf Configuration File (default "netperf.yml")
  -debug
    	Enable debug log
  -local
    	Run Netperf with pod/server on the same node
  -metrics
    	Show all system metrics retrieved from prom
  -prom string
    	Prometheus URL
  -tcp-tolerance float
    	Allowed %diff from hostNetwork to podNetwork, anything above tolerance will result in k8s-netperf exiting 1. (default 10)
```

`-prom` accepts a string (URL). Example  http://localhost:9090

When using `-prom` with a non-openshift clsuter, it will be necessary to pass the prometheus URL.

With OpenShift, we attempt to discover the OpenShift route. If that route is not reachable, it might be required to `port-forward` the service and pass that via the `-prom` option.

`-metrics` will enable displaying prometheus captured metrics to stdout. By default they will be written to a csv file. 

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
`k8s-netperf` has a cli option for `-tcp-tolerance` which defaults to 10%.

In order to have `k8s-netperf` determine pass/fail the user must pass the `-all` flag. `k8s-netperf` must be able to run with hostNetwork and podNetwork across nodes.

```shell
$ ./k8s-netperf -tcp-tolerance 10
------------------------------------------------------------------------------- Stream Results -------------------------------------------------------------------------------
Scenario           | Parallelism     | Host Network    | Service         | Message Size    | Same node       | Duration        | Samples         | Avg value      
-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
📊 TCP_STREAM      | 2               | true            | false           | 1024            | false           | 10              | 1               | 1867.890000     (Mb/s) 
📊 TCP_STREAM      | 2               | false           | false           | 1024            | false           | 10              | 1               | 1657.140000     (Mb/s) 
-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
--------------------------------------------------------------------------- Stream Latency Results ---------------------------------------------------------------------------
Scenario           | Parallelism     | Host Network    | Service         | Message Size    | Same node       | Duration        | Samples         | 99%tile value  
-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
📊 TCP_STREAM      | 2               | true            |false           | 1024            | false           | 10              | 1               | 32.000000       (usec) 
📊 TCP_STREAM      | 2               | false           |false           | 1024            | false           | 10              | 1               | 32.000000       (usec) 
-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
😥 TCP Stream percent difference when comparing hostNetwork to podNetwork is greater than 10.0 percent (12.0 percent)
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
------------------------------------------------------------------------------- Stream Results -------------------------------------------------------------------------------
Scenario           | Parallelism     | Host Network    | Service         | Message Size    | Same node       | Duration        | Samples         | Avg value      
-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
📊 TCP_STREAM      | 1               | false           | false           | 1024            | false           | 10              | 3               | 1131.150000     (Mb/s) 
📊 TCP_STREAM      | 2               | false           | false           | 1024            | false           | 10              | 3               | 1710.150000     (Mb/s) 
📊 TCP_STREAM      | 1               | false           | false           | 8192            | false           | 10              | 3               | 4437.520000     (Mb/s) 
📊 UDP_STREAM      | 1               | false           | false           | 1024            | false           | 10              | 3               | 1159.790000     (Mb/s) 
-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
---------------------------------------------------------------------------------- RR Results ----------------------------------------------------------------------------------
Scenario           | Parallelism     | Host Network    | Service         | Message Size    | Same node       | Duration        | Samples         | Avg value      
-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
📊 TCP_CRR         | 1               | false           | false           | 1024            | false           | 10              | 3               | 5954.940000     (OP/s) 
📊 TCP_CRR         | 1               | false           | true            | 1024            | false           | 10              | 3               | 1455.470000     (OP/s) 
📊 TCP_RR          | 1               | false           | false           | 1024            | false           | 10              | 3               | 41330.000000    (OP/s) 
-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
--------------------------------------------------------------------------- Stream Latency Results ---------------------------------------------------------------------------
Scenario           | Parallelism     | Host Network    | Service         | Message Size    | Same node       | Duration        | Samples         | 99%tile value  
-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
📊 TCP_STREAM      | 1               | false           |false           | 1024            | false           | 10              | 3               | 23.000000       (usec) 
📊 TCP_STREAM      | 2               | false           |false           | 1024            | false           | 10              | 3               | 34.000000       (usec) 
📊 TCP_STREAM      | 1               | false           |false           | 8192            | false           | 10              | 3               | 30.000000       (usec) 
📊 UDP_STREAM      | 1               | false           |false           | 1024            | false           | 10              | 3               | 14.000000       (usec) 
-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
---------------------------------------------------------------------------- RR Latency Results ----------------------------------------------------------------------------
Scenario           | Parallelism     | Host Network    | Service         | Message Size    | Same node       | Duration        | Samples         | 99%tile value  
-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
📊 TCP_CRR         |  1               | false           | false           | 1024            | false           | 10              | 3               | 456.000000      (usec) 
📊 TCP_CRR         |  1               | false           | true            | 1024            | false           | 10              | 3               | 248.000000      (usec) 
📊 TCP_RR          |  1               | false           | false           | 1024            | false           | 10              | 3               | 85.000000       (usec) 
-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
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
