package agent

import (
	"context"
	"log"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// RegisterWorker registers the worker capabilities with the Coordinator
func (a *Agent) RegisterWorker() error {
	info := &workerpb.WorkerInfo{
		Id:             a.id,
		SupportedModel: a.models,
	}

	err := a.broker.PublishProto(broker.SubjectRegister, info)
	if err != nil {
		return err
	}
	return nil
}

// StartHeartbeat sends periodic ping status updates to NATS
func (a *Agent) StartHeartbeat(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			req := &workerpb.PingRequest{
				Id:     a.id,
				Status: "free",
			}

			err := a.broker.PublishProto(broker.SubjectHeartbeat, req)
			if err != nil {
				log.Printf("❌ Failed to publish heartbeat: %v", err)
			}
		}
	}
}

// StartJobListener pulls jobs from the model stream and executes them
func (a *Agent) StartJobListener(ctx context.Context, model string) {
	subject := broker.SubjectJobsPrefix + model
	durableConsumer := "consumer-" + model
	sub, err := a.broker.JS.PullSubscribe(
		subject,
		durableConsumer,
		nats.ManualAck(),
	)
	if err != nil {
		log.Printf("❌ Failed to subscribe to subject %s: %v", subject, err)
		return
	}

	log.Printf("👂 Listening for jobs on %s (Durable: %s)", subject, durableConsumer)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
			if err != nil {
				if err == nats.ErrTimeout {
					continue
				}
				log.Printf("❌ Error fetching message on %s: %v", subject, err)
				time.Sleep(1 * time.Second)
				continue
			}

			if len(msgs) == 0 {
				continue
			}

			a.handleJob(msgs[0])
		}
	}
}

// handleJob processes a single job request
func (a *Agent) handleJob(msg *nats.Msg) {
	// 1. Recover from panics to keep the worker listener alive
	defer func() {
		if r := recover(); r != nil {
			log.Printf("🔥 Panic recovered while processing job: %v", r)
			msg.Nak()
		}
	}()

	// 2. Decode the incoming protobuf payload
	var req workerpb.JobRequest
	if err := proto.Unmarshal(msg.Data, &req); err != nil {
		log.Printf("❌ Failed to unmarshal JobRequest: %v", err)
		msg.Term()
		return
	}

	log.Printf("📥 Received job %s for model %s. Processing...", req.JobId, req.ModelName)

	// 3. Process the job: delegate to the executor
	embeddingResult, err := a.executor.Execute(context.Background(), req.ModelName, req.InputText)
	if err != nil {
		log.Printf("❌ Job execution failed: %v", err)
		msg.Nak()
		return
	}

	// 4. Construct JobResponse
	resp := &workerpb.JobResponse{
		JobId:     req.JobId,
		Success:   true,
		Embedding: embeddingResult,
		WorkerId:  a.id,
	}

	// 5. Publish result back to NATS results queue
	err = a.broker.PublishProto(broker.SubjectResults, resp)
	if err != nil {
		log.Printf("❌ Failed to publish result: %v", err)
		msg.Nak()
		return
	}

	// 6. Acknowledge completed work
	if err := msg.Ack(); err != nil {
		log.Printf("⚠️ Failed to ack message: %v", err)
	}
	log.Printf("✅ Successfully processed job %s and published embeddings", req.JobId)
}
