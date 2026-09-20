// Package sysstat reads this machine's CPU and memory load. Every OS
// exposes that differently — /proc on Linux, sysctl and vm_stat on macOS,
// kernel32 calls on Windows — so the implementations live in build-tagged
// files and only this API is shared.
//
// Both functions return a fraction in 0..1 and never fail: a machine whose
// stats can't be read reports 0 rather than an error, because these feed a
// dashboard gauge that should degrade to "nothing to show" instead of
// taking down the view.
package sysstat

// CPUUsage is the share of CPU capacity in use, 0..1.
//
// It is sampled as a delta against the previous call, so the first call
// after startup has no baseline to compare against and returns 0. Callers
// poll it on a timer, which makes that a non-issue after one tick.
func CPUUsage() float64 { return cpuUsage() }

// MemUsage is the share of physical memory in use, 0..1.
func MemUsage() float64 { return memUsage() }

// clamp keeps a computed fraction inside 0..1. Load-average-derived values
// (macOS) can legitimately exceed 1 on an oversubscribed machine, and delta
// arithmetic can go slightly negative when counters wrap.
func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
