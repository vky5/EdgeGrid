package worker

import (
	"os/exec"
	"strconv"
	"strings"
)

type HardwareSpec struct {
	HasGPU     bool
	GPUName    string
	GPUVramGB  float32
	RAMGB      float32
	DiskFreeGB float32
}

// detectHardware probes GPU (all platforms), RAM, and free disk space.
func detectHardware() HardwareSpec {
	spec := HardwareSpec{
		RAMGB:      detectRAMGB(),
		DiskFreeGB: detectDiskFreeGB(),
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
