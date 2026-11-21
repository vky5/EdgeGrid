//go:build darwin

package sysstat

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// CPU use on macOS, taken from the 1-minute load average so no cgo is needed
// it lags a spike by up to a minute and counts waiting on a slow disk as busy, fine for a gauge
func cpuUsage() float64 {
	load, ok := loadAvg1()
	if !ok {
		return 0
	}
	ncpu, err := unix.SysctlUint32("hw.ncpu")
	if err != nil || ncpu == 0 {
		return 0
	}
	return clamp(load / float64(ncpu))
}

// reads the 1-minute load average from sysctl vm.loadavg
// the reply is three whole numbers and then the scale to divide them by
func loadAvg1() (float64, bool) {
	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil || len(raw) < 12 {
		return 0, false
	}

	le := func(b []byte) uint32 {
		return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	}
	ldavg := le(raw[0:4])

	// the scale comes after the three numbers, lined up to 8 bytes
	var fscale uint64
	if len(raw) >= 24 {
		b := raw[16:24]
		for i := 7; i >= 0; i-- {
			fscale = fscale<<8 | uint64(b[i])
		}
	}
	if fscale == 0 {
		return 0, false
	}
	return float64(ldavg) / float64(fscale), true
}

// memory use on macOS: total from sysctl hw.memsize, free pages from the vm_stat command
// reading pages directly would need cgo, so running vm_stat is the cheaper option
func memUsage() float64 {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil || total == 0 {
		return 0
	}

	out, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0
	}

	pageSize := uint64(os.Getpagesize())
	var freePages uint64
	for _, line := range strings.Split(string(out), "\n") {
		// free, inactive and speculative pages can all be handed out right away; wired and active are in use
		for _, prefix := range []string{"Pages free:", "Pages inactive:", "Pages speculative:"} {
			if strings.HasPrefix(line, prefix) {
				freePages += parsePages(strings.TrimPrefix(line, prefix))
			}
		}
	}

	available := freePages * pageSize
	if available > total {
		return 0
	}
	return clamp(float64(total-available) / float64(total))
}

// reads a vm_stat page count, e.g. "  1234567." gives 1234567
func parsePages(s string) uint64 {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".")
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}
