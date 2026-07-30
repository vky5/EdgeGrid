package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
)

// MockExecutor simulates a training run without requiring Python or a GPU.
// It writes a fake checkpoint to jobDir/output/ after a short delay and
// publishes a few log lines when logPublish is set (so the dashboard still
// has something to show under GET /jobs/{id}/logs).
type MockExecutor struct {
	logPublish func(jobID, line string)
}

func NewMockExecutor(logPublish func(jobID, line string)) *MockExecutor {
	return &MockExecutor{logPublish: logPublish}
}

func (e *MockExecutor) log(jobID, line string) {
	if e.logPublish != nil {
		e.logPublish(jobID, line)
	}
}

func (e *MockExecutor) Execute(ctx context.Context, req *workerpb.TrainingJobRequest, jobDir string) error {
	outputDir := filepath.Join(jobDir, "output")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}

	e.log(req.JobId, fmt.Sprintf("[mock] starting job %s (executor=mock — script not executed)", req.JobId))
	e.log(req.JobId, fmt.Sprintf("[mock] base_model=%s dataset=%s", req.BaseModelRef, req.DatasetRef))
	if len(req.TrainingScript) > 0 {
		e.log(req.JobId, fmt.Sprintf("[mock] training_script received (%d bytes) — not run under mock", len(req.TrainingScript)))
	}

	// Simulate training time with periodic log lines.
	const steps = 4
	for i := 1; i <= steps; i++ {
		select {
		case <-time.After(500 * time.Millisecond):
		case <-ctx.Done():
			e.log(req.JobId, "[mock] cancelled")
			return ctx.Err()
		}
		e.log(req.JobId, fmt.Sprintf("[mock] step %d/%d", i, steps))
	}

	// Write a fake config.json so the output looks like a real HF checkpoint
	config := map[string]any{
		"mock":       true,
		"job_id":     req.JobId,
		"base_model": req.BaseModelRef,
		"dataset":    req.DatasetRef,
	}
	data, _ := json.MarshalIndent(config, "", "  ")
	path := filepath.Join(outputDir, "config.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		e.log(req.JobId, "[mock] failed to write checkpoint: "+err.Error())
		return err
	}
	e.log(req.JobId, "[mock] wrote "+path)
	e.log(req.JobId, "[mock] done")
	return nil
}

func (e *MockExecutor) Close() error { return nil }
