// Package joinapi: HTTP handlers for join/approval — submit, poll, approve/reject, claim.
// See docs/access-control.md and docs/grid-access.md for the full flow.
package joinapi

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/joinmgr"
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

// Return status of a node
// TODO currently there is no check, any node can query the status of any other node. 
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

// Approve mints a token, adds it to NATS, and stores it in node_auth.
// TODO only admin check and if it leaks then anyone can autoapprove a node
func Approve(
	w http.ResponseWriter, 
	r *http.Request, nodeID string, 
	jm *joinmgr.Manager,
	um *usermgr.Manager,
	jsBroker *broker.Broker,
	dataDir string,
) {
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

	// NATS credential propagation happens via each coordinator's own node_auth
	// watcher (coordinator.go: watchApprovedNodes), reacting to the kv.Put
	// above — not a direct AddUser call here — so it applies uniformly across
	// every embedding coordinator in the cluster, not just this one ofc including this one as well (if running embedded server)
	// coordURL: the client address, needed by any approved node (worker or server) to connect.
	coordURL := fmt.Sprintf("nats://localhost:%d", 4222) // ? this is for client connections and uses the Pub/Sub protocol

	// clusterSecret/Routes: the route-peer address, only needed by a non-primary coordinator.
	var clusterSecret string
	var clusterRoutes []string
	if req.Role == joinmgr.RoleServer {
		clusterSecret = nodeident.LoadToken(dataDir, "cluster.secret")
		clusterRoutes = []string{fmt.Sprintf("nats://localhost:%d", 6222)} // ? this is for server to server communication and uses some other kinda protocol
	}

	if err := jm.Approve(nodeID, token, clusterSecret, coordURL, clusterRoutes); err != nil {
		http.Error(w, "failed to approve join request", http.StatusInternalServerError)
		return
	}

	// already claimed? auto-grant dashboard access (docs/grid-access.md).
	if req.GitHubUsername != "" {
		if grantErr := um.Approve(req.GitHubUsername, "node:"+nodeID); grantErr != nil {
			log.Printf("warning: failed to auto-grant dashboard access for %s: %v", req.GitHubUsername, grantErr)
		}
	}

	log.Printf("approved join request: node=%s role=%s", nodeID, req.Role)
	w.WriteHeader(http.StatusOK)
}

// Claim links a GitHub username to a join request (POST /join/claim/{nodeID}).
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
	// out-of-order case: already approved before claim — grant now, no re-approval will come.
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

// List returns all join requests, secrets stripped (GET /admin/join).
func List(w http.ResponseWriter, r *http.Request, jm *joinmgr.Manager) {
	reqs, err := jm.List()
	if err != nil {
		http.Error(w, "failed to list join requests", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reqs)
}
