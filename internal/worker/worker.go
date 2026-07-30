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

// FinishedJob is one job this process finished in the current session
// (success or failure) — for the TUI overview, not durable history.
type FinishedJob struct {
	ID      string
	Success bool
}

type Worker struct {
	id              string
	broker          *broker.Broker
	executor        executor.Executor
	hw              hardware.Spec
	busy            atomic.Bool
	mu              sync.Mutex
	cancels         map[string]context.CancelFunc // jobID → cancel func for running jobs
	requireApproval bool

	// Session-only counters / recent list for the node Overview TUI.
	doneOK, doneFail int
	recent           []FinishedJob // newest last, capped
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

// IsBusy reports whether this worker is currently running a training job.
func (w *Worker) IsBusy() bool {
	if w == nil {
		return false
	}
	return w.busy.Load()
}

// ActiveJobIDs returns job IDs that currently have a cancel handle registered
// (i.e. jobs this process is executing). Order is not meaningful.
func (w *Worker) ActiveJobIDs() []string {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.cancels) == 0 {
		return nil
	}
	ids := make([]string, 0, len(w.cancels))
	for id := range w.cancels {
		ids = append(ids, id)
	}
	return ids
}

const maxRecentFinished = 64

// RecordFinished notes a job this process completed (session-local TUI stats).
func (w *Worker) RecordFinished(jobID string, success bool) {
	if w == nil || jobID == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if success {
		w.doneOK++
	} else {
		w.doneFail++
	}
	w.recent = append(w.recent, FinishedJob{ID: jobID, Success: success})
	if len(w.recent) > maxRecentFinished {
		w.recent = w.recent[len(w.recent)-maxRecentFinished:]
	}
}

// SessionStats returns how many jobs this process finished this session and
// the most recent ones (newest last).
func (w *Worker) SessionStats() (doneOK, doneFail int, recent []FinishedJob) {
	if w == nil {
		return 0, 0, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	recent = append([]FinishedJob(nil), w.recent...)
	return w.doneOK, w.doneFail, recent
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
