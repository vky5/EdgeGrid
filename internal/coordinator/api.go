package coordinator

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/jobstate"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
)

type SubmitJobRequest struct {
	ModelName string `json:"model_name"`
	InputText string `json:"input_text"`
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

func StartHTTPServer(addr string, jsBroker *broker.Broker) {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req SubmitJobRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON body", http.StatusBadRequest)
			return
		}

		if req.ModelName == "" || req.InputText == "" {
			http.Error(w, "model_name and input_text are required", http.StatusBadRequest)
			return
		}

		jobID := generateJobID()

		// Write initial QUEUED status to NATS KV
		kv, kvErr := jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
		if kvErr == nil {
			_ = jobstate.UpdateJobStatus(kv, jobID, jobstate.StateQueued, "", "", nil)
		} else {
			log.Printf("warning: failed to get jobs_state KV: %v", kvErr)
		}

		jobReq := &workerpb.JobRequest{
			JobId:     jobID,
			ModelName: req.ModelName,
			InputText: req.InputText,
		}

		subject := broker.SubjectJobsPrefix + req.ModelName
		if err := jsBroker.PublishProto(subject, jobReq); err != nil {
			log.Printf("failed to publish job: %v", err)
			if kvErr == nil {
				_ = jobstate.UpdateJobStatus(kv, jobID, jobstate.StateFailed, "", "failed to queue job in stream", nil)
			}
			http.Error(w, "Failed to publish job to stream", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(SubmitJobResponse{
			JobID:  jobID,
			Status: "queued",
		})
	})

	// GET /jobs/<job_id>
	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		jobID := r.URL.Path[len("/jobs/"):]
		if jobID == "" {
			http.Error(w, "job_id is required", http.StatusBadRequest)
			return
		}

		kv, kvErr := jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
		if kvErr != nil {
			log.Printf("failed to get jobs_state KV: %v", kvErr)
			http.Error(w, "Failed to connect to state store", http.StatusInternalServerError)
			return
		}

		status, err := jobstate.GetJobStatus(kv, jobID)
		if err != nil {
			log.Printf("failed to get job status: %v", err)
			http.Error(w, "Failed to retrieve job status", http.StatusInternalServerError)
			return
		}

		if status == nil {
			http.Error(w, "Job not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(status)
	})

	log.Printf("starting HTTP job API on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server failed: %v", err)
	}
}
