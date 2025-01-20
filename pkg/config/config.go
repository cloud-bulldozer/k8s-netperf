package config

import (
	"fmt"
	"os"
	"regexp"

	kubevirtv1 "github.com/cloud-bulldozer/k8s-netperf/pkg/kubevirt/client-go/clientset/versioned/typed/core/v1"
	"github.com/melbahja/goph"
	apiv1 "k8s.io/api/core/v1"

	log "github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
	"github.com/cloud-bulldozer/k8s-netperf/pkg/metrics"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/dynamic"
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
	Burst       int    `yaml:"burst,omitempty"`
	Service     bool   `default:"false" yaml:"service,omitempty"`
	Metric      string
	AcrossAZ    bool
}

// PerfScenarios describes the different scenarios
type PerfScenarios struct {
	NodeLocal           bool
	AcrossAZ            bool
	HostNetwork         bool
	Configs             []Config
	VM                  bool
	VMImage             string
	VMHost              string
	Udn                 bool
	BridgeServerNetwork string
	BridgeClientNetwork string
	ServerNodeInfo      metrics.NodeInfo
	ClientNodeInfo      metrics.NodeInfo
	Client              apiv1.PodList
	Server              apiv1.PodList
	ClientAcross        apiv1.PodList
	ClientHost          apiv1.PodList
	ServerHost          apiv1.PodList
	NetperfService      *apiv1.Service
	IperfService        *apiv1.Service
	UperfService        *apiv1.Service
	RestConfig          rest.Config
	ClientSet           *kubernetes.Clientset
	KClient             *kubevirtv1.KubevirtV1Client
	DClient             *dynamic.DynamicClient
	SSHClient           *goph.Client
}

// struct for bridge options
type BridgeNetworkConfig struct {
	BridgeServerNetwork string `json:"bridgeServerNetwork"`
	BridgeClientNetwork string `json:"bridgeClientNetwork"`
}

// Tests we will support in k8s-netperf
const validTests = "tcp_stream|udp_stream|tcp_rr|udp_rr|tcp_crr|udp_crr|sctp_stream|sctp_rr|sctp_crr"

func validConfig(cfg Config) (bool, error) {
	preEval := regexp.MustCompile("(?i)" + validTests)
	p := preEval.MatchString(cfg.Profile)
	if !p {
		return false, fmt.Errorf("unknown netperf profile")
	}
	if cfg.Duration < 1 {
		return false, fmt.Errorf("duration must be > 0")
	}
	if cfg.Samples < 1 {
		return false, fmt.Errorf("samples must be > 0")
	}
	if cfg.MessageSize < 1 {
		return false, fmt.Errorf("messagesize must be > 0")
	}
	if cfg.Parallelism < 1 {
		return false, fmt.Errorf("parallelism must be > 0")
	}
	return true, nil
}

// ParseConf will read in the netperf configuration file which
// describes which tests to run
// Returns Config struct
func ParseConf(fn string) ([]Config, error) {
	log.Infof("📒 Reading %s file. ", fn)
	buf, err := os.ReadFile(fn)
	if err != nil {
		return nil, err
	}
	c := make(map[string]Config)
	err = yaml.Unmarshal(buf, &c)
	if err != nil {
		return nil, fmt.Errorf("in file %q: %v", fn, err)
	}
	// Ignore the key
	// Pull out the specific tests
	var tests []Config
	for _, value := range c {
		ok, err := validConfig(value)
		if !ok {
			return nil, err
		}
		tests = append(tests, value)
	}
	return tests, nil
}

// ParseV2Conf will read in the netperf configuration file which
// describes which tests to run
// Returns Config struct
func ParseV2Conf(fn string) ([]Config, error) {
	log.Infof("📒 Reading %s file - using ConfigV2 Method. ", fn)
	buf, err := os.ReadFile(fn)
	if err != nil {
		return nil, err
	}
	c := make(map[string][]Config)
	// New YAML structure :
	// tests :
	//   - Test_name :
	//     profile: <xyz> ...
	err = yaml.Unmarshal(buf, &c)
	if err != nil {
		return nil, fmt.Errorf("in file %q: %v", fn, err)
	}

	// Ignore the key
	// Pull out the specific tests
	var tests []Config
	for _, cf := range c {
		for _, cfg := range cf {
			ok, err := validConfig(cfg)
			if !ok {
				return nil, err
			}
			tests = append(tests, cfg)
		}
	}
	return tests, nil
}

// Show Display the netperf config
func Show(c Config, driver string) {
	log.Infof("🗒️  Running %s %s (service %t) for %ds ", driver, c.Profile, c.Service, c.Duration)
}
