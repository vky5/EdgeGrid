//go:build unix

package node

import "golang.org/x/sys/unix"

// Bavail, not Bfree: Bfree includes blocks reserved for root.
// Field types differ between Linux and macOS, hence the conversions.
func freeBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
