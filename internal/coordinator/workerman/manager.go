package workerman

import (
	"encoding/json"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/nats-io/nats.go"
)


const (
	WorkerFree        = "free"
	WorkerBusy        = "busy"
	WorkerDead        = "dead"
	WorkerRegistering = "registering"
)

type Job struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Worker struct {
	Info     *workerpb.WorkerInfo `json:"info"`
	LastSeen time.Time            `json:"last_seen"`
	State    string               `json:"state"`
	Job      *Job                 `json:"job"`
}

type WorkerManager struct {
	kv nats.KeyValue
}

func NewWorkerManager(jsBroker *broker.Broker) (*WorkerManager, error) {
	// Set TTL to 1 minute to auto-reap dead workers
	kv, err := jsBroker.GetOrCreateKV("workers", 1*time.Minute)
	if err != nil {
		return nil, err
	}

	return &WorkerManager{
		kv: kv,
	}, nil
}

// AllWorkers returns every worker currently in the registry.
func (wm *WorkerManager) AllWorkers() ([]*Worker, error) {
	keys, err := wm.kv.Keys()
	if err != nil {
		if err == nats.ErrNoKeysFound {
			return []*Worker{}, nil
		}
		return nil, err
	}
	var workers []*Worker
	for _, key := range keys {
		entry, err := wm.kv.Get(key)
		if err != nil {
			continue
		}
		var w Worker
		if err := json.Unmarshal(entry.Value(), &w); err != nil {
			continue
		}
		workers = append(workers, &w)
	}
	return workers, nil
}

// FreeWorkerIDs returns the IDs of all workers currently in free state.
func (wm *WorkerManager) FreeWorkerIDs() ([]string, error) {
	keys, err := wm.kv.Keys()
	if err != nil {
		return nil, err
	}
	var free []string
	for _, key := range keys {
		entry, err := wm.kv.Get(key)
		if err != nil {
			continue
		}
		var w Worker
		if err := json.Unmarshal(entry.Value(), &w); err != nil {
			continue
		}
		if w.State == WorkerFree {
			free = append(free, key)
		}
	}
	return free, nil
}

func (wm *WorkerManager) SetWorkerState(workerID string, state string) {
	entry, err := wm.kv.Get(workerID)
	if err != nil {
		return
	}

	var worker Worker
	if err := json.Unmarshal(entry.Value(), &worker); err != nil {
		return
	}

	worker.State = state
	worker.LastSeen = time.Now()
	if state == WorkerFree {
		worker.Job = nil
	}

	data, err := json.Marshal(worker)
	if err != nil {
		return
	}

	_, _ = wm.kv.Put(workerID, data)
}
