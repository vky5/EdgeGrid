//go:build darwin

package sysstat

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// macOS has no /proc. Per-CPU tick counters live behind host_processor_info,
// a Mach call that needs cgo, so CPU load here is derived from the load
// average instead — available over plain sysctl. That is an approximation:
// load average counts runnable *and* uninterruptible-wait threads over the
// last minute, so a machine blocked on slow I/O reads as busy, and the
// figure lags a spike by up to a minute. For a dashboard gauge that is an
// acceptable trade against requiring cgo.
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

// loadAvg1 reads the 1-minute load average from the vm.loadavg sysctl,
// which returns a C struct loadavg: three fixed-point uint32 values
// followed by the scale they are divided by.
func loadAvg1() (float64, bool) {
	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil || len(raw) < 12 {
		return 0, false
	}

	le := func(b []byte) uint32 {
		return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	}
	ldavg := le(raw[0:4])

	// fscale follows the three uint32s, aligned to 8 bytes.
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

// memUsage combines hw.memsize (total physical memory, straight from
// sysctl) with vm_stat's page counts. The page counters are only reachable
// through Mach's host_statistics64 without shelling out, which again would
// mean cgo — vm_stat is a standard macOS binary and the cheaper trade.
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
		// Pages the kernel can hand out without evicting anything: free,
		// plus inactive and speculative, which are reclaimed on demand.
		// Wired and active pages are genuinely in use.
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

// parsePages reads a vm_stat count, which is printed with a trailing dot
// ("  1234567.").
func parsePages(s string) uint64 {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".")
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}
