// Package peerapi: HTTP handler for peer announcement (POST /peer/announce).
package peerapi

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"

	"github.com/edgegrid/edgegrid/internal/coordinator/peermgr"
	"github.com/edgegrid/edgegrid/internal/joinmgr"
	"github.com/edgegrid/edgegrid/internal/natsserver"
)

// Announce records an approved peer coordinator's dial details, authenticated
// with the same poll nonce that guards GET /join/{nodeID}.
func Announce(
	w http.ResponseWriter,
	r *http.Request,
	jm *joinmgr.Manager,
	pm *peermgr.Manager,
	ns *natsserver.EmbeddedServer,
	selfNodeID string,
) {
	var body struct {
		NodeID  string `json:"node_id"`
		NatsURL string `json:"nats_url"`
		Token   string `json:"token"` // credential we present when dialing them
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.NodeID == "" || body.NatsURL == "" || body.Token == "" {
		http.Error(w, "node_id, nats_url, and token are required", http.StatusBadRequest)
		return
	}

	req, err := jm.Get(body.NodeID)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	nonce := r.Header.Get("X-Node-Nonce")
	if req.PollNonce == "" || subtle.ConstantTimeCompare([]byte(nonce), []byte(req.PollNonce)) != 1 {
		http.NotFound(w, nil) // same response as an unknown node — no existence oracle
		return
	}
	if req.Status != joinmgr.StatusApproved || req.Role != joinmgr.RoleServer {
		http.Error(w, "not an approved coordinator", http.StatusForbidden)
		return
	}

	if err := pm.Put(peermgr.Peer{
		NodeID: body.NodeID,
		URL:    body.NatsURL,
		Token:  body.Token,
	}); err != nil {
		http.Error(w, "failed to store peer record", http.StatusInternalServerError)
		return
	}
	log.Printf("peer announced: node=%s url=%s", body.NodeID, body.NatsURL)

	selfURL := ""
	if ns != nil {
		selfURL = ns.AdvertisedClientURL()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		NodeID  string `json:"node_id"`
		NatsURL string `json:"nats_url"`
	}{NodeID: selfNodeID, NatsURL: selfURL})
}
