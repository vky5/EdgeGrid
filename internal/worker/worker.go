package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/worker/executor"
	"github.com/edgegrid/edgegrid/internal/worker/hardware"
	"github.com/nats-io/nats.go"
)

const (
	WorkerFree = "free"
	WorkerBusy = "busy"
)

type Worker struct {
	id              string
	broker          *broker.Broker
	executor        executor.Executor
	hw              hardware.Spec
	busy            atomic.Bool
	mu              sync.Mutex
	cancels         map[string]context.CancelFunc // jobID → cancel func for running jobs
	requireApproval bool
}

// Create a worker object with the connection
func NewWorkerWithConn(nc *nats.Conn, workerID string, exec executor.Executor, replicas int, requireApproval bool) (*Worker, error) {
	if workerID == "" {
		workerID = generateWorkerID()
	}

	wb, err := broker.NewBroker(nc, replicas)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize shared broker: %w", err)
	}

	return &Worker{
		id:              workerID,
		broker:          wb,
		executor:        exec,
		cancels:         make(map[string]context.CancelFunc),
		requireApproval: requireApproval,
	}, nil
}

func (w *Worker) Start(ctx context.Context) error {
	w.hw = hardware.Detect()

	if err := w.RegisterWorker(); err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	log.Printf("registered worker %s", w.id)

	go w.StartHeartbeat(ctx, 10*time.Second)
	go w.StartJobListener(ctx)
	go w.StartCancelListener(ctx)

	return nil
}

func (w *Worker) Close() {
	if w.executor != nil {
		_ = w.executor.Close()
	}
}

func generateWorkerID() string {
	if id := os.Getenv("WORKER_ID"); id != "" {
		return id
	}
	hostname, _ := os.Hostname()
	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	return fmt.Sprintf("worker-%s-%s", hostname, hex.EncodeToString(randBytes))
}
