package node

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// the file that says an EdgeGrid is running on this profile, inside the profile's folder
// e.g. ~/.edgegrid/test-cluster/edgegrid.lock, holding the running process's id
const lockFileName = "edgegrid.lock"

// another EdgeGrid is already running on the same profile
var ErrProfileInUse = errors.New("profile already in use")

// the operating system said someone else holds the lock
var errLocked = errors.New("locked by another process")

// a held lock on one profile's folder; let go of it with release
type profileLock struct {
	f *os.File
}

// takes the profile's lock, or fails at once if another EdgeGrid holds it
// the operating system frees it when the process ends, even after a crash or kill -9
//
//	first  `edgegrid up` on test-cluster    takes ~/.edgegrid/test-cluster/edgegrid.lock, runs
//	`edgegrid up` on 2nd-cluster            its own lock file, runs alongside
//	second `edgegrid up` on test-cluster    refused: "profile already in use: ~/.edgegrid/test-cluster
//	                                        is used by another EdgeGrid (pid 4242); stop it or use another profile"
func lockProfile(dataDir string) (*profileLock, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("lock profile: %w", err)
	}
	path := filepath.Join(dataDir, lockFileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock profile: %w", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		if errors.Is(err, errLocked) {
			return nil, fmt.Errorf("%w: %s is used by another EdgeGrid (pid %s); stop it or use another profile",
				ErrProfileInUse, dataDir, lockHolder(path))
		}
		return nil, fmt.Errorf("lock profile %s: %w", dataDir, err)
	}
	// only for the message a second EdgeGrid shows; the lock itself is what counts
	if err := f.Truncate(0); err == nil {
		f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return &profileLock{f: f}, nil
}

// lets go of the lock so another EdgeGrid can use the profile; safe to call twice
func (l *profileLock) release() {
	if l == nil || l.f == nil {
		return
	}
	unlockFile(l.f)
	l.f.Close()
	l.f = nil
}

// the process id written in the lock file, e.g. "4242", or "unknown"
func lockHolder(path string) string {
	b, err := os.ReadFile(path)
	if pid := strings.TrimSpace(string(b)); err == nil && pid != "" {
		return pid
	}
	return "unknown"
}
