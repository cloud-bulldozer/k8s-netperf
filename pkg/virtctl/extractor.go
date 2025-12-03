package virtctl

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	log "github.com/cloud-bulldozer/k8s-netperf/pkg/logging"
)

var (
	extractedPath string
	extractOnce   sync.Once
	extractErr    error
)

// GetVirtctlPath returns the path to virtctl binary, extracting embedded binary if needed
func GetVirtctlPath() (string, error) {
	extractOnce.Do(func() {
		extractedPath, extractErr = getVirtctlPathInternal()
	})
	return extractedPath, extractErr
}

func getVirtctlPathInternal() (string, error) {
	// Try embedded binary first
	if embeddedPath, err := extractEmbeddedVirtctl(); err == nil {
		log.Debugf("Using embedded virtctl binary: %s", embeddedPath)
		return embeddedPath, nil
	} else {
		log.Debugf("Failed to extract embedded virtctl: %v", err)
	}
	
	// Fallback to system PATH
	if systemPath, err := exec.LookPath("virtctl"); err == nil {
		log.Debugf("Using system virtctl binary: %s", systemPath)
		return systemPath, nil
	}
	
	return "", fmt.Errorf("virtctl not available: neither embedded nor system binary found")
}

func extractEmbeddedVirtctl() (string, error) {
	var binaryData []byte
	
	// Select platform-specific binary
	platform := runtime.GOOS + "/" + runtime.GOARCH
	switch platform {
	case "linux/amd64":
		binaryData = virtctlLinuxAmd64
	default:
		return "", fmt.Errorf("unsupported platform: %s (only linux/amd64 is currently embedded)", platform)
	}
	
	if len(binaryData) == 0 {
		return "", fmt.Errorf("embedded virtctl binary is empty for platform %s", platform)
	}
	
	// Create temporary file in system temp directory
	tmpFile, err := os.CreateTemp("", "k8s-netperf-virtctl-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %v", err)
	}
	defer tmpFile.Close()
	
	// Write binary data
	if _, err := tmpFile.Write(binaryData); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to write virtctl binary: %v", err)
	}
	
	// Make executable
	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to make virtctl executable: %v", err)
	}
	
	log.Debugf("Extracted virtctl binary to: %s", tmpFile.Name())
	return tmpFile.Name(), nil
}

// CleanupExtractedBinary removes the extracted virtctl binary if it was created
func CleanupExtractedBinary() error {
	if extractedPath != "" && isTemporaryPath(extractedPath) {
		log.Debugf("Cleaning up extracted virtctl binary: %s", extractedPath)
		return os.Remove(extractedPath)
	}
	return nil
}

// isTemporaryPath checks if the path is in the system temporary directory
func isTemporaryPath(path string) bool {
	tmpDir := os.TempDir()
	rel, err := filepath.Rel(tmpDir, path)
	if err != nil {
		return false
	}
	return !filepath.IsAbs(rel) && rel != ".."
}