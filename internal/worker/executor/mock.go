package executor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
)

// MockExecutor simulates a training run without requiring Python or a GPU.
// It writes a fake checkpoint to jobDir/output/ after a short delay.
// Used for Docker Compose setups and tests.
type MockExecutor struct{}

func NewMockExecutor() *MockExecutor {
	return &MockExecutor{}
}

func (e *MockExecutor) Execute(ctx context.Context, req *workerpb.TrainingJobRequest, jobDir string) error {
	outputDir := filepath.Join(jobDir, "output")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}

	// Simulate training time
	select {
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
		return ctx.Err()
	}

	// Write a fake config.json so the output looks like a real HF checkpoint
	config := map[string]any{
		"mock":       true,
		"job_id":     req.JobId,
		"base_model": req.BaseModelRef,
		"dataset":    req.DatasetRef,
	}
	data, _ := json.MarshalIndent(config, "", "  ")
	return os.WriteFile(filepath.Join(outputDir, "config.json"), data, 0644)
}

func (e *MockExecutor) Close() error { return nil }
