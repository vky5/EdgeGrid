// Package usersapi holds the HTTP handlers for the grid-access allowlist —
// who's allowed to submit jobs, separate from node approval. See
// docs/grid-access.md for the full design.
package usersapi

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/edgegrid/edgegrid/internal/usermgr"
)

// Status reports whether a GitHub username has been granted dashboard access
// (job submission), and if so, how (GET /users/{username}/status).
func Status(w http.ResponseWriter, username string, um *usermgr.Manager) {
	resp := struct {
		GitHubUsername string `json:"github_username"`
		Approved       bool   `json:"approved"`
		ApprovedVia    string `json:"approved_via,omitempty"`
		ApprovedAt     string `json:"approved_at,omitempty"`
	}{GitHubUsername: username}

	if u, ok := um.IsApproved(username); ok {
		resp.Approved = true
		resp.ApprovedVia = u.ApprovedVia
		resp.ApprovedAt = u.ApprovedAt.Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// List returns everyone who currently has dashboard/grid access (GET /admin/users).
func List(w http.ResponseWriter, r *http.Request, um *usermgr.Manager) {
	users, err := um.List()
	if err != nil {
		http.Error(w, "failed to list approved users", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(users)
}

// Approve grants a GitHub username dashboard access directly, with no node
// required (POST /admin/users/{username}/approve — "direct admin grant" in
// docs/grid-access.md).
func Approve(w http.ResponseWriter, username string, um *usermgr.Manager) {
	if err := um.Approve(username, "admin"); err != nil {
		http.Error(w, "failed to approve user", http.StatusInternalServerError)
		return
	}
	log.Printf("admin granted dashboard access to %s", username)
	w.WriteHeader(http.StatusOK)
}
