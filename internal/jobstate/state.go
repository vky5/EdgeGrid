package jobstate

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

type State string

const (
	StateQueued    State = "QUEUED"
	StateRunning   State = "RUNNING"
	StateCompleted State = "COMPLETED"
	StateFailed    State = "FAILED"
)

type JobStatus struct {
	JobID         string    `json:"job_id"`
	State         State     `json:"state"`
	WorkerID      string    `json:"worker_id,omitempty"`
	Error         string    `json:"error,omitempty"`
	CheckpointKey string    `json:"checkpoint_key,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func UpdateJobStatus(kv nats.KeyValue, jobID string, state State, workerID string, errMsg string, checkpointKey string) error {
	status := JobStatus{
		JobID:         jobID,
		State:         state,
		WorkerID:      workerID,
		Error:         errMsg,
		CheckpointKey: checkpointKey,
		UpdatedAt:     time.Now(),
	}

	bytes, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to marshal job status: %w", err)
	}

	_, err = kv.Put(jobID, bytes)
	if err != nil {
		return fmt.Errorf("failed to put job status into KV: %w", err)
	}
	return nil
}

func GetJobStatus(kv nats.KeyValue, jobID string) (*JobStatus, error) {
	entry, err := kv.Get(jobID)
	if err != nil {
		if err == nats.ErrKeyNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get job status from KV: %w", err)
	}

	var status JobStatus
	if err := json.Unmarshal(entry.Value(), &status); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job status: %w", err)
	}
	return &status, nil
}
