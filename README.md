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

```
--------------------------------------------------------------------------------------------------
Scenario           | Message Size    | Setup           | Duration        | Value
--------------------------------------------------------------------------------------------------
📊 TCP_STREAM      | 1024            | Across Node     | 10              | 815.480000      (Mb/s)
📊 TCP_STREAM      | 1024            | Same Node       | 10              | 1227.810000     (Mb/s)
📊 UDP_STREAM      | 1024            | Across Node     | 10              | 876.950000      (Mb/s)
📊 UDP_STREAM      | 1024            | Same Node       | 10              | 1413.400000     (Mb/s)
📊 TCP_CRR         | 1024            | Across Node     | 10              | 2555.640000     (OP/s)
📊 TCP_CRR         | 1024            | Same Node       | 10              | 11265.670000    (OP/s)
📊 TCP_RR          | 1024            | Across Node     | 10              | 9208.250000     (OP/s)
📊 TCP_RR          | 1024            | Same Node       | 10              | 49183.760000    (OP/s)
--------------------------------------------------------------------------------------------------
```
