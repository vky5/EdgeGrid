package executor

import (
	"context"
)

// EmbeddingExecutor handles local embedding generation.
type EmbeddingExecutor struct{}

// NewEmbeddingExecutor initializes an embedding executor.
func NewEmbeddingExecutor() *EmbeddingExecutor {
	return &EmbeddingExecutor{}
}

// Execute returns a deterministic placeholder embedding.
func (e *EmbeddingExecutor) Execute(ctx context.Context, modelName string, inputText string) ([]float32, error) {
	vector := make([]float32, 128)
	length := float32(len(inputText))
	for i := 0; i < 128; i++ {
		vector[i] = length*0.01 + float32(i)*0.005
	}
	return vector, nil
}
