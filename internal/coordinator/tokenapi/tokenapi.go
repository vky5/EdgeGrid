// Package tokenapi: HTTP handlers for minting/listing/revoking Tailscale
// auth keys (POST /admin/tokens/mint, GET /admin/tokens,
// POST /admin/tokens/{id}/revoke). Primary-coordinator-only in practice —
// tailscaleapi.LoadCredentials returns nil anywhere the operator hasn't
// placed OAuth client credentials in this node's data dir, which mint/List's
// activation-only view make unnecessary for a secondary coordinator/worker.
package tokenapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/edgegrid/edgegrid/internal/joinmgr"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"github.com/edgegrid/edgegrid/internal/tailscaleapi"
	"github.com/edgegrid/edgegrid/internal/tokenmgr"
)

// tokenView is the wire shape /admin/tokens returns — a TokenRecord
// enriched with join-time correlation. Never includes the raw key or its
// hash; those never need to leave the coordinator.
type tokenView struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Revoked   bool      `json:"revoked"`
	Activated bool      `json:"activated"`
	NodeID    string    `json:"node_id,omitempty"`
	NodeIP    string    `json:"node_ip,omitempty"`
	Hostname  string    `json:"hostname,omitempty"`
	Role      string    `json:"role,omitempty"`
}

// Mint calls the Tailscale API to create a new single-use auth key, records
// its hash (never the raw key) in tokenmgr, and returns the raw key to the
// caller exactly once — nothing in EdgeGrid ever has it again after this
// response.
func Mint(w http.ResponseWriter, r *http.Request, tm *tokenmgr.Manager, dataDir string) {
	ts := tailscaleapi.LoadCredentials(dataDir)
	if ts == nil {
		http.Error(w, "tailscale API credentials not configured — set them on the Settings tab "+
			"(Tailscale API Client ID / Client Secret / Tailnet)", http.StatusPreconditionFailed)
		return
	}
	minted, err := ts.CreateKey()
	if err != nil {
		http.Error(w, "mint failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	id, err := nodeident.RandomToken(16)
	if err != nil {
		http.Error(w, "failed to generate record id", http.StatusInternalServerError)
		return
	}
	sum := sha256.Sum256([]byte(minted.Key))
	rec := tokenmgr.TokenRecord{
		ID:        id,
		TSKeyID:   minted.ID,
		KeyHash:   hex.EncodeToString(sum[:]),
		CreatedAt: time.Now(),
	}
	if err := tm.Put(rec); err != nil {
		http.Error(w, "failed to store token record", http.StatusInternalServerError)
		return
	}
	log.Printf("minted tailscale auth key: record=%s ts_key_id=%s", id, minted.ID)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}{ID: id, Key: minted.Key})
}

// List returns every minted token's metadata (never the raw key or its
// hash), cross-referenced against approved join requests to show
// activation status and, once activated, which node/IP it belongs to.
func List(w http.ResponseWriter, r *http.Request, tm *tokenmgr.Manager, jm *joinmgr.Manager) {
	recs, err := tm.List()
	if err != nil {
		http.Error(w, "failed to list tokens", http.StatusInternalServerError)
		return
	}
	joins, _ := jm.List()

	byHash := make(map[string]*joinmgr.JoinRequest, len(joins))
	for _, j := range joins {
		if j.Status == joinmgr.StatusApproved && j.AuthKeyHash != "" {
			byHash[j.AuthKeyHash] = j
		}
	}

	views := make([]tokenView, 0, len(recs))
	for _, rec := range recs {
		v := tokenView{ID: rec.ID, CreatedAt: rec.CreatedAt, Revoked: rec.Revoked}
		if j, ok := byHash[rec.KeyHash]; ok {
			v.Activated = true
			v.NodeID = j.NodeID
			v.NodeIP = j.IP
			v.Hostname = j.Hostname
			v.Role = j.Role
		}
		views = append(views, v)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(views)
}

// Revoke deletes the key from Tailscale (if minting is configured on this
// coordinator) and marks the local record revoked either way, so a token
// minted before credentials were configured can still be marked dead
// locally.
func Revoke(w http.ResponseWriter, r *http.Request, id string, tm *tokenmgr.Manager, dataDir string) {
	rec, err := tm.Get(id)
	if err != nil {
		http.Error(w, "token not found", http.StatusNotFound)
		return
	}
	if ts := tailscaleapi.LoadCredentials(dataDir); ts != nil && rec.TSKeyID != "" {
		if err := ts.RevokeKey(rec.TSKeyID); err != nil {
			log.Printf("warning: tailscale revoke failed for %s: %v", rec.TSKeyID, err)
		}
	}
	if err := tm.Revoke(id); err != nil {
		http.Error(w, "failed to revoke token record", http.StatusInternalServerError)
		return
	}
	log.Printf("revoked minted token: record=%s", id)
	w.WriteHeader(http.StatusOK)
}
