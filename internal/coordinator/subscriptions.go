package coordinator

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/coordinator/workerman"
	"github.com/edgegrid/edgegrid/internal/jobstate"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// Each consumer group name must be unique per subject because JetStream
// derives the durable consumer name from the queue group name. Two
// subscriptions with the same group name but different subjects would cause
// "subject does not match consumer" errors.
const (
	groupRegister  = "coord-register"
	groupHeartbeat = "coord-heartbeat"
	groupResults   = "coord-results"
)

// SubscribeToWorkerEvents consumes registration and heartbeat events.
func (c *Coordinator) SubscribeToWorkerEvents(ctx context.Context) error {
	_, err := c.jsBroker.JS.QueueSubscribe(broker.SubjectRegister, groupRegister, func(msg *nats.Msg) {
		var info workerpb.WorkerInfo
		if err := proto.Unmarshal(msg.Data, &info); err != nil {
			log.Printf("failed to unmarshal worker registration payload: %v", err)
			return
		}
		if err := c.manager.RegisterWorker(ctx, &info); err != nil {
			log.Printf("failed to register worker: %v", err)
			return
		}
		go c.TryDispatchQueued(ctx, info.Id)
		msg.Ack()
	}, nats.ManualAck())
	if err != nil {
		return err
	}

	_, err = c.jsBroker.JS.QueueSubscribe(broker.SubjectHeartbeat, groupHeartbeat, func(msg *nats.Msg) {
		var req workerpb.PingRequest
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			log.Printf("failed to unmarshal heartbeat payload: %v", err)
			return
		}
		c.manager.SetWorkerState(req.Id, req.Status)
		msg.Ack()
	}, nats.ManualAck())
	if err != nil {
		return err
	}

	log.Println("subscribed to worker registration and heartbeat events")
	return nil
}

// SubscribeToWorkerStats listens for live resource usage published by workers at
// each heartbeat. Uses NATS Core (not JetStream) — these are ephemeral updates.
func (c *Coordinator) SubscribeToWorkerStats() error {
	_, err := c.jsBroker.Conn.Subscribe(broker.SubjectWorkerStatsWildcard, func(msg *nats.Msg) {
		// Subject is "workers.stats.<workerID>" — extract the ID from the last segment.
		parts := strings.Split(msg.Subject, ".")
		if len(parts) != 3 {
			return
		}
		workerID := parts[2]
		var stats workerman.WorkerStats
		if err := json.Unmarshal(msg.Data, &stats); err != nil {
			return
		}
		c.manager.UpdateWorkerStats(workerID, stats)
	})
	if err != nil {
		return err
	}
	log.Println("subscribed to worker live stats")
	return nil
}

// SubscribeToResults consumes completed job responses from workers.
func (c *Coordinator) SubscribeToResults(ctx context.Context) error {
	_, err := c.jsBroker.JS.QueueSubscribe(broker.SubjectResults, groupResults, func(msg *nats.Msg) {
		var resp workerpb.JobResponse
		if err := proto.Unmarshal(msg.Data, &resp); err != nil {
			log.Printf("failed to unmarshal job response: %v", err)
			return
		}

		kv, err := c.jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
		if err != nil {
			log.Printf("failed to get jobs_state KV: %v", err)
			msg.Nak()
			return
		}

		// Skip state update if the coordinator already marked this job cancelled —
		// the worker result arriving after cancellation should not overwrite it.
		current, _ := jobstate.GetJobStatus(kv, resp.JobId)
		if current != nil && current.State == jobstate.StateCancelled {
			log.Printf("job %s result ignored (already cancelled)", resp.JobId)
		} else if resp.Success {
			log.Printf("job %s completed by worker %s (checkpoint: %s)", resp.JobId, resp.WorkerId, resp.CheckpointKey)
			_ = jobstate.UpdateJobStatus(kv, resp.JobId, jobstate.StateCompleted, resp.WorkerId, "", resp.CheckpointKey)
		} else {
			log.Printf("job %s failed on worker %s: %s", resp.JobId, resp.WorkerId, resp.Error)
			_ = jobstate.UpdateJobStatus(kv, resp.JobId, jobstate.StateFailed, resp.WorkerId, resp.Error, "")
		}

		// Mark the worker free and immediately try to dispatch any queued jobs to it
		if resp.WorkerId != "" {
			c.manager.SetWorkerState(resp.WorkerId, workerman.WorkerFree)
			go c.TryDispatchQueued(ctx, resp.WorkerId)
		}

		msg.Ack()
	}, nats.ManualAck())
	if err != nil {
		return err
	}

	log.Println("subscribed to job result events")
	return nil
}
