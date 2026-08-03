// Package joinapi: HTTP handlers for join/approval — submit, poll, approve/reject.
package joinapi

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/joinmgr"
	"github.com/edgegrid/edgegrid/internal/natsserver"
	"github.com/edgegrid/edgegrid/internal/nodeident"
)

// Submit accepts a join request from a worker or server node (POST /join).
func Submit(w http.ResponseWriter, r *http.Request, jm *joinmgr.Manager) {
	var body struct {
		NodeID      string `json:"node_id"`
		Role        string `json:"role"`
		Hostname    string `json:"hostname"`
		Nonce       string `json:"nonce"`
		AuthKeyHash string `json:"auth_key_hash"`
		IP          string `json:"ip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.NodeID == "" || body.Role == "" || body.Nonce == "" {
		http.Error(w, "node_id, role, and nonce are required", http.StatusBadRequest)
		return
	}

	req := joinmgr.JoinRequest{
		NodeID:      body.NodeID,
		Role:        body.Role,
		Hostname:    body.Hostname,
		PollNonce:   body.Nonce,
		AuthKeyHash: body.AuthKeyHash,
		IP:          body.IP,
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

// Return status of a node.
func Status(w http.ResponseWriter, r *http.Request, nodeID string, jm *joinmgr.Manager) {
	req, err := jm.Get(nodeID)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	nonce := r.Header.Get("X-Node-Nonce")
	if req.PollNonce == "" || subtle.ConstantTimeCompare([]byte(nonce), []byte(req.PollNonce)) != 1 {
		http.NotFound(w, nil) // same response as an unknown node — no oracle for "does this ID exist"
		return
	}
	req.PollNonce = ""
	// Only include the token when approved so pending nodes can't fish for it.
	if req.Status != joinmgr.StatusApproved {
		req.Token = ""
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(req)
}

// Approve mints a token, adds it to NATS, and stores it in node_auth.
func Approve(
	w http.ResponseWriter,
	r *http.Request, nodeID string,
	jm *joinmgr.Manager,
	ns *natsserver.EmbeddedServer,
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

	// The client URL this node should dial us on. A joining coordinator also
	// keeps this as its peer record for us (see peerapi.Announce).
	coordURL := "nats://localhost:4222"
	if ns != nil {
		coordURL = ns.AdvertisedClientURL()
	}

	if err := jm.Approve(nodeID, token, coordURL); err != nil {
		http.Error(w, "failed to approve join request", http.StatusInternalServerError)
		return
	}

	log.Printf("approved join request: node=%s role=%s", nodeID, req.Role)
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
