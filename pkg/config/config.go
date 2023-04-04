package config

import (
	"fmt"
	"io/ioutil"
	"regexp"

	apiv1 "k8s.io/api/core/v1"

	log "github.com/jtaleric/k8s-netperf/pkg/logging"
	"github.com/jtaleric/k8s-netperf/pkg/metrics"
	"gopkg.in/yaml.v2"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Config describes the netperf tests
type Config struct {
	Parallelism int    `default:"1" yaml:"parallelism,omitempty"`
	Duration    int    `yaml:"duration,omitempty"`
	Profile     string `yaml:"profile,omitempty"`
	Samples     int    `yaml:"samples,omitempty"`
	MessageSize int    `yaml:"messagesize,omitempty"`
	Service     bool   `default:"false" yaml:"service,omitempty"`
	Metric      string
}

// PerfScenarios describes the different scenarios
type PerfScenarios struct {
	NodeLocal      bool
	HostNetwork    bool
	Configs        []Config
	ServerNodeInfo metrics.NodeInfo
	ClientNodeInfo metrics.NodeInfo
	Client         apiv1.PodList
	Server         apiv1.PodList
	ClientAcross   apiv1.PodList
	ClientHost     apiv1.PodList
	ServerHost     apiv1.PodList
	NetperfService *apiv1.Service
	IperfService   *apiv1.Service
	RestConfig     rest.Config
	ClientSet      *kubernetes.Clientset
}

// Tests we will support in k8s-netperf
const validTests = "tcp_stream|udp_stream|tcp_rr|udp_rr|tcp_crr|udp_crr|sctp_stream|sctp_rr|sctp_crr"

// ParseConf will read in the netperf configuration file which
// describes which tests to run
// Returns Config struct
func ParseConf(fn string) ([]Config, error) {
	log.Infof("📒 Reading %s file.\n", fn)
	buf, err := ioutil.ReadFile(fn)
	if err != nil {
		return nil, err
	}
	c := make(map[string]Config)
	err = yaml.Unmarshal(buf, &c)
	if err != nil {
		return nil, fmt.Errorf("In file %q: %v", fn, err)
	}
	// Ignore the key
	// Pull out the specific tests
	var tests []Config
	for _, value := range c {
		p, _ := regexp.MatchString("(?i)"+validTests, value.Profile)
		if !p {
			return nil, fmt.Errorf("unknown netperf profile")
		}
		if value.Duration < 1 {
			return nil, fmt.Errorf("duration must be > 0")
		}
		if value.Samples < 1 {
			return nil, fmt.Errorf("samples must be > 0")
		}
		if value.MessageSize < 1 {
			return nil, fmt.Errorf("messagesize must be > 0")
		}
		if value.Parallelism < 1 {
			return nil, fmt.Errorf("parallelism must be > 0")
		}
		if value.Service {
			if value.Parallelism > 1 {
				return nil, fmt.Errorf("parallelism must be 1 when using a service")
			}
		}
		tests = append(tests, value)
	}
	return tests, nil
}

// Show Display the netperf config
func Show(c Config, driver string) {
	log.Infof("🗒️  Running %s %s (service %t) for %ds\n", driver, c.Profile, c.Service, c.Duration)
}
