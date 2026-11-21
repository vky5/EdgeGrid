//go:build unix

package node

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// locks the whole file for this process without waiting; errLocked if someone else has it
func lockFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return errLocked
	}
	return err
}

// lets go of the lock taken by lockFile
func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
