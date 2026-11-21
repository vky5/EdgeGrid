//go:build windows

package node

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// the locked byte sits far past the process id written at the start,
// so a second EdgeGrid can still read the id for its message
const lockOffset = 1 << 30

// locks one byte of the file for this process without waiting; errLocked if someone else has it
func lockFile(f *os.File) error {
	ol := windows.Overlapped{Offset: lockOffset}
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errLocked
	}
	return err
}

// lets go of the lock taken by lockFile
func unlockFile(f *os.File) error {
	ol := windows.Overlapped{Offset: lockOffset}
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}
