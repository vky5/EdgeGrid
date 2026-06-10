package workerman

import (
	"sync"
	"time"

	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
)

const (
	WorkerFree        = "free"
	WorkerBusy        = "busy"
	WorkerDead        = "dead"
	WorkerRegistering = "registering"
)

type Job struct {
	ID        string
	Status    string
	StartedAt time.Time
	UpdatedAt time.Time
}

type Worker struct {
	Info           *workerpb.WorkerInfo
	LastSeen       time.Time
	State          string
	Job            *Job
	SupportedModel []string
}

type WorkerManager struct {
	mu      sync.Mutex
	workers map[string]*Worker
}

func (wm *WorkerManager) SetWorkerState(workerID string, state string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	worker, exists := wm.workers[workerID]
	if !exists {
		return
	}

	worker.State = state
	worker.LastSeen = time.Now()
	if state == WorkerFree {
		worker.Job = nil
	}
}

func NewWorkerManager() *WorkerManager {
	return &WorkerManager{
		workers: make(map[string]*Worker),
	}
}
