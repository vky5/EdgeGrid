package node

import (
	"errors"
	"io/fs"
	"log"
	"path/filepath"

	"github.com/edgegrid/edgegrid/internal/executor"
)

// taskRegistryFile lists the task kinds this node can run, relative to DataDir.
const taskRegistryFile = "tasks.json"

// taskRunsDir holds each run's scratch working dir, relative to DataDir.
const taskRunsDir = "runs"

// loadExecutor never fails: a missing or broken registry leaves the node
// running no task kinds, the same best-effort rule as the history database.
func loadExecutor(dataDir string) *executor.Executor {
	path := filepath.Join(dataDir, taskRegistryFile)
	runtimes := []executor.Runtime{executor.Bare{}}
	workRoot := filepath.Join(dataDir, taskRunsDir)

	defs, err := executor.LoadDefinitions(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		log.Printf("task: no registry at %s — this node runs no task kinds", path)
	case err != nil:
		log.Printf("warning: task registry unusable, this node runs no task kinds: %v", err)
	}

	e, err := executor.New(defs, runtimes, workRoot)
	if err != nil {
		log.Printf("warning: task registry rejected, this node runs no task kinds: %v", err)
		e, _ = executor.New(nil, runtimes, workRoot)
	}
	return e
}
