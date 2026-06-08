package broker

import (
	"context"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
)

// StartJobListener pulls jobs from the model stream and executes them
func (wb *WorkerBroker) StartJobListener(ctx context.Context, workerID string, model string) {
	subject := "jobs.build." + model
	durableConsumer := "consumer-" + model // pointer on NATS side to track worker progress
	sub, err := wb.JS.PullSubscribe(
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
			// Fetch 1 message, wait up to 5 seconds
			msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second)) // wait for NATS for 5 sec to send a msg
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

			// Process the retrieved job in an isolated helper function
			wb.handleJob(msgs[0], workerID)
		}
	}
}

// handleJob processes a single job request. Isolating this logic prevents
// defer resource leaks, allows panic recovery, and improves unit testability.
func (wb *WorkerBroker) handleJob(msg *nats.Msg, workerID string) {
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
		msg.Term() // terminate message so it is not redelivered (poison pill)
		return
	}

	log.Printf("📥 Received job %s for model %s. Processing...", req.JobId, req.ModelName)

	// 3. Process the job: delegate to the executor
	embeddingResult, err := wb.Executor.Execute(context.Background(), req.ModelName, req.InputText)
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
		WorkerId:  workerID,
	}

	respData, err := proto.Marshal(resp)
	if err != nil {
		log.Printf("❌ Failed to marshal JobResponse: %v", err)
		msg.Nak()
		return
	}

	// 5. Publish result back to NATS results queue
	_, err = wb.JS.Publish("jobs.results", respData)
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
