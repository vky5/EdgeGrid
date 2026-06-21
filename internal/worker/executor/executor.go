package executor

import "context"

// Executor defines the interface for local model workloads.
type Executor interface {
	Start(ctx context.Context, models []string) error
	Execute(ctx context.Context, modelName string, inputText string) ([]float32, error)
	Close() error
}
