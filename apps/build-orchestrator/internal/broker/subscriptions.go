package broker

import (
	"context"
	"log"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	workerpb "github.com/edgegrid/edgegrid/apps/shared/proto/worker"
	"github.com/edgegrid/edgegrid/build-orchestrator/internal/workerman"
)

// SubscribeToWorkerEvents consumes registration and heartbeat events from NATS
func (b *JetStreamBroker) SubscribeToWorkerEvents(ctx context.Context, wm *workerman.WorkerManager) error {
	// Subscribe to registration events
	_, err := b.JS.Subscribe("workers.register", func(msg *nats.Msg) {
		var info workerpb.WorkerInfo
		if err := proto.Unmarshal(msg.Data, &info); err != nil {
			log.Printf("❌ Failed to unmarshal worker registration payload: %v", err)
			return
		}

		// Use the register method in our manager
		err := wm.RegisterWorker(ctx, &info)
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
	_, err = b.JS.Subscribe("workers.heartbeat", func(msg *nats.Msg) {
		var req workerpb.PingRequest
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			log.Printf("❌ Failed to unmarshal heartbeat payload: %v", err)
			return
		}

		// Update worker state in the manager
		wm.SetWorkerState(req.Id, req.Status)
		msg.Ack()
	}, nats.ManualAck())
	if err != nil {
		return err
	}

	log.Println("👂 Subscribed to NATS worker events: workers.register, workers.heartbeat")
	return nil
}

// SubscribeToResults consumes completed job responses from workers
func (b *JetStreamBroker) SubscribeToResults(ctx context.Context) error {
	_, err := b.JS.Subscribe("jobs.results", func(msg *nats.Msg) {
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
