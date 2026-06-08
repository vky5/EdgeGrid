package agent

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

// Agent coordinates the worker NATS broker client and the local executor
type Agent struct {
	id       string
	models   []string
	broker   *broker.Broker
	executor *executor.EmbeddingExecutor
}

// NewAgentWithConn instantiates the agent's dependencies using a shared NATS connection
func NewAgentWithConn(nc *nats.Conn, supportedModels []string, workerID string) (*Agent, error) {
	if workerID == "" {
		workerID = generateWorkerID()
	}

	// Initialize executor and shared broker
	exec := executor.NewEmbeddingExecutor()
	wb, err := broker.NewBroker(nc)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize shared broker: %w", err)
	}

	return &Agent{
		id:       workerID,
		models:   supportedModels,
		broker:   wb,
		executor: exec,
	}, nil
}

// Start registers the worker, triggers the heartbeat routine, and begins job pulling
func (a *Agent) Start(ctx context.Context) error {
	// Register worker capabilities with Coordinator
	err := a.RegisterWorker()
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	log.Printf("✅ Registered with Coordinator over NATS. ID: %s, Models: %v", a.id, a.models)

	// Spawn heartbeat goroutine
	go a.StartHeartbeat(ctx, 10*time.Second)
	log.Println("💓 Started heartbeat routine")

	// Start pull listeners for compatible model subjects
	for _, model := range a.models {
		go a.StartJobListener(ctx, model)
	}

	return nil
}

// Close gracefully releases resources
func (a *Agent) Close() {
	// Shared connection closed by parent Agent
}

// generateWorkerID creates a unique worker ID using hostname, timestamp, and random bytes
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
