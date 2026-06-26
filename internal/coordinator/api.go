package coordinator

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/jobstate"
)

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
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// POST /jobs — submit a training job
	mux.HandleFunc("/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleSubmitJob(w, r, jsBroker)
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

		// No sub-action: status check
		if len(parts) == 1 {
			handleGetJobStatus(w, r, jsBroker, jobID)
			return
		}

		switch parts[1] {
		case "upload":
			handleUpload(w, r, jsBroker, jobID)
		case "artifact":
			handleArtifactDownload(w, r, jsBroker, jobID)
		default:
			http.NotFound(w, r)
		}
	})

	log.Printf("starting HTTP job API on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server failed: %v", err)
	}
}

func handleSubmitJob(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker) {
	// TODO: decode TrainingJobRequest, validate, publish to jobs.train.<model>
	// This handler will be fully implemented when the training executor is wired up.
	jobID := generateJobID()

	kv, err := jsBroker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if err != nil {
		log.Printf("failed to get jobs_state KV: %v", err)
		http.Error(w, "failed to connect to state store", http.StatusInternalServerError)
		return
	}

	if err := jobstate.UpdateJobStatus(kv, jobID, jobstate.StateQueued, "", "", ""); err != nil {
		log.Printf("failed to write initial job state: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(SubmitJobResponse{
		JobID:  jobID,
		Status: "queued",
	})
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
