package coordinator

import (
	"io"
	"log"
	"net/http"

	"github.com/edgegrid/edgegrid/internal/broker"
)

// handleUpload streams the request body directly into NATS Object Store.
// No buffering to disk — the coordinator is a pure pipe between HTTP and NATS.
func handleUpload(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker, jobID string) {
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
	w.WriteHeader(http.StatusNoContent)
}

// handleArtifactDownload streams the trained checkpoint from NATS Object Store
// directly to the HTTP response. No buffering to disk.
func handleArtifactDownload(w http.ResponseWriter, r *http.Request, jsBroker *broker.Broker, jobID string) {
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
