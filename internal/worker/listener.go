package worker

import (
	"context"
	"log"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// RegisterWorker publishes the worker capabilities.
func (a *Worker) RegisterWorker() error {
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

// StartHeartbeat sends periodic worker status updates.
func (a *Worker) StartHeartbeat(ctx context.Context, interval time.Duration) {
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
				log.Printf("failed to publish heartbeat: %v", err)
			}
		}
	}
}

// StartJobListener pulls and executes jobs for one model.
func (a *Worker) StartJobListener(ctx context.Context, model string) {
	subject := broker.SubjectJobsPrefix + model
	durableConsumer := "consumer-" + model
	sub, err := a.broker.JS.PullSubscribe(
		subject,
		durableConsumer,
		nats.ManualAck(),
	)
	if err != nil {
		log.Printf("failed to subscribe to subject %s: %v", subject, err)
		return
	}

	log.Printf("listening for jobs on %s with durable consumer %s", subject, durableConsumer)

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
				log.Printf("error fetching message on %s: %v", subject, err)
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

// handleJob processes one job request.
func (a *Worker) handleJob(msg *nats.Msg) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("recovered panic while processing job: %v", r)
			msg.Nak()
		}
	}()

	var req workerpb.JobRequest
	if err := proto.Unmarshal(msg.Data, &req); err != nil {
		log.Printf("failed to unmarshal job request: %v", err)
		msg.Term()
		return
	}

	log.Printf("received job %s for model %s", req.JobId, req.ModelName)

	embeddingResult, err := a.executor.Execute(context.Background(), req.ModelName, req.InputText)
	if err != nil {
		log.Printf("job execution failed: %v", err)
		msg.Nak()
		return
	}

	resp := &workerpb.JobResponse{
		JobId:     req.JobId,
		Success:   true,
		Embedding: embeddingResult,
		WorkerId:  a.id,
	}

	err = a.broker.PublishProto(broker.SubjectResults, resp)
	if err != nil {
		log.Printf("failed to publish job result: %v", err)
		msg.Nak()
		return
	}

	if err := msg.Ack(); err != nil {
		log.Printf("failed to ack message: %v", err)
	}
	log.Printf("processed job %s", req.JobId)
}
