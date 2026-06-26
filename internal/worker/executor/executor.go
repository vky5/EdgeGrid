package executor

import (
	"context"

	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
)

// Executor defines the interface for running a training job locally.
type Executor interface {
	// Execute runs a training job. jobDir is the isolated working directory
	// for this job — the executor writes the checkpoint to jobDir/output/.
	Execute(ctx context.Context, req *workerpb.TrainingJobRequest, jobDir string) error

	Close() error
}
