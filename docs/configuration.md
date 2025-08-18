# Configuration

## Config File

### Config File v2
The v2 config file will be executed in the order the tests are presented in the config file.

```yml
tests :
  - TCPStream:              # Place-holder of a test name
    parallelism: 1          # Number of concurrent netperf processes to run.
    profile: "TCP_STREAM"   # Netperf profile to execute. This can be [TCP,UDP]_STREAM, [TCP,UDP]_RR, TCP_CRR
    duration: 3             # How long to run the test
    samples: 1              # Iterations to run specified test
    messagesize: 1024       # Size of the data-gram
    burst: 1                # Number of transactions inflight at one time. By default, netperf does one transaction at a time. This is netperf's TCP_RR specific option. 
    service: false          # If we should test with the server pod behind a service
```

### Config File v1
The v1 config file will be executed in random order. If you need the list of tests to be ran in a specific order, use the v2 config file format.
`netperf.yml` contains a default set of tests.

Description of each field in the YAML:
```yml
TCPStream:                 # Place-holder of a test name
   parallelism: 1          # Number of concurrent netperf processes to run.
   profile: "TCP_STREAM"   # Netperf profile to execute. This can be [TCP,UDP]_STREAM, [TCP,UDP]_RR, TCP_CRR
   duration: 3             # How long to run the test
   samples: 1              # Iterations to run specified test
   messagesize: 1024       # Size of the data-gram
   burst: 1                # Number of transactions inflight at one time. By default, netperf does one transaction at a time. This is netperf's TCP_RR specific option. 
   service: false          # If we should test with the server pod behind a service
```

### Parallelism
In most cases setting parallelism greater than 1 is OK, when using `service: true`, multiple threads (or processes in netperf) connect to the same service.

## Supported Benchmarks

| Tool    | Test       | Status  | Pass/Fail Context |
| ------- | ---------- | ------- | ----------------- |
| netperf | TCP_STREAM | Working | Yes               |
| netperf | UDP_STREAM | Working | No                |
| netperf | TCP_RR     | Working | No                |
| netperf | UDP_RR     | Working | No                |
| netperf | TCP_CRR    | Working | No                |
| uperf   | TCP_STREAM | Working | Yes               |
| uperf   | UDP_STREAM | Working | No                |
| uperf   | TCP_RR     | Working | No                |
| uperf   | UDP_RR     | Working | No                |
| iperf3  | TCP_STREAM | Working | Yes               |
| iperf3  | UDP_STREAM | Working | No                |

## Indexing to OpenSearch
`k8s-netperf` can store results in OpenSearch, if the user provides the OpenSearch URL. 
```shell
rhino in ~ $ ./k8s-netperf --config test.yml --search https://admin:pass@my-es:443
... <trimmed output>
INFO[2023-03-02 16:38:48] Connected to : [https://admin:pass@my-es:443] 
INFO[2023-03-02 16:38:48] Attempting to index 2 documents              
```

Document format can be seen in `pkg/archive/archive.go`
