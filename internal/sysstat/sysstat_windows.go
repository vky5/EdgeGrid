//go:build windows

package sysstat

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// both calls come from kernel32, loaded only from System32
// so a DLL dropped next to the binary can't be picked up instead
var (
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes     = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatus = kernel32.NewProc("GlobalMemoryStatusEx")
)

// the last GetSystemTimes reading
// those counts add up since boot, so only the change between two readings shows current load
var prevIdle, prevBusy uint64

// Windows' FILETIME: one 64-bit tick count split into two 32-bit halves
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

	// kernel time already includes idle, so busy is (kernel - idle) + user
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

// Windows' MEMORYSTATUSEX; length must be set to the struct's size before the call
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

	// already the percent in use, worked out by Windows
	return clamp(float64(status.memoryLoad) / 100.0)
}
