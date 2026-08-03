// Package peerapi: HTTP handler for peer announcement (POST /peer/announce).
package peerapi

import (
	"crypto/subtle"
	"encoding/json"
	"io"
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
		HTTPURL string `json:"http_url"` // announcer's own HTTP address, for us to repair against later; may be empty if it couldn't resolve one
		Token   string `json:"token"`    // credential we present when dialing them
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

	if err := pm.Put(peermgr.RosterEntry{
		NodeID:  body.NodeID,
		NatsURL: body.NatsURL,
		HttpURL: body.HTTPURL,
		State:   peermgr.StateActive,
	}); err != nil {
		http.Error(w, "failed to store peer record", http.StatusInternalServerError)
		return
	}

	if err := pm.PutCred(body.NodeID, peermgr.EdgeCred{
		TokenPresent: body.Token,
	}); err != nil {
		http.Error(w, "failed to store peer credential", http.StatusInternalServerError)
		return
	}
	log.Printf("peer announced: node=%s url=%s", body.NodeID, body.NatsURL)

	selfURL := ""
	if ns != nil {
		selfURL = ns.AdvertisedClientURL()
	}

	peers, err := pm.List()
	if err != nil {
		http.Error(w, "failed to list peers", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		NodeID  string                 `json:"node_id"`
		NatsURL string                 `json:"nats_url"`
		Peers   []*peermgr.RosterEntry `json:"peers"`
	}{NodeID: selfNodeID, NatsURL: selfURL, Peers: peers}) // shound send entire roster
}

// GetRoster answers a peer's repair pull. The caller sends its own digest
// ({nodeID: incarnation}) as the request body; this returns only the entries
// the caller is missing or behind on, not the whole roster
func GetRoster(
	w http.ResponseWriter,
	r *http.Request,
	pm *peermgr.Manager,
) {
	var callerDigest map[string]uint64
	if err := json.NewDecoder(r.Body).Decode(&callerDigest); err != nil && err != io.EOF {
		http.Error(w, "invalid digest", http.StatusBadRequest)
		return
	}

	peers, err := pm.List()
	if err != nil {
		http.Error(w, "failed to list peers", http.StatusInternalServerError)
		return
	}

	delta := make([]*peermgr.RosterEntry, 0)
	for _, p := range peers {
		if known, ok := callerDigest[p.NodeID]; !ok || p.Incarnation > known {
			delta = append(delta, p)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(delta)
}
