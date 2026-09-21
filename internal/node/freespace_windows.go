//go:build windows

package node

import "golang.org/x/sys/windows"

// "available" honours per-user disk quotas, unlike the volume's total free space.
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
