# k8s-netperf - Kubernetes Network Performance
Running Networking Performance Tests against K8s

## Status
Currently a work-in-progress.

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
$ ./k8s-netperf
```

`netperf.yml` contains a default set of tests.
```yml
TCPStream:
   profile: "TCP_STREAM"
   duration: 3
   samples: 1
   messagesize: 1024

# TCPStream is a place-holder of a test name.
# profile is the netperf profile to execute. This can be `[TCP,UDP]_STREAM, [TCP,UDP]_RR, TCP_CRR`
# duration how long to run the test.
# samples how many times to run the test
# messagesize the size of the data-gram to send.
```

## Output
`k8s-netperf` will provide updates to stdout of the operations it is running, such as creating the server/client deployments and the execution of the workload in the container.

Same node refers to how the pods were deployed. If the cluster has > 2 nodes with nodes which have `worker=` there will be a cross-node throughput test.
```
----------------------------------------------------------- Stream Results -----------------------------------------------------------
Scenario           | Service         | Message Size    | Same node       | Duration        | Samples         | Avg value
----------------------------------------------------------------------------------------------------------------------------------------
📊 UDP_STREAM      | false           | 16384           | false           | 10              | 1               | 3180.260000     (Mb/s)
📊 TCP_STREAM      | false           | 16384           | false           | 10              | 1               | 846.200000      (Mb/s)
----------------------------------------------------------------------------------------------------------------------------------------
-------------------------------------------------------------- RR Results --------------------------------------------------------------
Scenario           | Service         | Message Size    | Same node       | Duration        | Samples         | Avg value
----------------------------------------------------------------------------------------------------------------------------------------
📊 TCP_CRR         | false           | 16384           | false           | 10              | 1               | 2534.770000     (OP/s)
📊 TCP_CRR         | true            | 16384           | false           | 10              | 1               | 470.500000      (OP/s)
📊 TCP_RR          | false           | 16384           | false           | 10              | 1               | 10065.760000    (OP/s)
----------------------------------------------------------------------------------------------------------------------------------------
```

### Output to CSV
`k8s-netperf` will write a csv file, after it has completed the desired performance tests.

Example output
```
Profile,Same node,Service,Duration,# of Samples,Avg Throughput,Metric
TCP_STREAM,false,false,10,1,894.030000,Mb/s
UDP_STREAM,false,false,10,1,3297.260000,Mb/s
TCP_CRR,false,false,10,1,2691.570000,OP/s
TCP_CRR,false,true,10,1,1971.290000,OP/s
TCP_RR,false,false,10,1,9504.450000,OP/s
```

