package utils

import (
	"os"
	"path/filepath"
)

var runnerCandidates = []string{
	"internal/worker/executor/runner.py",
	"executor/runner.py",
	"runner.py",
	"../internal/worker/executor/runner.py",
}

// FindRunnerPath searches for the runner.py script relative to the working
// directory and the executable location.
func FindRunnerPath() string {
	for _, candidate := range runnerCandidates {
		if fileExists(candidate) {
			return candidate
		}
	}

	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		for _, rel := range runnerCandidates {
			candidate := filepath.Join(exeDir, rel)
			if fileExists(candidate) {
				return candidate
			}
		}
	}

	return "internal/worker/executor/runner.py"
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}