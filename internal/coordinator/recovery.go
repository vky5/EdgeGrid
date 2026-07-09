package coordinator

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/edgegrid/edgegrid/internal/jobstate"
)

// datasetUploadTimeout bounds how long a job may sit QUEUED waiting for its
// object_store dataset upload before it's failed outright — otherwise a
// client that submits and never calls Upload leaves an entry parked in
// jobs_state forever, since nothing else clears AwaitingDataset.
const datasetUploadTimeout = 10 * time.Minute

// StartStaleJobRecovery periodically scans for RUNNING jobs whose worker has
// disappeared from the KV store and requeues them so they can be re-dispatched.
func (c *Coordinator) StartStaleJobRecovery(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.recoverStaleJobs(ctx)
		}
	}
}

func (c *Coordinator) recoverStaleJobs(ctx context.Context) {
	jobsKV, err := c.jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if err != nil {
		log.Printf("stale job recovery: failed to get jobs_state KV: %v", err)
		return
	}

	keys, err := jobsKV.Keys()
	if err != nil {
		return
	}

	var requeued int
	for _, key := range keys {
		entry, err := jobsKV.Get(key)
		if err != nil {
			continue
		}
		var status jobstate.JobStatus
		if err := json.Unmarshal(entry.Value(), &status); err != nil {
			continue
		}

		if status.State == jobstate.StateQueued && status.AwaitingDataset {
			if time.Since(status.UpdatedAt) > datasetUploadTimeout {
				log.Printf("stale job recovery: job %s never received its dataset upload, failing", status.JobID)
				if err := jobstate.UpdateJobStatus(jobsKV, status.JobID, jobstate.StateFailed, "", "dataset upload timed out", ""); err != nil {
					log.Printf("stale job recovery: failed to fail job %s: %v", status.JobID, err)
				}
			}
			continue
		}

		if (status.State != jobstate.StateRunning && status.State != jobstate.StatePendingReview) || status.WorkerID == "" {
			continue
		}

		// Check whether the worker still exists in the workers KV.
		if _, err := c.manager.GetWorker(status.WorkerID); err == nil {
			continue // worker is alive
		}

		log.Printf("stale job recovery: worker %s gone, requeueing job %s", status.WorkerID, status.JobID)
		if err := jobstate.RequeueJob(jobsKV, status.JobID); err != nil {
			log.Printf("stale job recovery: failed to requeue job %s: %v", status.JobID, err)
			continue
		}
		requeued++
	}

	if requeued == 0 {
		return
	}

	// Try to dispatch the newly queued jobs to any free workers.
	freeIDs, err := c.manager.FreeWorkerIDs()
	if err != nil {
		log.Printf("stale job recovery: failed to list free workers: %v", err)
		return
	}
	for _, workerID := range freeIDs {
		go c.TryDispatchQueued(ctx, workerID)
	}
}
