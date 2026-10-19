package k8s

import (
	"context"
	b64 "encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	kubevirtfake "github.com/cloud-bulldozer/k8s-netperf/pkg/kubevirt/client-go/clientset/versioned/fake"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "kubevirt.io/api/core/v1"
)

func TestNetworkDataBase64OmitsDefaultNetwork(t *testing.T) {
	for _, networkData := range []string{"", "{}", "  {}  "} {
		if got := networkDataBase64(networkData); got != "" {
			t.Errorf("networkDataBase64(%q) = %q, want empty", networkData, got)
		}
	}
	if got := networkDataBase64("version: 2\nethernets: {}\n"); got == "" {
		t.Error("networkDataBase64() omitted explicit network data")
	}
}

func TestNewVMISelectsDiskSource(t *testing.T) {
	tests := []struct {
		name       string
		vmImage    string
		diskVolume string
	}{
		{name: "container disk default", vmImage: "mirror.example/vm:1"},
		{name: "offline clone disk", vmImage: "ignored.example/vm:1", diskVolume: "client-disk"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vmi := newVMI("workload", nil, "", corev1.PodAntiAffinity{}, corev1.NodeAffinity{}, tc.vmImage, tc.diskVolume, nil, nil, "", "", 1, 1, 1)
			disk := vmi.Spec.Volumes[0].VolumeSource
			if tc.diskVolume == "" {
				if disk.ContainerDisk == nil || disk.ContainerDisk.Image != tc.vmImage {
					t.Fatalf("ContainerDisk = %#v, want image %q", disk.ContainerDisk, tc.vmImage)
				}
				return
			}
			if disk.DataVolume == nil || disk.DataVolume.Name != tc.diskVolume || disk.ContainerDisk != nil {
				t.Fatalf("disk source = %#v, want DataVolume %q", disk, tc.diskVolume)
			}
		})
	}
}

func TestOfflineCloudInitContainsNoDownloads(t *testing.T) {
	cloudInit := map[string]string{"client": offlineClientCloudInit("key", []string{"netperf", "iperf3", "uperf"}, nil), "server": offlineServerCloudInit("key", []string{"netperf", "iperf3", "uperf"}, nil)}
	for name, data := range cloudInit {
		t.Run(name, func(t *testing.T) {
			for _, forbidden := range []string{"dnf", "git", "curl", "wget", "./configure", "make install"} {
				if strings.Contains(data, forbidden) {
					t.Errorf("offline cloud-init contains %q: %s", forbidden, data)
				}
			}
		})
	}
	if !strings.Contains(cloudInit["client"], "command -v netperf") || !strings.Contains(cloudInit["client"], "command -v super-netperf") || !strings.Contains(cloudInit["client"], "command -v iperf3") || !strings.Contains(cloudInit["client"], "command -v uperf") {
		t.Errorf("client cloud-init does not validate its required benchmark tools: %s", cloudInit["client"])
	}
	if !strings.Contains(cloudInit["server"], "command -v netserver") || !strings.Contains(cloudInit["server"], "netserver &") {
		t.Errorf("server cloud-init does not validate and start its required benchmark tools: %s", cloudInit["server"])
	}
	for _, data := range cloudInit {
		if strings.Contains(data, "chpasswd") || strings.Contains(data, "fedora:fedora\n") || !strings.Contains(data, "ssh_pwauth: false") || !strings.Contains(data, "disable_root: true") || !strings.Contains(data, "lock_passwd: true") {
			t.Errorf("cloud-init must be SSH-key only: %s", data)
		}
	}
	if _, err := b64.StdEncoding.DecodeString(b64.StdEncoding.EncodeToString([]byte(offlineClientCloudInit("key", nil, nil)))); err != nil {
		t.Fatalf("cloud-init is not encodable: %v", err)
	}
}

func TestOfflineCloudInitConfiguresAuthorizedKeysUnderUsers(t *testing.T) {
	for name, data := range map[string]string{
		"client": offlineClientCloudInit("current-key", nil, nil),
		"server": offlineServerCloudInit("current-key", nil, nil),
	} {
		t.Run(name, func(t *testing.T) {
			users := strings.Index(data, "users:")
			runcmd := strings.Index(data, "runcmd:")
			if strings.Contains(data, "bootcmd:") {
				t.Fatalf("cloud-init must not use bootcmd: %s", data)
			}
			key := strings.Index(data, "ssh_authorized_keys:\n      - current-key")
			if users < 0 || key < users || runcmd < key {
				t.Fatalf("cloud-init must configure the key under users before runcmd: %s", data)
			}
			for _, required := range []string{"PasswordAuthentication no", "PermitRootLogin no"} {
				if !strings.Contains(data, required) {
					t.Errorf("cloud-init does not enforce %q: %s", required, data)
				}
			}
		})
	}
}

func TestVirtctlSSHArgsDisableHostCheckingAndPrompts(t *testing.T) {
	args := virtctlSSHArgs("test-namespace", "/tmp/id_rsa", "echo ready", "test-vm")
	if !containsArg(args, "-c") || !containsArg(args, "echo ready") {
		t.Errorf("virtctl SSH arguments must pass the remote command using -c: %q", args)
	}
	if !containsArg(args, "--known-hosts") || !containsArg(args, "/dev/null") {
		t.Errorf("virtctl SSH arguments must discard virtctl known hosts: %q", args)
	}
	for _, option := range []string{
		"-o BatchMode=yes",
		"-o StrictHostKeyChecking=no",
		"-o UserKnownHostsFile=/dev/null",
		"-o GlobalKnownHostsFile=/dev/null",
	} {
		if !containsArg(args, option) {
			t.Errorf("virtctl SSH arguments missing %q: %q", option, args)
		}
	}
}

func TestRunVirtctlSSHWithRetries(t *testing.T) {
	t.Run("retries three times before succeeding", func(t *testing.T) {
		attempts := 0
		var delays []time.Duration
		output, err := runVirtctlSSHWithRetries(context.Background(), func() ([]byte, error) {
			attempts++
			if attempts <= virtctlSSHRetries {
				return nil, errors.New("connection unavailable")
			}
			return []byte("ready"), nil
		}, func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		})
		if err != nil {
			t.Fatalf("runVirtctlSSHWithRetries() error = %v", err)
		}
		if attempts != virtctlSSHRetries+1 {
			t.Errorf("attempts = %d, want %d", attempts, virtctlSSHRetries+1)
		}
		if want := []time.Duration{virtctlSSHRetryDelay, virtctlSSHRetryDelay, virtctlSSHRetryDelay}; !reflect.DeepEqual(delays, want) {
			t.Errorf("delays = %v, want %v", delays, want)
		}
		if got := string(output); got != "ready" {
			t.Errorf("output = %q, want ready", got)
		}
	})

	t.Run("stops when context is canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		attempts := 0
		_, err := runVirtctlSSHWithRetries(ctx, func() ([]byte, error) {
			attempts++
			return nil, errors.New("connection unavailable")
		}, func(context.Context, time.Duration) error {
			cancel()
			return context.Canceled
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context cancellation", err)
		}
		if attempts != 1 {
			t.Errorf("attempts = %d, want 1", attempts)
		}
	})
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

type fakeVMExecutor struct {
	output  []byte
	err     error
	command string
}

func (e *fakeVMExecutor) Run(command string) ([]byte, error) {
	e.command = command
	return e.output, e.err
}
func (e *fakeVMExecutor) Close() error { return nil }

func TestValidateOfflineVMPrerequisites(t *testing.T) {
	t.Run("requested tools present", func(t *testing.T) {
		executor := &fakeVMExecutor{}
		if err := ValidateOfflineVMPrerequisites(executor, []string{"netperf", "iperf3"}, nil); err != nil {
			t.Fatalf("ValidateOfflineVMPrerequisites() error = %v", err)
		}
		if !strings.Contains(executor.command, "command -v netperf") || !strings.Contains(executor.command, "command -v iperf3") {
			t.Errorf("command = %q, want requested tools", executor.command)
		}
	})
	t.Run("missing tool is reported without install", func(t *testing.T) {
		executor := &fakeVMExecutor{output: []byte("offline-prerequisite-missing:uperf"), err: fmt.Errorf("exit status 1")}
		err := ValidateOfflineVMPrerequisites(executor, []string{"uperf"}, nil)
		if err == nil || !strings.Contains(err.Error(), "offline VM client prerequisite missing: uperf") {
			t.Fatalf("error = %v, want exact missing prerequisite", err)
		}
		if strings.Contains(executor.command, "dnf") {
			t.Errorf("command must not install software: %q", executor.command)
		}
	})
	t.Run("server missing tool is reported", func(t *testing.T) {
		executor := &fakeVMExecutor{output: []byte("offline-prerequisite-missing:netserver"), err: fmt.Errorf("exit status 1")}
		err := ValidateOfflineVMServerPrerequisites(executor, []string{"netperf"}, nil)
		if err == nil || !strings.Contains(err.Error(), "offline VM server prerequisite missing: netserver") {
			t.Fatalf("error = %v, want exact missing server prerequisite", err)
		}
	})
}

func TestWaitForOfflineVMServerPrerequisites(t *testing.T) {
	t.Run("retries cloud-init before validating tools", func(t *testing.T) {
		cloudInitAttempts := 0
		var delays []time.Duration
		var commands []string
		err := waitForOfflineVMServerPrerequisites(context.Background(), func(_ context.Context, command string) ([]byte, error) {
			commands = append(commands, command)
			if command == "cloud-init status --wait" {
				cloudInitAttempts++
				if cloudInitAttempts < 3 {
					return nil, errors.New("ssh connection not ready")
				}
			}
			return nil, nil
		}, []string{"netperf"}, nil, func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		})
		if err != nil {
			t.Fatalf("waitForOfflineVMServerPrerequisites() error = %v", err)
		}
		if want := []time.Duration{offlineVMPrerequisiteBackoff, 2 * offlineVMPrerequisiteBackoff}; !reflect.DeepEqual(delays, want) {
			t.Errorf("backoff delays = %v, want %v", delays, want)
		}
		if got, want := commands, []string{"cloud-init status --wait", "cloud-init status --wait", "cloud-init status --wait", offlinePrerequisiteCommand(true, []string{"netperf"}, nil)}; !reflect.DeepEqual(got, want) {
			t.Errorf("commands = %q, want %q", got, want)
		}
	})

	t.Run("reports missing tools immediately after cloud-init", func(t *testing.T) {
		calls := 0
		err := waitForOfflineVMServerPrerequisites(context.Background(), func(_ context.Context, command string) ([]byte, error) {
			calls++
			if command == "cloud-init status --wait" {
				return nil, nil
			}
			return []byte("offline-prerequisite-missing:netserver"), errors.New("exit status 1")
		}, []string{"netperf"}, nil, func(context.Context, time.Duration) error {
			t.Fatal("sleep must not be called after cloud-init completes")
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "offline VM server prerequisite missing: netserver") {
			t.Fatalf("error = %v, want missing netserver error", err)
		}
		if calls != 2 {
			t.Errorf("run calls = %d, want 2", calls)
		}
	})

	for _, tc := range []struct {
		name   string
		output []byte
		stderr string
	}{
		{name: "status in stderr", stderr: "command terminated with exit status 2"},
		{name: "status in stdout", output: []byte("command terminated with exit status 2")},
	} {
		t.Run("continues after recoverable cloud-init errors in "+tc.name, func(t *testing.T) {
			var commands []string
			err := waitForOfflineVMServerPrerequisites(context.Background(), func(_ context.Context, command string) ([]byte, error) {
				commands = append(commands, command)
				if command == "cloud-init status --wait" {
					return tc.output, &virtctlSSHError{err: fakeExitError{code: 1}, stderr: tc.stderr}
				}
				return nil, nil
			}, []string{"netperf"}, nil, func(context.Context, time.Duration) error {
				t.Fatal("sleep must not be called after a recoverable cloud-init error")
				return nil
			})
			if err != nil {
				t.Fatalf("waitForOfflineVMServerPrerequisites() error = %v", err)
			}
			if got, want := commands, []string{"cloud-init status --wait", offlinePrerequisiteCommand(true, []string{"netperf"}, nil)}; !reflect.DeepEqual(got, want) {
				t.Errorf("commands = %q, want %q", got, want)
			}
		})
	}

	t.Run("does not retry a missing tool reported by cloud-init", func(t *testing.T) {
		calls := 0
		err := waitForOfflineVMServerPrerequisites(context.Background(), func(_ context.Context, command string) ([]byte, error) {
			calls++
			if command != "cloud-init status --wait" {
				t.Fatalf("command = %q, want cloud-init status", command)
			}
			return []byte("offline-prerequisite-missing:netserver"), errors.New("exit status 1")
		}, []string{"netperf"}, nil, func(context.Context, time.Duration) error {
			t.Fatal("sleep must not be called for a missing tool")
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "offline VM server prerequisite missing: netserver") {
			t.Fatalf("error = %v, want missing netserver error", err)
		}
		if calls != 1 {
			t.Errorf("run calls = %d, want 1", calls)
		}
	})

	t.Run("honors context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := waitForOfflineVMServerPrerequisites(ctx, func(context.Context, string) ([]byte, error) {
			t.Fatal("run must not be called after cancellation")
			return nil, nil
		}, []string{"netperf"}, nil, sleepWithContext)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context cancellation", err)
		}
	})
}

type fakeExitError struct {
	code int
}

func (e fakeExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.code)
}

func (e fakeExitError) ExitCode() int {
	return e.code
}

func TestOfflineCloudInitUsesHistogramUperfForLatency(t *testing.T) {
	configs := []config.Config{{Profile: "TCP_STREAM_LAT"}}
	client := offlineClientCloudInit("key", []string{"uperf"}, configs)
	server := offlineServerCloudInit("key", []string{"uperf"}, configs)
	for _, data := range []string{client, server} {
		if !strings.Contains(data, "/opt/uperf-histogram/bin/uperf") || strings.Contains(data, "command -v uperf") {
			t.Errorf("histogram cloud-init must validate histogram uperf: %s", data)
		}
	}
	if !strings.Contains(server, fmt.Sprintf("-P %d", UperfLatServerCtlPort)) {
		t.Errorf("server cloud-init does not start histogram listener: %s", server)
	}
}

func TestOfflineCloudInitStartsBothUperfListenersForMixedProfiles(t *testing.T) {
	server := offlineServerCloudInit("key", []string{"uperf"}, []config.Config{{Profile: "TCP_STREAM_LAT"}, {Profile: "TCP_STREAM"}})
	for _, port := range []int{UperfLatServerCtlPort, UperfServerCtlPort} {
		if !strings.Contains(server, fmt.Sprintf("-P %d", port)) {
			t.Errorf("server cloud-init does not start listener on port %d: %s", port, server)
		}
	}
}

func TestVMIStartError(t *testing.T) {
	testCases := []struct {
		name string
		vmi  *v1.VirtualMachineInstance
		want bool
	}{
		{name: "pending VMI", vmi: &v1.VirtualMachineInstance{}, want: false},
		{
			name: "failed VMI",
			vmi:  &v1.VirtualMachineInstance{Status: v1.VirtualMachineInstanceStatus{Phase: v1.Failed, Reason: "Unschedulable"}},
			want: true,
		},
		{
			name: "failed synchronization",
			vmi: &v1.VirtualMachineInstance{Status: v1.VirtualMachineInstanceStatus{Conditions: []v1.VirtualMachineInstanceCondition{
				{Type: v1.VirtualMachineInstanceSynchronized, Status: corev1.ConditionFalse, Reason: "Unsupported", Message: "TDX unavailable"},
			}}},
			want: true,
		},
		{
			name: "waiting for clone PVC",
			vmi: &v1.VirtualMachineInstance{Status: v1.VirtualMachineInstanceStatus{Conditions: []v1.VirtualMachineInstanceCondition{
				{Type: v1.VirtualMachineInstanceSynchronized, Status: corev1.ConditionFalse, Reason: "FailedPvcNotfound"},
			}}},
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vmiStartError(tc.vmi) != nil; got != tc.want {
				t.Errorf("vmiStartError() returned error = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestWaitForVMIAlreadyRunning(t *testing.T) {
	client := kubevirtfake.NewSimpleClientset(&v1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "running-vmi",
			Namespace: namespace,
		},
		Status: v1.VirtualMachineInstanceStatus{Phase: v1.Running},
	})

	if err := WaitForVMI(client.KubevirtV1(), "running-vmi"); err != nil {
		t.Fatalf("WaitForVMI() error = %v, want nil", err)
	}

	actions := client.Actions()
	if len(actions) != 1 || actions[0].GetVerb() != "get" {
		t.Errorf("actions = %#v, want exactly one get action", actions)
	}
}

func TestWaitForFailedPVCNotFound(t *testing.T) {
	t.Run("backs off until running", func(t *testing.T) {
		failedPVC := func() *v1.VirtualMachineInstance {
			return &v1.VirtualMachineInstance{Status: v1.VirtualMachineInstanceStatus{Conditions: []v1.VirtualMachineInstanceCondition{{Type: v1.VirtualMachineInstanceSynchronized, Status: corev1.ConditionFalse, Reason: "FailedPvcNotfound"}}}}
		}
		states := []*v1.VirtualMachineInstance{
			failedPVC(),
			failedPVC(),
			failedPVC(),
			{Status: v1.VirtualMachineInstanceStatus{Phase: v1.Running}},
		}
		var delays []time.Duration
		err := waitForFailedPVCNotFound(context.Background(), "clone-vmi", func(context.Context) (*v1.VirtualMachineInstance, error) {
			vmi := states[0]
			states = states[1:]
			return vmi, nil
		}, func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		})
		if err != nil {
			t.Fatalf("waitForFailedPVCNotFound() error = %v", err)
		}
		if want := []time.Duration{vmiPVCBackoff, 2 * vmiPVCBackoff, 4 * vmiPVCBackoff}; !reflect.DeepEqual(delays, want) {
			t.Errorf("backoff delays = %v, want %v", delays, want)
		}
	})

	t.Run("returns terminal failures", func(t *testing.T) {
		calls := 0
		err := waitForFailedPVCNotFound(context.Background(), "clone-vmi", func(context.Context) (*v1.VirtualMachineInstance, error) {
			return &v1.VirtualMachineInstance{Status: v1.VirtualMachineInstanceStatus{Conditions: []v1.VirtualMachineInstanceCondition{{Type: v1.VirtualMachineInstanceSynchronized, Status: corev1.ConditionFalse, Reason: "Unsupported"}}}}, nil
		}, func(_ context.Context, _ time.Duration) error {
			calls++
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "failed to synchronize") {
			t.Fatalf("waitForFailedPVCNotFound() error = %v, want terminal synchronization error", err)
		}
		if calls != 0 {
			t.Errorf("sleep calls = %d, want 0", calls)
		}
	})

	t.Run("honors context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := waitForFailedPVCNotFound(ctx, "clone-vmi", nil, sleepWithContext)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("waitForFailedPVCNotFound() error = %v, want context cancellation", err)
		}
	})
}

func TestVMIStartTimeout(t *testing.T) {
	if vmiStartTimeout != 10*time.Minute {
		t.Errorf("vmiStartTimeout = %s, want %s", vmiStartTimeout, 10*time.Minute)
	}
}
