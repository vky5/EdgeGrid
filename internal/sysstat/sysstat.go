// Package sysstat reads this machine's CPU and memory use as a number from 0 to 1, with one file per OS.
// a reading that fails gives 0, not an error, so the dashboard gauge just shows nothing
package sysstat

// how much of the CPU is in use, from 0 to 1
// it compares against the last call, so the first call returns 0
func CPUUsage() float64 { return cpuUsage() }

// how much of the memory is in use, from 0 to 1
func MemUsage() float64 { return memUsage() }

// keeps a value between 0 and 1
// e.g. a busy Mac's load average can go over 1, and a counter wrapping can go below 0
func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
