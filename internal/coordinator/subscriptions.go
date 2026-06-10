package coordinator

import (
	"context"
	"log"

	"github.com/edgegrid/edgegrid/internal/broker"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// SubscribeToWorkerEvents consumes registration and heartbeat events.
func (c *Coordinator) SubscribeToWorkerEvents(ctx context.Context) error {
	_, err := c.jsBroker.JS.Subscribe(broker.SubjectRegister, func(msg *nats.Msg) {
		var info workerpb.WorkerInfo
		if err := proto.Unmarshal(msg.Data, &info); err != nil {
			log.Printf("failed to unmarshal worker registration payload: %v", err)
			return
		}

		err := c.manager.RegisterWorker(ctx, &info)
		if err != nil {
			log.Printf("failed to register worker from NATS: %v", err)
			return
		}
		msg.Ack()
	}, nats.ManualAck())
	if err != nil {
		return err
	}

	_, err = c.jsBroker.JS.Subscribe(broker.SubjectHeartbeat, func(msg *nats.Msg) {
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

// SubscribeToResults consumes completed job responses.
func (c *Coordinator) SubscribeToResults(ctx context.Context) error {
	_, err := c.jsBroker.JS.Subscribe(broker.SubjectResults, func(msg *nats.Msg) {
		var resp workerpb.JobResponse
		if err := proto.Unmarshal(msg.Data, &resp); err != nil {
			log.Printf("failed to unmarshal job response: %v", err)
			return
		}

		if resp.Success {
			log.Printf("job %s completed by worker %s", resp.JobId, resp.WorkerId)
			log.Printf("embedding vector length: %d", len(resp.Embedding))
		} else {
			log.Printf("job %s failed on worker %s: %s", resp.JobId, resp.WorkerId, resp.Error)
		}
		msg.Ack()
	}, nats.ManualAck())

	if err != nil {
		return err
	}

	log.Println("subscribed to job result events")
	return nil
}
