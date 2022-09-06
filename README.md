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
`k8s-netperf` will provide updates to stdout of the operations it is running, such as creating the server/client deployments and the execution of the workload in the contianer.

Same node refers to how the pods were deployed. If the cluster has > 2 nodes with nodes which have `worker=` there will be a cross-node throughput test. 

```
----------------------------------------------------------------------------------------------------------------------
Scenario           | Message Size    | Same node       | Duration        | Samples         | Avg value
----------------------------------------------------------------------------------------------------------------------
📊 TCP_STREAM      | 16384           | false           | 10              | 1               | 901.350000      (Mb/s)
📊 TCP_STREAM      | 16384           | true            | 10              | 1               | 10974.320000    (Mb/s)
📊 UDP_STREAM      | 16384           | false           | 10              | 1               | 3316.240000     (Mb/s)
📊 UDP_STREAM      | 16384           | true            | 10              | 1               | 8925.150000     (Mb/s)
📊 TCP_CRR         | 16384           | false           | 10              | 1               | 3040.690000     (OP/s)
📊 TCP_CRR         | 16384           | true            | 10              | 1               | 10880.340000    (OP/s)
📊 TCP_RR          | 16384           | false           | 10              | 1               | 13537.740000    (OP/s)
📊 TCP_RR          | 16384           | true            | 10              | 1               | 48891.490000    (OP/s)
----------------------------------------------------------------------------------------------------------------------
```
