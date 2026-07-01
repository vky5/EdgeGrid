package coordinator

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/coordinator/workerman"
	"github.com/edgegrid/edgegrid/internal/jobstate"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"google.golang.org/protobuf/proto"
)

// TryDispatchQueued scans the job state KV for the oldest QUEUED job that
// workerID can handle and dispatches it directly to that worker.
// Called when a worker becomes available (new registration or job completion).
func (c *Coordinator) TryDispatchQueued(ctx context.Context, workerID string) {
	worker, err := c.manager.GetWorker(workerID)
	if err != nil || worker.State != workerman.WorkerFree {
		return
	}

	kv, err := c.jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if err != nil {
		log.Printf("TryDispatchQueued: failed to get jobs_state KV: %v", err)
		return
	}

	keys, err := kv.Keys()
	if err != nil {
		return
	}

	var bestStatus *jobstate.JobStatus
	var bestReq *workerpb.TrainingJobRequest

	for _, key := range keys {
		entry, err := kv.Get(key)
		if err != nil {
			continue
		}
		var status jobstate.JobStatus
		if err := json.Unmarshal(entry.Value(), &status); err != nil {
			continue
		}
		if status.State != jobstate.StateQueued || len(status.RequestProto) == 0 {
			continue
		}
		if workerAlreadyRejected(status.RejectedBy, workerID) {
			continue
		}
		var req workerpb.TrainingJobRequest
		if err := proto.Unmarshal(status.RequestProto, &req); err != nil {
			continue
		}
		if !workerman.MeetsRequirements(worker.Info, worker.Stats, &req) {
			continue
		}
		if bestStatus == nil || status.UpdatedAt.Before(bestStatus.UpdatedAt) {
			s := status
			bestStatus = &s
			bestReq = &req
		}
	}

	if bestStatus == nil {
		return
	}

	if err := c.manager.TryAssignWorker(workerID, bestStatus.JobID); err != nil {
		log.Printf("TryDispatchQueued: failed to assign worker %s for job %s: %v", workerID, bestStatus.JobID, err)
		return
	}

	subject := broker.SubjectTrainPrefix + workerID
	if err := c.jsBroker.PublishProto(subject, bestReq); err != nil {
		log.Printf("TryDispatchQueued: failed to dispatch job %s to worker %s: %v", bestStatus.JobID, workerID, err)
		c.manager.SetWorkerState(workerID, workerman.WorkerFree)
		return
	}

	log.Printf("dispatched queued job %s to newly available worker %s", bestStatus.JobID, workerID)
}

func workerAlreadyRejected(rejectedBy []string, workerID string) bool {
	for _, id := range rejectedBy {
		if id == workerID {
			return true
		}
	}
	return false
}
