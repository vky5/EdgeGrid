// Package hardware detects the local machine's GPU, RAM, and disk capacity
// (used once at worker registration) and live resource usage (polled at
// every heartbeat). Platform-specific probing lives in the build-tagged
// hardware_{linux,darwin,windows}.go files; this one holds the cross-platform
// GPU probe shared by all three.
package hardware

import (
	"os/exec"
	"strconv"
	"strings"
)

type Spec struct {
	HasGPU     bool
	GPUName    string
	GPUVramGB  float32
	RAMGB      float32
	DiskFreeGB float32
}

// Detect probes GPU (all platforms), RAM, and free disk space.
func Detect() Spec {
	spec := Spec{
		RAMGB:      detectRAMGB(),
		DiskFreeGB: DiskFreeGB(),
	}

	// nvidia-smi is the same command on Linux, macOS, and Windows
	out, err := exec.Command(
		"nvidia-smi",
		"--query-gpu=name,memory.total",
		"--format=csv,noheader,nounits",
	).Output()
	if err == nil {
		line := strings.TrimSpace(string(out))
		if idx := strings.Index(line, "\n"); idx != -1 {
			line = line[:idx] // first GPU only
		}
		parts := strings.SplitN(line, ", ", 2)
		if len(parts) == 2 {
			spec.HasGPU = true
			spec.GPUName = strings.TrimSpace(parts[0])
			if mb, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
				spec.GPUVramGB = float32(mb) / 1024
			}
		}
	}

	return spec
}
