//go:build darwin

package worker

import (
	"encoding/binary"
	"os"

	"golang.org/x/sys/unix"
)

func detectRAMGB() float32 {
	// hw.memsize returns total physical RAM as a little-endian uint64
	b, err := unix.SysctlRaw("hw.memsize")
	if err != nil || len(b) != 8 {
		return 0
	}
	totalBytes := binary.LittleEndian.Uint64(b)
	return float32(totalBytes) / (1 << 30)
}

func detectDiskFreeGB() float32 {
	var fs unix.Statfs_t
	if err := unix.Statfs(os.TempDir(), &fs); err != nil {
		return 0
	}
	freeBytes := fs.Bavail * uint64(fs.Bsize)
	return float32(freeBytes) / (1 << 30)
}

func liveRAMUsedGB() float32 {
	// macOS doesn't expose used RAM simply via sysctl; return 0 as a safe fallback.
	// Active+wired pages are accessible via host_vm_info but require cgo.
	return 0
}

func liveDiskUsedGB() float32 {
	var fs unix.Statfs_t
	if err := unix.Statfs(os.TempDir(), &fs); err != nil {
		return 0
	}
	total := fs.Blocks * uint64(fs.Bsize)
	free := fs.Bavail * uint64(fs.Bsize)
	return float32(total-free) / (1 << 30)
}

func liveDiskTotalGB() float32 {
	var fs unix.Statfs_t
	if err := unix.Statfs(os.TempDir(), &fs); err != nil {
		return 0
	}
	return float32(fs.Blocks*uint64(fs.Bsize)) / (1 << 30)
}
