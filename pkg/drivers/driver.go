package drivers

import (
	"bytes"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/sample"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Driver interface {
	IsTestSupported(string) bool
	Run(c *kubernetes.Clientset, rc rest.Config, nc config.Config, client apiv1.PodList, serverIP string, perf *config.PerfScenarios) (bytes.Buffer, error)
	ParseResults(stdout *bytes.Buffer, nc config.Config) (sample.Sample, error)
}

type netperf struct {
	driverName string
}

type iperf3 struct {
	driverName string
}

type uperf struct {
	driverName string
}

// NewDriver creates a new driver based on the provided driver name.
//
// It takes a string parameter `driverName` and returns a Driver.
func NewDriver(driverName string) Driver {
	switch driverName {
	case "iperf3":
		return &iperf3{
			driverName: driverName,
		}
	case "uperf":
		return &uperf{
			driverName: driverName,
		}
	default:
		return &netperf{
			driverName: driverName,
		}
	}
}
