# Output and Results

## Pass / Fail
`k8s-netperf` has a cli option for `--tcp-tolerance` which defaults to 10%.

In order to have `k8s-netperf` determine pass/fail the user must pass the `--all` flag. `k8s-netperf` must be able to run with hostNetwork and podNetwork across nodes.

```shell
$ ./k8s-netperf --tcp-tolerance 1
+-------------------+---------+------------+-------------+--------------+---------+-----------------+-------+-----------+----------+---------+--------------------+
|    RESULT TYPE    | DRIVER  |  SCENARIO  | PARALLELISM | HOST NETWORK | SERVICE | EXTERNAL SERVER | MESSAGE SIZE | BURST | SAME NODE | DURATION | SAMPLES |     AVG VALUE      |
+-------------------+---------+------------+-------------+--------------+---------+-----------------+-------+-----------+----------+---------+--------------------+
| 📊 Stream Results | netperf | TCP_STREAM | 1           | true         | false   | false           | 1024         | 0     | false     | 10       | 3       | 2661.006667 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | true         | false   | false           | 1024         | 0     | false     | 10       | 3       | 2483.078229 (Mb/s) |
| 📊 Stream Results | uperf   | TCP_STREAM | 1           | true         | false   | false           | 1024         | 0     | false     | 10       | 3       | 2581.705097 (Mb/s) |
| 📊 Stream Results | netperf | TCP_STREAM | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 2702.230000 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 2523.434069 (Mb/s) |
| 📊 Stream Results | uperf   | TCP_STREAM | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 2567.665412 (Mb/s) |
| 📊 Stream Results | netperf | TCP_STREAM | 1           | true         | false   | false           | 8192         | 0     | false     | 10       | 3       | 2697.276667 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | true         | false   | false           | 8192         | 0     | false     | 10       | 3       | 2542.793728 (Mb/s) |
| 📊 Stream Results | uperf   | TCP_STREAM | 1           | true         | false   | false           | 8192         | 0     | false     | 10       | 3       | 2571.881579 (Mb/s) |
| 📊 Stream Results | netperf | TCP_STREAM | 1           | false        | false   | false           | 8192         | 0     | false     | 10       | 3       | 2707.076667 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | false        | false   | false           | 8192         | 0     | false     | 10       | 3       | 2604.067072 (Mb/s) |
| 📊 Stream Results | uperf   | TCP_STREAM | 1           | false        | false   | false           | 8192         | 0     | false     | 10       | 3       | 2687.276667 (Mb/s) |
| 📊 Stream Results | netperf | UDP_STREAM | 1           | true         | false   | false           | 1024         | 0     | false     | 10       | 3       | 1143.926667 (Mb/s) |
| 📊 Stream Results | iperf3  | UDP_STREAM | 1           | true         | false   | false           | 1024         | 0     | false     | 10       | 3       | 1202.428288 (Mb/s) |
| 📊 Stream Results | uperf   | UDP_STREAM | 1           | true         | false   | false           | 1024         | 0     | false     | 10       | 3       | 1242.059988 (Mb/s) |
| 📊 Stream Results | netperf | UDP_STREAM | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 1145.066667 (Mb/s) |
| 📊 Stream Results | iperf3  | UDP_STREAM | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 1239.580672 (Mb/s) |
| 📊 Stream Results | uperf   | UDP_STREAM | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 1261.840000 (Mb/s) |
+-------------------+---------+------------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+--------------------+
+---------------+---------+----------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+---------------------+
|  RESULT TYPE  | DRIVER  | SCENARIO | PARALLELISM | HOST NETWORK | SERVICE | EXTERNAL SERVER | MESSAGE SIZE | BURST | SAME NODE | DURATION | SAMPLES |      AVG VALUE      |
+---------------+---------+----------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+---------------------+
| 📊 Rr Results | netperf | TCP_CRR  | 1           | true         | true    | false           | 1024         | 0     | false     | 10       | 3       | 2370.196667 (OP/s)  |
| 📊 Rr Results | netperf | TCP_CRR  | 1           | false        | true    | false           | 1024         | 0     | false     | 10       | 3       | 3046.126667 (OP/s)  |
| 📊 Rr Results | netperf | TCP_RR   | 1           | true         | false   | false           | 1024         | 2     | false     | 10       | 3       | 16849.056667 (OP/s) |
| 📊 Rr Results | netperf | TCP_RR   | 1           | false        | false   | false           | 1024         | 2     | false     | 10       | 3       | 17101.856667 (OP/s) |
| 📊 Rr Results | netperf | TCP_CRR  | 1           | true         | false   | false           | 1024         | 0     | false     | 10       | 3       | 3166.136667 (OP/s)  |
| 📊 Rr Results | netperf | TCP_CRR  | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 1787.530000 (OP/s)  |
+---------------+---------+----------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+---------------------+
+---------------------------+---------+------------+-------------+--------------+---------+--------------+-------+-----------+----------+-----------------------------+
|        RESULT TYPE        | DRIVER  |  SCENARIO  | PARALLELISM | HOST NETWORK | SERVICE | EXTERNAL SERVER | MESSAGE SIZE | BURST | SAME NODE | DURATION | SAMPLES |   99%TILE VALUE   |
+---------------------------+---------+------------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+-------------------+
| 📊 Stream Latency Results | netperf | TCP_STREAM | 1           | true         | false   | false           | 1024         | 0     | false     | 10       | 3       | 71.333333 (usec)  |
| 📊 Stream Latency Results | netperf | TCP_STREAM | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 2.333333 (usec)   |
| 📊 Stream Latency Results | netperf | TCP_STREAM | 1           | true         | false   | false           | 8192         | 0     | false     | 10       | 3       | 276.000000 (usec) |
| 📊 Stream Latency Results | netperf | TCP_STREAM | 1           | false        | false   | false           | 8192         | 0     | false     | 10       | 3       | 124.333333 (usec) |
| 📊 Stream Latency Results | netperf | UDP_STREAM | 1           | true         | false   | false           | 1024         | 0     | false     | 10       | 3       | 14.666667 (usec)  |
| 📊 Stream Latency Results | netperf | UDP_STREAM | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 14.666667 (usec)  |
+---------------------------+---------+------------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+-------------------+
+-----------------------+---------+----------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+-------------------+
|      RESULT TYPE      | DRIVER  | SCENARIO | PARALLELISM | HOST NETWORK | SERVICE | EXTERNAL SERVER | MESSAGE SIZE | BURST | SAME NODE | DURATION | SAMPLES |   99%TILE VALUE   |
+-----------------------+---------+----------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+-------------------+
| 📊 Rr Latency Results | netperf | TCP_CRR  | 1           | true         | true    | false           | 1024         | 0     | false     | 10       | 3       | 817.333333 (usec) |
| 📊 Rr Latency Results | netperf | TCP_CRR  | 1           | false        | true    | false           | 1024         | 0     | false     | 10       | 3       | 647.666667 (usec) |
| 📊 Rr Latency Results | netperf | TCP_RR   | 1           | true         | false   | false           | 1024         | 2     | false     | 10       | 3       | 125.333333 (usec) |
| 📊 Rr Latency Results | netperf | TCP_RR   | 1           | false        | false   | false           | 1024         | 2     | false     | 10       | 3       | 119.666667 (usec) |
| 📊 Rr Latency Results | netperf | TCP_CRR  | 1           | true         | false   | false           | 1024         | 0     | false     | 10       | 3       | 621.000000 (usec) |
| 📊 Rr Latency Results | netperf | TCP_CRR  | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 539.666667 (usec) |
+-----------------------+---------+----------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+-------------------+
😥 TCP Stream percent difference when comparing hostNetwork to podNetwork is greater than 1.0 percent (2.7 percent)
$ echo $?
1
```

| Tool    | Test       | pass/fail             |
| ------- | ---------- | --------------------- |
| netperf | TCP_STREAM | working (default:10%) |

## Output Interpretation

`k8s-netperf` will provide updates to stdout of the operations it is running, such as creating the server/client deployments and the execution of the workload in the container.

Same node refers to how the pods were deployed. If the cluster has > 2 nodes with nodes which have `worker=` there will be a cross-node throughput test.

### Standard Output Format
```shell
+-------------------+---------+------------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+--------------------+
|    RESULT TYPE    | DRIVER  |  SCENARIO  | PARALLELISM | HOST NETWORK | SERVICE | EXTERNAL SERVER | MESSAGE SIZE | BURST | SAME NODE | DURATION | SAMPLES |     AVG VALUE      |
+-------------------+---------+------------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+--------------------+
| 📊 Stream Results | netperf | TCP_STREAM | 1           | true         | false   | false           | 1024         | 0     | false     | 10       | 3       | 2661.006667 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | true         | false   | false           | 1024         | 0     | false     | 10       | 3       | 2483.078229 (Mb/s) |
| 📊 Stream Results | netperf | TCP_STREAM | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 2702.230000 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 2523.434069 (Mb/s) |
| 📊 Stream Results | netperf | TCP_STREAM | 1           | true         | false   | false           | 8192         | 0     | false     | 10       | 3       | 2697.276667 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | true         | false   | false           | 8192         | 0     | false     | 10       | 3       | 2542.793728 (Mb/s) |
| 📊 Stream Results | netperf | TCP_STREAM | 1           | false        | false   | false           | 8192         | 0     | false     | 10       | 3       | 2707.076667 (Mb/s) |
| 📊 Stream Results | iperf3  | TCP_STREAM | 1           | false        | false   | false           | 8192         | 0     | false     | 10       | 3       | 2604.067072 (Mb/s) |
| 📊 Stream Results | netperf | UDP_STREAM | 1           | true         | false   | false           | 1024         | 0     | false     | 10       | 3       | 1143.926667 (Mb/s) |
| 📊 Stream Results | iperf3  | UDP_STREAM | 1           | true         | false   | false           | 1024         | 0     | false     | 10       | 3       | 1202.428288 (Mb/s) |
| 📊 Stream Results | netperf | UDP_STREAM | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 1145.066667 (Mb/s) |
| 📊 Stream Results | iperf3  | UDP_STREAM | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 1239.580672 (Mb/s) |
+-------------------+---------+------------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+--------------------+
```

### Loss/Retransmissions
k8s-netperf will report TCP Retransmissions and UDP Loss for both workload drivers (netperf and iperf).
```shell
+---------------------+---------+------------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+-----------+
|        TYPE         | DRIVER  |  SCENARIO  | PARALLELISM | HOST NETWORK | SERVICE | EXTERNAL SERVER | MESSAGE SIZE | BURST | SAME NODE | DURATION | SAMPLES | AVG VALUE |
+---------------------+---------+------------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+-----------+
| TCP Retransmissions | netperf | TCP_STREAM | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 54.666667 |
| TCP Retransmissions | netperf | TCP_STREAM | 1           | false        | false   | false           | 8192         | 0     | false     | 10       | 3       | 15.000000 |
| UDP Loss Percent    | netperf | UDP_STREAM | 1           | false        | false   | false           | 1024         | 0     | false     | 10       | 3       | 0.067031  |
+---------------------+---------+------------+-------------+--------------+---------+--------------+-------+-----------+----------+---------+-----------+
```

### Output to CSV
`k8s-netperf` will write a csv file, after it has completed the desired performance tests.

Example output:
```csv
Driver,Profile,Same node,Host Network,Service,External Server,Duration,Parallelism,# of Samples,Message Size,Confidence metric - low,Confidence metric - high,Avg Throughput,Throughput Metric,99%tile Observed Latency,Latency Metric
netperf,TCP_STREAM,false,false,false,false,10,1,3,1024,861.9391413991156,885.2741919342178,873.606667,Mb/s,3.3333333333333335,usec
netperf,TCP_STREAM,false,false,false,false,10,1,3,8192,178.12442996547009,1310.3422367011967,744.233333,Mb/s,2394.6666666666665,usec
netperf,UDP_STREAM,false,false,false,false,10,1,3,1024,584.3478157889886,993.4588508776783,788.903333,Mb/s,23,usec
netperf,TCP_CRR,false,false,false,false,10,1,3,1024,1889.3183973002176,2558.074936033115,2223.696667,OP/s,4682.666666666667,usec
netperf,TCP_CRR,false,false,true,false,10,1,3,1024,1169.206855676418,2954.3464776569153,2061.776667,OP/s,4679.333333333333,usec
netperf,TCP_RR,false,false,false,false,10,1,3,1024,6582.5359452538705,12085.437388079461,9333.986667,OP/s,451.3333333333333,usec
```
