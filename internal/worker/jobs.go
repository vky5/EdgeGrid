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

// StartCancelListener subscribes to jobs.cancel and cancels any running job
// whose ID matches. Every worker receives every cancel message; only the worker
// that holds the job in its cancels map acts on it.
// Broadcast cancel design
func (a *Worker) StartCancelListener(ctx context.Context) {
	sub, err := a.broker.JS.Subscribe(broker.SubjectCancel, func(msg *nats.Msg) { // NATs pushes the msgs whenever they arrive and it calls the callback
		jobID := string(msg.Data)
		a.mu.Lock()
		if cancel, ok := a.cancels[jobID]; ok {
			cancel()
			log.Printf("cancelling job %s on coordinator request", jobID)
		}
		a.mu.Unlock()
		msg.Ack()
	}, nats.DeliverNew(), // Only deliver mesg published after this subscription starts (so no old jobs cancel replays)
		nats.ManualAck())
	if err != nil {
		log.Printf("failed to subscribe to cancel events: %v", err)
		return
	}
	defer sub.Unsubscribe()
	<-ctx.Done()
}

// StartJobListener pulls training jobs addressed to this worker from NATS JetStream.
func (a *Worker) StartJobListener(ctx context.Context) {
	subject := broker.SubjectTrainPrefix + a.id
	durableConsumer := "training-consumer-" + a.id

	sub, err := a.broker.JS.PullSubscribe(subject, durableConsumer, nats.ManualAck()) // creates a subscription and then explicitly asks for messages
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
			msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second)) // wait for 5 sec to get msg from the subject
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

// handleJob runs the full training pipeline for one job.
func (a *Worker) handleJob(ctx context.Context, msg *nats.Msg) {
	msgAcked := false // to prvent doing NAK after ACK

	defer func() {
		if r := recover(); r != nil {
			log.Printf("recovered panic in job handler: %v", r)
			if !msgAcked {
				msg.Nak()
			}
		}
	}()

	if !a.busy.CompareAndSwap(false, true) { // check if itnst already running something
		msg.NakWithDelay(10 * time.Second)
		return
	}
	defer a.busy.Store(false)

	var req workerpb.TrainingJobRequest
	if err := proto.Unmarshal(msg.Data, &req); err != nil {
		log.Printf("failed to unmarshal training job request: %v", err)
		msg.Term() // don't redeliver the msg is broken
		return
	}

	log.Printf("received training job %s (base_model: %s, dataset: %s %s)",
		req.JobId, req.BaseModelRef, req.DatasetType, req.DatasetRef)

	// Approval gate: ACK immediately (taking ownership of the message so after rejection it doesnt requeue)
	// wait for a human decision before proceeding. Any path that doesn't approve
	// sends a rejection notice to the coordinator.
	if a.requireApproval {
		msg.Ack()
		msgAcked = true

		if !a.awaitApproval(ctx, &req) {
			a.sendRejection(req.JobId)
			return
		}
	}

	// Per-job context so this job can be cancelled independently of the worker.
	jobCtx, cancel := context.WithCancel(ctx) // creating a new context over previous context (now either context can cancel)
	a.mu.Lock()
	a.cancels[req.JobId] = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		delete(a.cancels, req.JobId)
		a.mu.Unlock()
	}()

	kv, kvErr := a.broker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if kvErr == nil {
		_ = jobstate.UpdateJobStatus(kv, req.JobId, jobstate.StateRunning, a.id, "", "")
	}

	checkpointKey, err := a.runTrainingPipeline(jobCtx, &req)

	resp := &workerpb.JobResponse{
		JobId:    req.JobId,
		WorkerId: a.id,
	}
	if err != nil {
		log.Printf("job %s failed: %v", req.JobId, err)
		resp.Success = false
		resp.Error = err.Error()
		if kvErr == nil {
			_ = jobstate.UpdateJobStatus(kv, req.JobId, jobstate.StateFailed, a.id, err.Error(), "")
		}
	} else {
		log.Printf("job %s completed, checkpoint: %s", req.JobId, checkpointKey)
		resp.Success = true
		resp.CheckpointKey = checkpointKey
		if kvErr == nil {
			_ = jobstate.UpdateJobStatus(kv, req.JobId, jobstate.StateCompleted, a.id, "", checkpointKey)
		}
	}

	if pubErr := a.broker.PublishProto(broker.SubjectResults, resp); pubErr != nil {
		log.Printf("failed to publish result for job %s: %v", req.JobId, pubErr)
		if !msgAcked {
			msg.Nak()
		}
		return
	}

	if !msgAcked {
		msg.Ack()
	}
}
