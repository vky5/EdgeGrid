// Package joinapi holds the HTTP handlers for the node join/approval flow:
// a node submits a request, polls its status, an admin approves/rejects it,
// and (separately) a GitHub user claims it. See docs/access-control.md and
// docs/grid-access.md for the full flow this sits inside of.
package joinapi

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/joinmgr"
	"github.com/edgegrid/edgegrid/internal/natsserver"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"github.com/edgegrid/edgegrid/internal/usermgr"
)

// Submit accepts a join request from a worker or server node (POST /join).
func Submit(w http.ResponseWriter, r *http.Request, jm *joinmgr.Manager) {
	var body struct {
		NodeID   string `json:"node_id"`
		Role     string `json:"role"`
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.NodeID == "" || body.Role == "" {
		http.Error(w, "node_id and role are required", http.StatusBadRequest)
		return
	}

	req := joinmgr.JoinRequest{
		NodeID:      body.NodeID,
		Role:        body.Role,
		Hostname:    body.Hostname,
		Status:      joinmgr.StatusPending,
		RequestedAt: time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := jm.Submit(req); err != nil {
		http.Error(w, "failed to store join request", http.StatusInternalServerError)
		return
	}
	log.Printf("join request received: node=%s role=%s host=%s", body.NodeID, body.Role, body.Hostname)
	w.WriteHeader(http.StatusAccepted)
}

// Status returns the current status of a join request (GET /join/{nodeID}).
// Strips secrets from pending/rejected responses; includes credentials only when approved.
func Status(w http.ResponseWriter, nodeID string, jm *joinmgr.Manager) {
	req, err := jm.Get(nodeID)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	// Only include the token/secrets when approved so pending nodes can't fish for them.
	if req.Status != joinmgr.StatusApproved {
		req.Token = ""
		req.ClusterSecret = ""
		req.ClusterRoutes = nil
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(req)
}

// Approve approves a pending join request, generates a unique token, adds the
// credential to NATS, and stores it in the node_auth KV
// (POST /admin/join/{nodeID}/approve).
func Approve(w http.ResponseWriter, r *http.Request, nodeID string, jm *joinmgr.Manager, um *usermgr.Manager, ns *natsserver.EmbeddedServer, jsBroker *broker.Broker, dataDir string) {
	req, err := jm.Get(nodeID)
	if err != nil {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}
	if req.Status == joinmgr.StatusApproved {
		http.Error(w, "already approved", http.StatusConflict)
		return
	}

	// Generate a unique token for this node.
	token, err := nodeident.RandomToken(32)
	if err != nil {
		http.Error(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	// Persist the token in node_auth KV so it survives coordinator restarts.
	kv, err := jsBroker.GetOrCreateKV("node_auth", 0)
	if err == nil {
		_, _ = kv.Put(nodeID, []byte(token))
	}

	// Hot-reload NATS with the new credential so the node can connect immediately.
	var clusterSecret, coordURL string
	var clusterRoutes []string
	if ns != nil {
		if addErr := ns.AddUser(natsserver.NodeCred{Username: nodeID, Password: token}); addErr != nil {
			log.Printf("warning: NATS reload failed for node %s: %v", nodeID, addErr)
		}
		if req.Role == joinmgr.RoleServer {
			clusterSecret = nodeident.LoadToken(dataDir, "cluster.secret")
			clusterRoutes = []string{fmt.Sprintf("nats://localhost:%d", 6222)}
			coordURL = fmt.Sprintf("nats://localhost:%d", 4222)
		}
	}

	if err := jm.Approve(nodeID, token, clusterSecret, clusterRoutes, coordURL); err != nil {
		http.Error(w, "failed to approve join request", http.StatusInternalServerError)
		return
	}

	// If the node was already claimed by a GitHub user, grant that user
	// dashboard access as a side effect — this is the "contribute a worker to
	// earn grid access" default path (see docs/grid-access.md).
	if req.GitHubUsername != "" {
		if grantErr := um.Approve(req.GitHubUsername, "node:"+nodeID); grantErr != nil {
			log.Printf("warning: failed to auto-grant dashboard access for %s: %v", req.GitHubUsername, grantErr)
		}
	}

	log.Printf("approved join request: node=%s role=%s", nodeID, req.Role)
	w.WriteHeader(http.StatusOK)
}

// Claim links a GitHub username to a pending join request
// (POST /join/claim/{nodeID}). Called by the Next.js server route after the
// user authenticates with GitHub.
func Claim(w http.ResponseWriter, r *http.Request, nodeID string, jm *joinmgr.Manager, um *usermgr.Manager) {
	var body struct {
		GitHubUsername string `json:"github_username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.GitHubUsername == "" {
		http.Error(w, "github_username is required", http.StatusBadRequest)
		return
	}
	if err := jm.Claim(nodeID, body.GitHubUsername); err != nil {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}
	// Covers the out-of-order case: admin approved the node before the
	// operator got around to claiming it. Grant immediately instead of
	// waiting for a re-approval that will never come (Approve rejects
	// already-approved nodes with a 409).
	if req, err := jm.Get(nodeID); err == nil && req.Status == joinmgr.StatusApproved {
		if grantErr := um.Approve(body.GitHubUsername, "node:"+nodeID); grantErr != nil {
			log.Printf("warning: failed to auto-grant dashboard access for %s: %v", body.GitHubUsername, grantErr)
		}
	}
	log.Printf("node %s claimed by github user %s", nodeID, body.GitHubUsername)
	w.WriteHeader(http.StatusOK)
}

// Reject rejects a pending join request (POST /admin/join/{nodeID}/reject).
func Reject(w http.ResponseWriter, nodeID string, jm *joinmgr.Manager) {
	if err := jm.Reject(nodeID); err != nil {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}
	log.Printf("rejected join request: node=%s", nodeID)
	w.WriteHeader(http.StatusOK)
}

// List returns all join requests (GET /admin/join — admin view, secrets
// already stripped by joinmgr.Manager.List).
func List(w http.ResponseWriter, r *http.Request, jm *joinmgr.Manager) {
	reqs, err := jm.List()
	if err != nil {
		http.Error(w, "failed to list join requests", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reqs)
}
