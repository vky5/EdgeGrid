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
	Info           *workerpb.WorkerInfo `json:"info"`
	LastSeen       time.Time            `json:"last_seen"`
	State          string               `json:"state"`
	Job            *Job                 `json:"job"`
	SupportedModel []string             `json:"supported_model"`
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
