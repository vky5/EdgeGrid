package coordinator

import (
	"context"
	"log"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/coordinator/workerman"
	"github.com/edgegrid/edgegrid/internal/jobstate"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// coordinatorGroup is the NATS queue group name shared by all coordinator
// instances. NATS delivers each message to exactly one member of the group,
// preventing duplicate processing when multiple coordinators are running.
const coordinatorGroup = "coordinators"

// SubscribeToWorkerEvents consumes registration and heartbeat events.
func (c *Coordinator) SubscribeToWorkerEvents(ctx context.Context) error {
	_, err := c.jsBroker.JS.QueueSubscribe(broker.SubjectRegister, coordinatorGroup, func(msg *nats.Msg) {
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

	_, err = c.jsBroker.JS.QueueSubscribe(broker.SubjectHeartbeat, coordinatorGroup, func(msg *nats.Msg) {
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

// SubscribeToResults consumes completed job responses from workers.
func (c *Coordinator) SubscribeToResults(ctx context.Context) error {
	_, err := c.jsBroker.JS.QueueSubscribe(broker.SubjectResults, coordinatorGroup, func(msg *nats.Msg) {
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

		if resp.Success {
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
