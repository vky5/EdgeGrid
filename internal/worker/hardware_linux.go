//go:build linux

package worker

import (
	"os"
	"syscall"
)

func detectRAMGB() float32 {
	var info syscall.Sysinfo_t
	if err := syscall.Sysinfo(&info); err != nil {
		return 0
	}
	totalBytes := uint64(info.Totalram) * uint64(info.Unit)
	return float32(totalBytes) / (1 << 30)
}

func detectDiskFreeGB() float32 {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(os.TempDir(), &fs); err != nil {
		return 0
	}
	freeBytes := fs.Bavail * uint64(fs.Bsize)
	return float32(freeBytes) / (1 << 30)
}

func liveRAMUsedGB() float32 {
	var info syscall.Sysinfo_t
	if err := syscall.Sysinfo(&info); err != nil {
		return 0
	}
	unit := uint64(info.Unit)
	used := (uint64(info.Totalram) - uint64(info.Freeram) - uint64(info.Bufferram)) * unit
	return float32(used) / (1 << 30)
}

func liveDiskUsedGB() float32 {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(os.TempDir(), &fs); err != nil {
		return 0
	}
	total := fs.Blocks * uint64(fs.Bsize)
	free := fs.Bavail * uint64(fs.Bsize)
	return float32(total-free) / (1 << 30)
}

func liveDiskTotalGB() float32 {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(os.TempDir(), &fs); err != nil {
		return 0
	}
	return float32(fs.Blocks*uint64(fs.Bsize)) / (1 << 30)
}
