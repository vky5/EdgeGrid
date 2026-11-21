//go:build windows

package node

import "golang.org/x/sys/windows"

// free space this user can write to, after any per-user disk quota
func freeBytes(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &available, &total, &totalFree); err != nil {
		return 0, err
	}
	return available, nil
}
