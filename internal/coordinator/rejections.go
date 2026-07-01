package coordinator

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/jobstate"
	"github.com/nats-io/nats.go"
)

type workerRejectionMsg struct {
	JobID    string `json:"job_id"`
	WorkerID string `json:"worker_id"`
}

// SubscribeToRejections listens on workers.reject (NATS Core).
// When a worker rejects or times out on a job, it publishes here.
// The coordinator requeues the job (recording the rejecting worker) and
// attempts to dispatch it to the next available worker.
func (c *Coordinator) SubscribeToRejections(ctx context.Context) error {
	_, err := c.jsBroker.Conn.Subscribe(broker.SubjectWorkerReject, func(msg *nats.Msg) { //nolint:revive
		var rej workerRejectionMsg
		if err := json.Unmarshal(msg.Data, &rej); err != nil {
			log.Printf("SubscribeToRejections: malformed message: %v", err)
			return
		}

		kv, err := c.jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
		if err != nil {
			log.Printf("SubscribeToRejections: failed to get KV for job %s: %v", rej.JobID, err)
			return
		}

		// Don't requeue jobs that have already reached a terminal state
		// (e.g., cancelled by user while the worker was in the approval window).
		status, _ := jobstate.GetJobStatus(kv, rej.JobID)
		if status == nil {
			return
		}
		switch status.State {
		case jobstate.StateCancelled, jobstate.StateCompleted, jobstate.StateFailed:
			log.Printf("SubscribeToRejections: job %s is %s, ignoring rejection", rej.JobID, status.State)
			return
		}

		log.Printf("job %s rejected by worker %s — requeueing", rej.JobID, rej.WorkerID)
		if err := jobstate.RequeueJobAfterRejection(kv, rej.JobID, rej.WorkerID); err != nil {
			log.Printf("SubscribeToRejections: failed to requeue job %s: %v", rej.JobID, err)
			return
		}

		// Dispatch to every currently free worker; TryDispatchQueued will
		// skip this job for any worker already in its RejectedBy list.
		freeIDs, err := c.manager.FreeWorkerIDs()
		if err != nil {
			log.Printf("SubscribeToRejections: failed to list free workers: %v", err)
			return
		}
		for _, workerID := range freeIDs {
			go c.TryDispatchQueued(ctx, workerID)
		}
	})
	return err
}
