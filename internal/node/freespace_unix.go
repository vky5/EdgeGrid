//go:build unix

package node

import "golang.org/x/sys/unix"

// free space this user can write to, not counting space kept for root
// the conversions are there because Linux and macOS use different number types
func freeBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
