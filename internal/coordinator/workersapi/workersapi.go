// Package workersapi holds the HTTP handler for listing registered workers.
package workersapi

import (
	"encoding/json"
	"net/http"

	"github.com/edgegrid/edgegrid/internal/coordinator/workerman"
)

// List handles GET /workers.
func List(w http.ResponseWriter, r *http.Request, manager *workerman.WorkerManager) {
	workers, err := manager.AllWorkers()
	if err != nil {
		http.Error(w, "failed to list workers", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(workers)
}
