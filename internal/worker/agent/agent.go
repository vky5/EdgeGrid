package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/edgegrid/edgegrid/internal/worker/broker"
	"github.com/edgegrid/edgegrid/internal/worker/executor"
)

// Agent coordinates the worker NATS broker client and the local executor
type Agent struct {
	id       string
	natsURL  string
	models   []string
	broker   *broker.WorkerBroker
	executor *executor.EmbeddingExecutor
}

// NewAgent reads environment configuration and instantiates the agent's dependencies
func NewAgent() (*Agent, error) {
	// 1. Fetch NATS url configuration
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}

	// 2. Parse supported models
	modelsEnv := os.Getenv("SUPPORTED_MODELS")
	var supportedModels []string
	if modelsEnv != "" {
		for _, m := range strings.Split(modelsEnv, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				supportedModels = append(supportedModels, m)
			}
		}
	}
	if len(supportedModels) == 0 {
		supportedModels = []string{"all-minilm"}
	}

	// 3. Generate worker identifier
	workerID := generateWorkerID()

	// 4. Initialize executor and broker
	exec := executor.NewEmbeddingExecutor()
	wb, err := broker.NewWorkerBroker(natsURL, exec)
	if err != nil {
		return nil, fmt.Errorf("failed to connect NATS: %w", err)
	}

	return &Agent{
		id:       workerID,
		natsURL:  natsURL,
		models:   supportedModels,
		broker:   wb,
		executor: exec,
	}, nil
}

// Start registers the worker, triggers the heartbeat routine, and begins job pulling
func (a *Agent) Start(ctx context.Context) error {
	// Register worker capabilities with Coordinator
	err := a.broker.RegisterWorker(a.id, a.models)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	log.Printf("✅ Registered with Coordinator over NATS. ID: %s, Models: %v", a.id, a.models)

	// Spawn heartbeat goroutine
	go a.broker.StartHeartbeat(ctx, a.id, 10*time.Second)
	log.Println("💓 Started heartbeat routine")

	// Start pull listeners for compatible model subjects
	for _, model := range a.models {
		go a.broker.StartJobListener(ctx, a.id, model)
	}

	return nil
}

// Close gracefully releases connections
func (a *Agent) Close() {
	if a.broker != nil {
		a.broker.Close()
	}
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
