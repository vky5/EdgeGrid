//go:build darwin

package hardware

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

func DiskFreeGB() float32 {
	var fs unix.Statfs_t
	if err := unix.Statfs(os.TempDir(), &fs); err != nil {
		return 0
	}
	freeBytes := fs.Bavail * uint64(fs.Bsize)
	return float32(freeBytes) / (1 << 30)
}

func LiveRAMUsedGB() float32 {
	// macOS doesn't expose used RAM simply via sysctl; return 0 as a safe fallback.
	// Active+wired pages are accessible via host_vm_info but require cgo.
	return 0
}

func LiveDiskUsedGB() float32 {
	var fs unix.Statfs_t
	if err := unix.Statfs(os.TempDir(), &fs); err != nil {
		return 0
	}
	total := fs.Blocks * uint64(fs.Bsize)
	free := fs.Bavail * uint64(fs.Bsize)
	return float32(total-free) / (1 << 30)
}

func LiveDiskTotalGB() float32 {
	var fs unix.Statfs_t
	if err := unix.Statfs(os.TempDir(), &fs); err != nil {
		return 0
	}
	return float32(fs.Blocks*uint64(fs.Bsize)) / (1 << 30)
}
