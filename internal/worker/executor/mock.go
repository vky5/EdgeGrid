package executor

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
)

// MockExecutor generates high-quality deterministic embedding vectors for testing/mocking.
type MockExecutor struct{}

// NewMockExecutor initializes a MockExecutor.
func NewMockExecutor() *MockExecutor {
	return &MockExecutor{}
}

// Start is a no-op for MockExecutor.
func (e *MockExecutor) Start(ctx context.Context, models []string) error {
	return nil
}

// Close is a no-op for MockExecutor.
func (e *MockExecutor) Close() error {
	return nil
}

// Execute returns a deterministic, normalized embedding vector based on input hash.
func (e *MockExecutor) Execute(ctx context.Context, modelName string, inputText string) ([]float32, error) {
	dimensions := 128
	switch modelName {
	case "all-minilm":
		dimensions = 384
	case "clip":
		dimensions = 512
	case "nomic-embed-text":
		dimensions = 768
	}

	vector := make([]float32, dimensions)
	var sumSquares float64

	for i := 0; i < dimensions; i++ {
		seed := fmt.Sprintf("%s:%s:%d", modelName, inputText, i)
		hash := sha256.Sum256([]byte(seed))

		valUint := binary.BigEndian.Uint32(hash[:4])
		valFloat := (float32(valUint) / float32(math.MaxUint32)) * 2.0 - 1.0

		vector[i] = valFloat
		sumSquares += float64(valFloat * valFloat)
	}

	norm := math.Sqrt(sumSquares)
	if norm > 0 {
		for i := 0; i < dimensions; i++ {
			vector[i] = float32(float64(vector[i]) / norm)
		}
	}

	return vector, nil
}
