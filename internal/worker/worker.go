package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/worker/executor"
	"github.com/nats-io/nats.go"
)

// Worker coordinates the NATS client and local executor.
type Worker struct {
	id       string
	models   []string
	broker   *broker.Broker
	executor *executor.EmbeddingExecutor
}

// NewWorkerWithConn creates a worker with a shared NATS connection.
func NewWorkerWithConn(nc *nats.Conn, supportedModels []string, workerID string) (*Worker, error) {
	if workerID == "" {
		workerID = generateWorkerID()
	}

	exec := executor.NewEmbeddingExecutor()
	wb, err := broker.NewBroker(nc)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize shared broker: %w", err)
	}

	return &Worker{
		id:       workerID,
		models:   supportedModels,
		broker:   wb,
		executor: exec,
	}, nil
}

// Start registers the worker and starts background listeners.
func (w *Worker) Start(ctx context.Context) error {
	err := w.RegisterWorker()
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	log.Printf("registered worker %s with models %v", w.id, w.models)

	go w.StartHeartbeat(ctx, 10*time.Second)
	log.Println("started heartbeat routine")

	for _, model := range w.models {
		go w.StartJobListener(ctx, model)
	}

	return nil
}

// generateWorkerID creates a unique worker ID.
func generateWorkerID() string {
	workerID := os.Getenv("WORKER_ID")
	if workerID != "" {
		return workerID
	}

	hostname, _ := os.Hostname()
	timestamp := time.Now().UnixNano()
	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	randHex := hex.EncodeToString(randBytes)

	return fmt.Sprintf("worker-%s-%d-%s", hostname, timestamp, randHex)
}
