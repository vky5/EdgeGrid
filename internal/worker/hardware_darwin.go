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
