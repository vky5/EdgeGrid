// workman stands for workermanger
// this file handles the array of workers info and registering and deregistering them...

// and also periodically running the scheduler to check for the status of the workers

package workerman

import (
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"

	"sync"
	"time"
)

const (
	WorkerFree        = "free"
	WorkerBusy        = "busy"
	WorkerDead        = "dead"
	WorkerRegistering = "registering"
)

type Job struct {
	ID string
	Status string
	StartedAt time.Time
	UpdatedAt time.Time
}

type Worker struct {
	Info     *workerpb.WorkerInfo
	LastSeen time.Time
	State    string           // state can be busy, free, dead, registering
	Job      *Job
	SupportedModel []string

}

// keeping the array of all workers
type WorkerManager struct {
	mu            sync.Mutex // so that when a type (example a varible w Workermanager, w.Worker is updated no other goroutine intefres with it)
	workers       map[string]*Worker
}

// for safely updating the state of the worker
func (wm *WorkerManager) SetWorkerState(workerID string, state string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	worker, exists := wm.workers[workerID] // different from js actually getting the pointer to worker object 
	if !exists {
		return
	}

	worker.State = state
	worker.LastSeen = time.Now()
	if state == WorkerFree {
		worker.Job = nil
	}
}

// NewWorkerManager creates a new WorkerManager with a buffered channel for free workers
func NewWorkerManager() *WorkerManager { // bufferSize is the size of the channel for free workers
	return &WorkerManager{
		workers:     make(map[string]*Worker),
	}
}
