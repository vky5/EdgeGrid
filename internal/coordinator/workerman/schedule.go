package workerman

import (
	"encoding/json"
	"fmt"
	"time"

	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
)

// FindAndAssignWorker scans the worker registry for a free worker that meets
// the job's hardware requirements, marks it busy, and returns its ID.
// Uses KV revision-based update to prevent two coordinators assigning the same worker.
func (wm *WorkerManager) FindAndAssignWorker(jobID string, req *workerpb.TrainingJobRequest) (string, error) {
	keys, err := wm.kv.Keys()
	if err != nil {
		return "", fmt.Errorf("no workers registered")
	}

	for _, key := range keys {
		entry, err := wm.kv.Get(key)
		if err != nil {
			continue
		}

		var worker Worker
		if err := json.Unmarshal(entry.Value(), &worker); err != nil {
			continue
		}

		if worker.State != WorkerFree {
			continue
		}

		if !MeetsRequirements(worker.Info, req) {
			continue
		}

		worker.State = WorkerBusy
		worker.LastSeen = time.Now()
		worker.Job = &Job{
			ID:        jobID,
			Status:    "running",
			StartedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		data, err := json.Marshal(worker)
		if err != nil {
			continue
		}

		// Atomic update using the entry revision — fails if another coordinator
		// already grabbed this worker between our Get and Update.
		if _, err := wm.kv.Update(key, data, entry.Revision()); err != nil {
			continue
		}

		return worker.Info.Id, nil
	}

	return "", fmt.Errorf("no available worker meets job requirements (gpu=%v ram=%.1fGB vram=%.1fGB disk=%.1fGB)",
		req.RequiresGpu, req.MinRamGb, req.MinVramGb, req.MinDiskGb)
}

// MeetsRequirements returns true if the worker's hardware satisfies the job's requirements.
func MeetsRequirements(info *workerpb.WorkerInfo, req *workerpb.TrainingJobRequest) bool {
	if req.RequiresGpu && !info.HasGpu {
		return false
	}
	if req.MinRamGb > 0 && info.RamGb < req.MinRamGb {
		return false
	}
	if req.MinVramGb > 0 && info.GpuVramGb < req.MinVramGb {
		return false
	}
	if req.MinDiskGb > 0 && info.DiskFreeGb < req.MinDiskGb {
		return false
	}
	return true
}

// GetWorker reads a worker's current state from KV without modifying it.
func (wm *WorkerManager) GetWorker(workerID string) (*Worker, error) {
	entry, err := wm.kv.Get(workerID)
	if err != nil {
		return nil, err
	}
	var worker Worker
	if err := json.Unmarshal(entry.Value(), &worker); err != nil {
		return nil, err
	}
	return &worker, nil
}

// TryAssignWorker atomically marks a specific worker as busy for the given job.
// Uses CAS on the KV revision to prevent two coordinators assigning the same worker.
func (wm *WorkerManager) TryAssignWorker(workerID, jobID string) error {
	entry, err := wm.kv.Get(workerID)
	if err != nil {
		return fmt.Errorf("worker not found: %w", err)
	}
	var worker Worker
	if err := json.Unmarshal(entry.Value(), &worker); err != nil {
		return err
	}
	if worker.State != WorkerFree {
		return fmt.Errorf("worker %s is %s", workerID, worker.State)
	}
	worker.State = WorkerBusy
	worker.LastSeen = time.Now()
	worker.Job = &Job{
		ID:        jobID,
		Status:    "running",
		StartedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	data, err := json.Marshal(worker)
	if err != nil {
		return err
	}
	if _, err := wm.kv.Update(workerID, data, entry.Revision()); err != nil {
		return fmt.Errorf("CAS conflict — worker grabbed by another coordinator: %w", err)
	}
	return nil
}
