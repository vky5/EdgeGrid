//go:build windows

package worker

import (
	"os"
	"syscall"
	"unsafe"
)

type memoryStatusEx struct {
	dwLength                uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

func detectRAMGB() float32 {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GlobalMemoryStatusEx")

	var ms memoryStatusEx
	ms.dwLength = uint32(unsafe.Sizeof(ms))
	ret, _, _ := proc.Call(uintptr(unsafe.Pointer(&ms)))
	if ret == 0 {
		return 0
	}
	return float32(ms.ullTotalPhys) / (1 << 30)
}

func detectDiskFreeGB() float32 {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetDiskFreeSpaceExW")

	dir, err := syscall.UTF16PtrFromString(os.TempDir())
	if err != nil {
		return 0
	}
	var freeBytesAvail, totalBytes, totalFreeBytes uint64
	ret, _, _ := proc.Call(
		uintptr(unsafe.Pointer(dir)),
		uintptr(unsafe.Pointer(&freeBytesAvail)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)),
	)
	if ret == 0 {
		return 0
	}
	return float32(freeBytesAvail) / (1 << 30)
}

func liveRAMUsedGB() float32 {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GlobalMemoryStatusEx")
	var ms memoryStatusEx
	ms.dwLength = uint32(unsafe.Sizeof(ms))
	ret, _, _ := proc.Call(uintptr(unsafe.Pointer(&ms)))
	if ret == 0 {
		return 0
	}
	used := ms.ullTotalPhys - ms.ullAvailPhys
	return float32(used) / (1 << 30)
}

func liveDiskUsedGB() float32 {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetDiskFreeSpaceExW")
	dir, err := syscall.UTF16PtrFromString(os.TempDir())
	if err != nil {
		return 0
	}
	var freeBytesAvail, totalBytes, totalFreeBytes uint64
	ret, _, _ := proc.Call(
		uintptr(unsafe.Pointer(dir)),
		uintptr(unsafe.Pointer(&freeBytesAvail)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)),
	)
	if ret == 0 {
		return 0
	}
	return float32(totalBytes-freeBytesAvail) / (1 << 30)
}

func liveDiskTotalGB() float32 {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetDiskFreeSpaceExW")
	dir, err := syscall.UTF16PtrFromString(os.TempDir())
	if err != nil {
		return 0
	}
	var freeBytesAvail, totalBytes, totalFreeBytes uint64
	ret, _, _ := proc.Call(
		uintptr(unsafe.Pointer(dir)),
		uintptr(unsafe.Pointer(&freeBytesAvail)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)),
	)
	if ret == 0 {
		return 0
	}
	return float32(totalBytes) / (1 << 30)
}
