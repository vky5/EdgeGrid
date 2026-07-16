// Package jobsapi holds the HTTP handlers for the job lifecycle: submit,
// list, get status, cancel, approve/reject a pending review, stream logs,
// upload a dataset, and download a checkpoint. Each function is a pure HTTP
// handler — the route table itself lives in the parent coordinator package's
// router.go, which only wires paths to these functions.
package jobsapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/coordinator/workerman"
	"github.com/edgegrid/edgegrid/internal/jobstate"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// maxSubmitBodyBytes bounds POST /jobs request bodies (training_script +
// requirements are plain text, not model weights or datasets).
const maxSubmitBodyBytes = 2 << 20 // 2 MiB

type SubmitJobRequest struct {
	TrainingScript     string  `json:"training_script"`      // Python script content (plain text)
	Requirements       string  `json:"requirements"`         // requirements.txt content
	DatasetType        string  `json:"dataset_type"`         // "hf" or "object_store"
	DatasetRef         string  `json:"dataset_ref"`          // HF dataset name or object store key
	BaseModelType      string  `json:"base_model_type"`      // "hf" or "object_store"
	BaseModelRef       string  `json:"base_model_ref"`       // HF model name or object store key
	TrainingConfigJSON string  `json:"training_config_json"` // arbitrary JSON passed to training script
	RequiresGPU        bool    `json:"requires_gpu"`
	MinRAMGB           float32 `json:"min_ram_gb"`
	MinVRAMGB          float32 `json:"min_vram_gb"`
	MinDiskGB          float32 `json:"min_disk_gb"`
}

type SubmitJobResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

// jobStatusPublic is JobStatus with request_proto stripped — it's large binary
// that the dashboard doesn't need and bloats every list response.
type jobStatusPublic struct {
	JobID         string         `json:"job_id"`
	State         jobstate.State `json:"state"`
	WorkerID      string         `json:"worker_id,omitempty"`
	Error         string         `json:"error,omitempty"`
	CheckpointKey string         `json:"checkpoint_key,omitempty"`
	UpdatedAt     time.Time      `json:"updated_at"`
	SubmittedBy   string         `json:"submitted_by,omitempty"`
}

func generateJobID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// tryDispatch attempts to assign a free worker to req and publish it to that
// worker's private subject. If no worker is available, it returns nil and
// the job is left QUEUED for a later dispatch trigger (worker registration,
// job completion, stale recovery, or — for object_store jobs — the dataset
// upload finally landing).
func tryDispatch(jsBroker *broker.Broker, manager *workerman.WorkerManager, jobID string, req *workerpb.TrainingJobRequest) error {
	workerID, err := manager.FindAndAssignWorker(jobID, req)
	if err != nil {
		log.Printf("no free worker for job %s, leaving queued: %v", jobID, err)
		return nil
	}

	subject := broker.SubjectTrainPrefix + workerID
	if pubErr := jsBroker.PublishProto(subject, req); pubErr != nil {
		log.Printf("failed to dispatch job %s to worker %s: %v", jobID, workerID, pubErr)
		manager.SetWorkerState(workerID, workerman.WorkerFree)
		return pubErr
	}

	log.Printf("job %s dispatched to worker %s", jobID, workerID)
	return nil
}

// Submit handles POST /jobs.
func Submit(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker, manager *workerman.WorkerManager) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSubmitBodyBytes)

	var body SubmitJobRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if body.TrainingScript == "" {
		http.Error(w, "training_script is required", http.StatusBadRequest)
		return
	}

	jobID := generateJobID()

	req := &workerpb.TrainingJobRequest{
		JobId:              jobID,
		TrainingScript:     []byte(body.TrainingScript),
		Requirements:       body.Requirements,
		DatasetType:        body.DatasetType,
		DatasetRef:         body.DatasetRef,
		BaseModelType:      body.BaseModelType,
		BaseModelRef:       body.BaseModelRef,
		TrainingConfigJson: body.TrainingConfigJSON,
		RequiresGpu:        body.RequiresGPU,
		MinRamGb:           body.MinRAMGB,
		MinVramGb:          body.MinVRAMGB,
		MinDiskGb:          body.MinDiskGB,
	}

	reqBytes, err := proto.Marshal(req)
	if err != nil {
		log.Printf("failed to marshal job request: %v", err)
		http.Error(w, "failed to serialize job", http.StatusInternalServerError)
		return
	}

	kv, err := jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if err != nil {
		log.Printf("failed to get jobs_state KV: %v", err)
		http.Error(w, "failed to connect to state store", http.StatusInternalServerError)
		return
	}

	// X-Submitted-By is set by the Next.js API proxy from the GitHub session.
	submittedBy := r.Header.Get("X-Submitted-By")

	// object_store jobs need their dataset uploaded via a separate request
	// after this one returns the job_id, so dispatch must wait — otherwise an
	// idle worker can pull the job and fail trying to fetch a dataset that
	// hasn't been uploaded yet. Upload() clears this and dispatches instead.
	awaitingDataset := body.DatasetType == "object_store"

	if err := jobstate.InitJobState(kv, jobID, reqBytes, submittedBy, awaitingDataset); err != nil {
		log.Printf("failed to write initial job state: %v", err)
		http.Error(w, "failed to initialize job state", http.StatusInternalServerError)
		return
	}

	if awaitingDataset {
		log.Printf("job %s awaiting dataset upload before dispatch", jobID)
	} else if err := tryDispatch(jsBroker, manager, jobID, req); err != nil {
		http.Error(w, "failed to dispatch job", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(SubmitJobResponse{
		JobID:  jobID,
		Status: "queued",
	})
}

// Logs streams stdout/stderr from a running (or completed) job as
// Server-Sent Events. Each log line arrives as "data: <line>\n\n".
// When the job finishes, a final "event: done\ndata: <state>\n\n" is sent
// and the stream closes. Clients that connect after the job started receive
// all prior log lines from the beginning (JetStream DeliverAll).
func Logs(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker, jobID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	kv, err := jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if err != nil {
		http.Error(w, "failed to get state store", http.StatusInternalServerError)
		return
	}

	// Subscribe before committing SSE headers so we can still send a clean
	// HTTP error if the subscription itself fails.
	msgCh := make(chan *nats.Msg, 64)
	sub, err := jsBroker.JS.ChanSubscribe(
		broker.SubjectLogsPrefix+jobID,
		msgCh,
		nats.DeliverAll(),
		nats.AckNone(),
	)
	if err != nil {
		log.Printf("Logs: failed to subscribe for job %s: %v", jobID, err)
		http.Error(w, "failed to subscribe to logs", http.StatusInternalServerError)
		return
	}
	defer sub.Unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case msg := <-msgCh:
			fmt.Fprintf(w, "data: %s\n\n", msg.Data)
			flusher.Flush()

		case <-ticker.C:
			status, err := jobstate.GetJobStatus(kv, jobID)
			if err != nil || status == nil {
				return
			}
			if status.State == jobstate.StateCompleted || status.State == jobstate.StateFailed || status.State == jobstate.StateCancelled {
				// Drain any log lines that arrived between the last read and now.
				for {
					select {
					case msg := <-msgCh:
						fmt.Fprintf(w, "data: %s\n\n", msg.Data)
						flusher.Flush()
					default:
						fmt.Fprintf(w, "event: done\ndata: %s\n\n", status.State)
						flusher.Flush()
						return
					}
				}
			}
		}
	}
}

// Cancel handles DELETE /jobs/{id}.
func Cancel(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker, jobID string) {
	kv, err := jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if err != nil {
		http.Error(w, "failed to get state store", http.StatusInternalServerError)
		return
	}

	status, err := jobstate.GetJobStatus(kv, jobID)
	if err != nil {
		http.Error(w, "failed to retrieve job", http.StatusInternalServerError)
		return
	}
	if status == nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	switch status.State {
	case jobstate.StateQueued, jobstate.StateRunning, jobstate.StatePendingReview:
		// cancellable states
	default:
		http.Error(w, fmt.Sprintf("job is %s, cannot cancel", status.State), http.StatusConflict)
		return
	}

	if err := jobstate.UpdateJobStatus(kv, jobID, jobstate.StateCancelled, status.WorkerID, "cancelled by user", ""); err != nil {
		http.Error(w, "failed to update job state", http.StatusInternalServerError)
		return
	}

	switch status.State {
	case jobstate.StateRunning:
		// Signal the worker's job context to cancel.
		if _, err := jsBroker.JS.Publish(broker.SubjectCancel, []byte(jobID)); err != nil {
			log.Printf("failed to publish cancel signal for job %s: %v", jobID, err)
		}
	case jobstate.StatePendingReview:
		// Unblock the worker waiting in awaitApproval by sending a decision signal.
		subject := fmt.Sprintf(broker.SubjectWorkerDecisionFmt, status.WorkerID, jobID)
		if err := jsBroker.Conn.Publish(subject, []byte("cancel")); err != nil {
			log.Printf("failed to publish cancel decision for job %s: %v", jobID, err)
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

// Decision relays an approve or reject decision from an external caller
// to the worker currently holding the job in PENDING_REVIEW state.
func Decision(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker, jobID, decision string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	kv, err := jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if err != nil {
		http.Error(w, "failed to get state store", http.StatusInternalServerError)
		return
	}

	status, err := jobstate.GetJobStatus(kv, jobID)
	if err != nil {
		http.Error(w, "failed to retrieve job", http.StatusInternalServerError)
		return
	}
	if status == nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	if status.State != jobstate.StatePendingReview {
		http.Error(w, fmt.Sprintf("job is %s, not pending review", status.State), http.StatusConflict)
		return
	}

	subject := fmt.Sprintf(broker.SubjectWorkerDecisionFmt, status.WorkerID, jobID)
	if err := jsBroker.Conn.Publish(subject, []byte(decision)); err != nil {
		log.Printf("failed to publish %s decision for job %s: %v", decision, jobID, err)
		http.Error(w, "failed to send decision", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// List handles GET /jobs. ?user=github_username filters to only that user's
// jobs; omit the param to get all jobs (admin use).
func List(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker) {
	userFilter := r.URL.Query().Get("user")

	kv, err := jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if err != nil {
		http.Error(w, "failed to connect to state store", http.StatusInternalServerError)
		return
	}

	keys, err := kv.Keys()
	if err != nil {
		if err == nats.ErrNoKeysFound {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]jobStatusPublic{})
			return
		}
		http.Error(w, "failed to list jobs", http.StatusInternalServerError)
		return
	}

	jobs := make([]jobStatusPublic, 0, len(keys))
	for _, key := range keys {
		status, err := jobstate.GetJobStatus(kv, key)
		if err != nil || status == nil {
			continue
		}
		if userFilter != "" && status.SubmittedBy != userFilter {
			continue
		}
		jobs = append(jobs, jobStatusPublic{
			JobID:         status.JobID,
			State:         status.State,
			WorkerID:      status.WorkerID,
			Error:         status.Error,
			CheckpointKey: status.CheckpointKey,
			UpdatedAt:     status.UpdatedAt,
			SubmittedBy:   status.SubmittedBy,
		})
	}

	// Sort newest-first by UpdatedAt
	for i := 0; i < len(jobs)-1; i++ {
		for j := i + 1; j < len(jobs); j++ {
			if jobs[j].UpdatedAt.After(jobs[i].UpdatedAt) {
				jobs[i], jobs[j] = jobs[j], jobs[i]
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jobs)
}

// Status handles GET /jobs/{id}.
func Status(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker, jobID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	kv, err := jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if err != nil {
		log.Printf("failed to get jobs_state KV: %v", err)
		http.Error(w, "failed to connect to state store", http.StatusInternalServerError)
		return
	}

	status, err := jobstate.GetJobStatus(kv, jobID)
	if err != nil {
		log.Printf("failed to get job status: %v", err)
		http.Error(w, "failed to retrieve job status", http.StatusInternalServerError)
		return
	}
	if status == nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// Upload streams the request body directly into NATS Object Store, then — if
// this job was left QUEUED waiting on exactly this upload (see Submit) —
// clears that gate and attempts dispatch immediately, the same way Submit
// does for jobs that didn't need one.
func Upload(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker, manager *workerman.WorkerManager, jobID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	if err := jsBroker.PushDataset(jobID, r.Body); err != nil {
		log.Printf("dataset upload failed for job %s: %v", jobID, err)
		http.Error(w, "failed to store dataset", http.StatusInternalServerError)
		return
	}
	log.Printf("dataset stored for job %s", jobID)

	kv, err := jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if err != nil {
		log.Printf("upload: failed to get jobs_state KV for job %s: %v", jobID, err)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	status, err := jobstate.GetJobStatus(kv, jobID)
	if err != nil || status == nil || !status.AwaitingDataset {
		// Not a gated job — either it doesn't exist, or dispatch already
		// happened at submit time (e.g. a re-upload). Nothing more to do.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var req workerpb.TrainingJobRequest
	if err := proto.Unmarshal(status.RequestProto, &req); err != nil {
		log.Printf("upload: failed to unmarshal request for job %s: %v", jobID, err)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := jobstate.MarkDatasetReady(kv, jobID); err != nil {
		log.Printf("upload: failed to mark job %s dataset ready: %v", jobID, err)
	}

	_ = tryDispatch(jsBroker, manager, jobID, &req)

	w.WriteHeader(http.StatusNoContent)
}

// Download streams the trained checkpoint from NATS Object Store directly to
// the HTTP response. No buffering to disk.
func Download(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker, jobID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	result, err := jsBroker.PullCheckpoint(jobID)
	if err != nil {
		log.Printf("checkpoint not found for job %s: %v", jobID, err)
		http.Error(w, "checkpoint not found", http.StatusNotFound)
		return
	}
	defer result.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\"checkpoint.tar\"")

	if _, err := io.Copy(w, result); err != nil {
		log.Printf("stream interrupted for job %s: %v", jobID, err)
	}
}
