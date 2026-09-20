//go:build windows

package sysstat

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Both calls come from kernel32. NewLazySystemDLL resolves only out of
// System32, which is what keeps this from being a DLL-planting hole the way
// a plain LoadLibrary by name would be.
var (
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes     = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatus = kernel32.NewProc("GlobalMemoryStatusEx")
)

// prevIdle/prevBusy hold the last GetSystemTimes sample. Like Linux's
// /proc/stat these are cumulative since boot, so only the delta between two
// samples describes current load.
var prevIdle, prevBusy uint64

// fileTime is Windows' FILETIME: a 64-bit tick count split across two
// 32-bit halves.
type fileTime struct {
	low  uint32
	high uint32
}

func (f fileTime) uint64() uint64 { return uint64(f.high)<<32 | uint64(f.low) }

func cpuUsage() float64 {
	var idle, kernel, user fileTime
	r, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if r == 0 {
		return 0
	}

	// Kernel time already includes idle time, so busy work is
	// (kernel - idle) + user.
	idleTicks := idle.uint64()
	busyTicks := (kernel.uint64() - idleTicks) + user.uint64()

	diffIdle := idleTicks - prevIdle
	diffBusy := busyTicks - prevBusy
	prevIdle, prevBusy = idleTicks, busyTicks

	total := diffIdle + diffBusy
	if total == 0 {
		return 0
	}
	return clamp(float64(diffBusy) / float64(total))
}

// memoryStatusEx mirrors Windows' MEMORYSTATUSEX. length must be set to the
// struct's own size before the call, which is how the API versions itself.
type memoryStatusEx struct {
	length               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

func memUsage() float64 {
	var status memoryStatusEx
	status.length = uint32(unsafe.Sizeof(status))

	r, _, _ := procGlobalMemoryStatus.Call(uintptr(unsafe.Pointer(&status)))
	if r == 0 {
		return 0
	}

	// memoryLoad is already the percentage in use, computed by the kernel.
	return clamp(float64(status.memoryLoad) / 100.0)
}
