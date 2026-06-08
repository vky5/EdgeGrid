package coordinator

import (
	"context"
	"log"

	"github.com/edgegrid/edgegrid/internal/broker"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// SubscribeToWorkerEvents consumes registration and heartbeat events from NATS
func (c *Coordinator) SubscribeToWorkerEvents(ctx context.Context) error {
	// Subscribe to registration events
	_, err := c.jsBroker.JS.Subscribe(broker.SubjectRegister, func(msg *nats.Msg) {
		var info workerpb.WorkerInfo
		if err := proto.Unmarshal(msg.Data, &info); err != nil {
			log.Printf("❌ Failed to unmarshal worker registration payload: %v", err)
			return
		}

		err := c.manager.RegisterWorker(ctx, &info)
		if err != nil {
			log.Printf("❌ Failed to register worker via NATS: %v", err)
			return
		}
		msg.Ack()
	}, nats.ManualAck())
	if err != nil {
		return err
	}

	// Subscribe to heartbeat events
	_, err = c.jsBroker.JS.Subscribe(broker.SubjectHeartbeat, func(msg *nats.Msg) {
		var req workerpb.PingRequest
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			log.Printf("❌ Failed to unmarshal heartbeat payload: %v", err)
			return
		}

		c.manager.SetWorkerState(req.Id, req.Status)
		msg.Ack()
	}, nats.ManualAck())
	if err != nil {
		return err
	}

	log.Println("👂 Subscribed to NATS worker events: workers.register, workers.heartbeat")
	return nil
}

// SubscribeToResults consumes completed job responses from workers
func (c *Coordinator) SubscribeToResults(ctx context.Context) error {
	_, err := c.jsBroker.JS.Subscribe(broker.SubjectResults, func(msg *nats.Msg) {
		var resp workerpb.JobResponse
		if err := proto.Unmarshal(msg.Data, &resp); err != nil {
			log.Printf("❌ Failed to unmarshal job response: %v", err)
			return
		}

		if resp.Success {
			log.Printf("✅ Job %s completed successfully by worker %s", resp.JobId, resp.WorkerId)
			log.Printf("   Embedding vector length: %d", len(resp.Embedding))
		} else {
			log.Printf("❌ Job %s failed on worker %s: %s", resp.JobId, resp.WorkerId, resp.Error)
		}
		msg.Ack()
	}, nats.ManualAck())

	if err != nil {
		return err
	}

	log.Println("👂 Subscribed to NATS job results: jobs.results")
	return nil
}
