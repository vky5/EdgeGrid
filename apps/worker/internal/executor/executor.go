package executor

import (
	"context"
)

// EmbeddingExecutor handles local model inference and embedding generation
type EmbeddingExecutor struct {
	// In the future, this can hold references to ONNX runtimes, loaded model weights, or system resources
}

// NewEmbeddingExecutor initializes a new executor instance
func NewEmbeddingExecutor() *EmbeddingExecutor {
	return &EmbeddingExecutor{}
}

// Execute performs the embedding generation logic
func (e *EmbeddingExecutor) Execute(ctx context.Context, modelName string, inputText string) ([]float32, error) {
	// Stub/Mock embedding generation
	// This keeps the inference code clean and completely isolated from message-passing details.
	vector := make([]float32, 128)
	length := float32(len(inputText))
	for i := 0; i < 128; i++ {
		vector[i] = length*0.01 + float32(i)*0.005
	}
	return vector, nil
}
