package drivers

import (
	"bytes"
	"fmt"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/sample"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Driver interface {
	IsTestSupported() bool
	Run(c *kubernetes.Clientset, rc rest.Config, nc config.Config, client apiv1.PodList, serverIP string, perf *config.PerfScenarios) (bytes.Buffer, error)
	ParseResults(stdout *bytes.Buffer, nc config.Config) (sample.Sample, error)
}

type netperf struct {
	driverName string
	testConfig config.Config
}

type iperf3 struct {
	driverName string
	testConfig config.Config
}

type uperf struct {
	driverName string
	testConfig config.Config
}

// NewDriver returns a Driver based on the given driverName and configuration.
// It currently supports the "iperf3", "uperf", and "netperf" drivers.
// If the driverName is not recognized, it returns an error.
func NewDriver(driverName string, cfg config.Config) (Driver, error) {
	switch driverName {
	case "iperf3":
		return &iperf3{
			driverName: driverName,
			testConfig: cfg,
		}, nil
	case "uperf":
		return &uperf{
			driverName: driverName,
			testConfig: cfg,
		}, nil
	case "netperf":
		return &netperf{
			driverName: driverName,
			testConfig: cfg,
		}, nil
	default:
		return nil, fmt.Errorf("unknown driver: %s", driverName)
	}
}
