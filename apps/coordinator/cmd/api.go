package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"

	workerpb "github.com/edgegrid/edgegrid/apps/shared/proto/worker"
	"github.com/edgegrid/edgegrid/coordinator/internal/broker"
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

func startHTTPServer(addr string, jsBroker *broker.JetStreamBroker) {
	mux := http.NewServeMux()

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
		jobReq := &workerpb.JobRequest{
			JobId:     jobID,
			ModelName: req.ModelName,
			InputText: req.InputText,
		}

		if err := jsBroker.PublishJob(req.ModelName, jobReq); err != nil {
			log.Printf("❌ Failed to publish job: %v", err)
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

	log.Printf("🚀 Starting HTTP job submission API on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil && err != http.ErrServerClosed {
		log.Fatalf("❌ HTTP server failed: %v", err)
	}
}
