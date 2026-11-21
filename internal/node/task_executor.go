package node

import (
	"errors"
	"io/fs"
	"log"
	"path/filepath"

	"github.com/edgegrid/edgegrid/internal/executor"
)

// the file listing the task kinds this node can run, inside the data folder
// e.g. [{"kind":"hello","runtime":"bare","entrypoint":["echo","hello world"]}]
const taskRegistryFile = "tasks.json"

// the folder holding each run's own working folder, inside the data folder
// e.g. runs/run-1234567, removed when that run ends
const taskRunsDir = "runs"

// builds the task runner, and never fails
// a missing or broken tasks.json just means this node runs no task kinds
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
