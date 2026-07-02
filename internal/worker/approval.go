package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/jobstate"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/nats-io/nats.go"
)

const approvalTimeout = 60 * time.Second

type rejectionMsg struct {
	JobID    string `json:"job_id"`
	WorkerID string `json:"worker_id"`
}

// awaitApproval sets the job to PENDING_REVIEW and waits up to approvalTimeout
// for the coordinator to relay a human decision ("approve", "reject", or "cancel").
// Returns true only when the decision is exactly "approve".
func (a *Worker) awaitApproval(ctx context.Context, req *workerpb.TrainingJobRequest) bool {
	kv, err := a.broker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if err == nil {
		_ = jobstate.UpdateJobStatus(kv, req.JobId, jobstate.StatePendingReview, a.id, "", "")
	}

	log.Printf("job %s awaiting approval (timeout: %v)", req.JobId, approvalTimeout)

	subject := fmt.Sprintf(broker.SubjectWorkerDecisionFmt, a.id, req.JobId)
	decisionCh := make(chan string, 1)

	// Approval decisions are short-lived direct signals, so use NATS Core
	// instead of JetStream; the durable state is already in jobs_state KV.
	sub, err := a.broker.Conn.Subscribe(subject, func(msg *nats.Msg) {
		// This callback runs only when a decision message arrives.
		select {
		case decisionCh <- string(msg.Data):
		default: // If decisionCh is already full, don't block.
		}
	})
	if err != nil {
		log.Printf("job %s: failed to subscribe for approval signal: %v", req.JobId, err)
		return false
	}
	defer sub.Unsubscribe()

	select {
	case decision := <-decisionCh:
		log.Printf("job %s: decision received: %q", req.JobId, decision)
		return decision == "approve" // true if approve false otherwise
	case <-time.After(approvalTimeout):
		log.Printf("job %s: approval timed out after %v", req.JobId, approvalTimeout)
		return false
	case <-ctx.Done():
		log.Printf("job %s: context cancelled while awaiting approval", req.JobId)
		return false
	}
}

// sendRejection notifies the coordinator that this worker declined the job.
// The coordinator will requeue it and try the next available worker.
func (a *Worker) sendRejection(jobID string) {
	data, err := json.Marshal(rejectionMsg{JobID: jobID, WorkerID: a.id})
	if err != nil {
		log.Printf("job %s: failed to marshal rejection: %v", jobID, err)
		return
	}
	if err := a.broker.Conn.Publish(broker.SubjectWorkerReject, data); err != nil {
		log.Printf("job %s: failed to publish rejection: %v", jobID, err)
		return
	}
	log.Printf("job %s: rejection sent to coordinator", jobID)
}
