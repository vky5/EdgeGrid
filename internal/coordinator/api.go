package coordinator

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/coordinator/workerman"
	"github.com/edgegrid/edgegrid/internal/jobstate"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

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

func generateJobID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// corsMiddleware adds permissive CORS headers so the local dashboard
// (Next.js on :3000) can call the coordinator (:8080) during development.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func StartHTTPServer(addr string, jsBroker *broker.Broker, manager *workerman.WorkerManager) {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// GET /workers — list all registered workers and their current state
	mux.HandleFunc("/workers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		workers, err := manager.AllWorkers()
		if err != nil {
			http.Error(w, "failed to list workers", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(workers)
	})

	// GET /jobs  — list all jobs (strips request_proto from response, too large)
	// POST /jobs — submit a training job
	mux.HandleFunc("/jobs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleListJobs(w, jsBroker)
		case http.MethodPost:
			handleSubmitJob(w, r, jsBroker, manager)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// /jobs/{id}           → GET job status
	// /jobs/{id}/upload    → POST dataset upload
	// /jobs/{id}/artifact  → GET checkpoint download
	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/jobs/")
		parts := strings.SplitN(path, "/", 2)

		jobID := parts[0]
		if jobID == "" {
			http.Error(w, "job_id is required", http.StatusBadRequest)
			return
		}

		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				handleGetJobStatus(w, r, jsBroker, jobID)
			case http.MethodDelete:
				handleCancelJob(w, r, jsBroker, jobID)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}

		switch parts[1] {
		case "upload":
			handleUpload(w, r, jsBroker, jobID)
		case "artifact":
			handleArtifactDownload(w, r, jsBroker, jobID)
		case "logs":
			handleJobLogs(w, r, jsBroker, jobID)
		case "approve":
			handleJobDecision(w, r, jsBroker, jobID, "approve")
		case "reject":
			handleJobDecision(w, r, jsBroker, jobID, "reject")
		default:
			http.NotFound(w, r)
		}
	})

	log.Printf("starting HTTP job API on %s", addr)
	if err := http.ListenAndServe(addr, corsMiddleware(mux)); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server failed: %v", err)
	}
}

func handleSubmitJob(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker, manager *workerman.WorkerManager) {
	var body SubmitJobRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if body.DatasetRef == "" {
		http.Error(w, "dataset_ref is required", http.StatusBadRequest)
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

	if err := jobstate.InitJobState(kv, jobID, reqBytes); err != nil {
		log.Printf("failed to write initial job state: %v", err)
		http.Error(w, "failed to initialize job state", http.StatusInternalServerError)
		return
	}

	workerID, err := manager.FindAndAssignWorker(jobID, req)
	if err != nil {
		// No free worker right now — job stays QUEUED and will be dispatched
		// when a capable worker becomes available.
		log.Printf("no free worker for job %s, leaving queued: %v", jobID, err)
	} else {
		subject := broker.SubjectTrainPrefix + workerID
		if pubErr := jsBroker.PublishProto(subject, req); pubErr != nil {
			log.Printf("failed to dispatch job %s to worker %s: %v", jobID, workerID, pubErr)
			http.Error(w, "failed to dispatch job", http.StatusInternalServerError)
			return
		}
		log.Printf("job %s dispatched to worker %s", jobID, workerID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(SubmitJobResponse{
		JobID:  jobID,
		Status: "queued",
	})
}

// handleJobLogs streams stdout/stderr from a running (or completed) job as
// Server-Sent Events. Each log line arrives as "data: <line>\n\n".
// When the job finishes, a final "event: done\ndata: <state>\n\n" is sent
// and the stream closes. Clients that connect after the job started receive
// all prior log lines from the beginning (JetStream DeliverAll).
func handleJobLogs(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker, jobID string) {
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
		log.Printf("handleJobLogs: failed to subscribe for job %s: %v", jobID, err)
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

func handleCancelJob(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker, jobID string) {
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

// handleJobDecision relays an approve or reject decision from an external caller
// to the worker currently holding the job in PENDING_REVIEW state.
func handleJobDecision(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker, jobID, decision string) {
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

// jobStatusPublic is JobStatus with request_proto stripped — it's large binary
// that the dashboard doesn't need and bloats every list response.
type jobStatusPublic struct {
	JobID         string          `json:"job_id"`
	State         jobstate.State  `json:"state"`
	WorkerID      string          `json:"worker_id,omitempty"`
	Error         string          `json:"error,omitempty"`
	CheckpointKey string          `json:"checkpoint_key,omitempty"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func handleListJobs(w http.ResponseWriter, jsBroker *broker.Broker) {
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
		jobs = append(jobs, jobStatusPublic{
			JobID:         status.JobID,
			State:         status.State,
			WorkerID:      status.WorkerID,
			Error:         status.Error,
			CheckpointKey: status.CheckpointKey,
			UpdatedAt:     status.UpdatedAt,
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

func handleGetJobStatus(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker, jobID string) {
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
