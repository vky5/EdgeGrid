package worker

import (
	"context"
	"log"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/jobstate"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// RegisterWorker publishes the worker's capabilities to the coordinator.
func (a *Worker) RegisterWorker() error {
	info := &workerpb.WorkerInfo{
		Id:             a.id,
		SupportedModel: a.models,
	}
	return a.broker.PublishProto(broker.SubjectRegister, info)
}

// StartHeartbeat sends periodic worker status updates to the coordinator.
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
			if err := a.broker.PublishProto(broker.SubjectHeartbeat, req); err != nil {
				log.Printf("failed to publish heartbeat: %v", err)
			}
		}
	}
}

// StartJobListener pulls training jobs for one model type from NATS JetStream.
func (a *Worker) StartJobListener(ctx context.Context, model string) {
	subject := broker.SubjectTrainPrefix + model
	durableConsumer := "training-consumer-" + model

	sub, err := a.broker.JS.PullSubscribe(subject, durableConsumer, nats.ManualAck())
	if err != nil {
		log.Printf("failed to subscribe to %s: %v", subject, err)
		return
	}

	log.Printf("listening for training jobs on %s", subject)

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
				log.Printf("error fetching from %s: %v", subject, err)
				time.Sleep(1 * time.Second)
				continue
			}

			if len(msgs) == 0 {
				continue
			}

			a.handleJob(ctx, msgs[0])
		}
	}
}

// handleJob processes one training job request.
func (a *Worker) handleJob(ctx context.Context, msg *nats.Msg) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("recovered panic in job handler: %v", r)
			msg.Nak()
		}
	}()

	var req workerpb.TrainingJobRequest
	if err := proto.Unmarshal(msg.Data, &req); err != nil {
		log.Printf("failed to unmarshal training job request: %v", err)
		msg.Term()
		return
	}

	log.Printf("received training job %s (base_model: %s, dataset: %s %s)",
		req.JobId, req.BaseModelRef, req.DatasetType, req.DatasetRef)

	kv, kvErr := a.broker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if kvErr == nil {
		_ = jobstate.UpdateJobStatus(kv, req.JobId, jobstate.StateRunning, a.id, "", "")
	}

	// TODO: implement full training execution pipeline:
	//   1. disk pre-check (syscall.Statfs)
	//   2. pull dataset (HF download or broker.PullDataset)
	//   3. resolve venv (SHA256 cache)
	//   4. check for resume checkpoint
	//   5. run executor.Execute(ctx, &req, jobDir)
	//   6. broker.PushCheckpoint(req.JobId, checkpointReader)
	//   7. publish JobResponse to jobs.results
	log.Printf("training executor not yet implemented — NAKing job %s for redelivery", req.JobId)
	msg.Nak()
}
