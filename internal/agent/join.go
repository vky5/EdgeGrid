package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/joinmgr"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"tailscale.com/tsnet"
)

// requestAndWaitForApproval submits a join request and polls until approved/rejected. Blocks.
func requestAndWaitForApproval(ts *tsnet.Server, cfg *config.Config, ident *nodeident.Identity, role string) (*joinmgr.JoinRequest, error) { //nolint:unparam
	client := ts.HTTPClient()
	hostname, _ := os.Hostname()
	nonce, err := nodeident.EnsurePollNonce(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("prepare poll nonce: %w", err)
	}
	// Reported so the coordinator's tokenapi can recognize a join that used
	// a minted key without either side ever sending the raw key again.
	var authKeyHash string
	if cfg.TailscaleAuthKey != "" {
		sum := sha256.Sum256([]byte(cfg.TailscaleAuthKey))
		authKeyHash = hex.EncodeToString(sum[:])
	}
	ip4, _ := ts.TailscaleIPs()
	reqBody, _ := json.Marshal(map[string]string{
		"node_id":       ident.NodeID,
		"role":          role,
		"hostname":      hostname,
		"nonce":         nonce,
		"auth_key_hash": authKeyHash,
		"ip":            ip4.String(),
	})

	joinURL := cfg.JoinURL
	submitURL := joinURL + "/join"
	pollURL := fmt.Sprintf("%s/join/%s", joinURL, ident.NodeID)

	// submit join request
	resp, err := client.Post(submitURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("join request to %s failed: %w", submitURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != 409 { // 409 = already pending, OK
		return nil, fmt.Errorf("join request rejected with status %d", resp.StatusCode)
	}

	log.Printf("[edgegrid] join request submitted (node id: %s, role: %s)", ident.NodeID, role)
	log.Printf("[edgegrid] waiting for admin approval...")

	// poll until approved or rejected
	for {
		time.Sleep(5 * time.Second)

		pollReq, err := http.NewRequest(http.MethodGet, pollURL, nil)
		if err != nil {
			log.Printf("building poll request: %v (retrying...)", err)
			continue
		}
		pollReq.Header.Set("X-Node-Nonce", nonce)
		r, err := client.Do(pollReq)
		if err != nil {
			log.Printf("polling join status: %v (retrying...)", err)
			continue
		}

		var result joinmgr.JoinRequest
		if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
			r.Body.Close()
			continue
		}
		r.Body.Close()

		switch result.Status {
		case joinmgr.StatusApproved:
			log.Printf("[edgegrid] join approved — connecting...")
			return &result, nil
		case joinmgr.StatusRejected:
			return nil, fmt.Errorf("join request rejected by admin")
		default:
			log.Printf("[edgegrid] still pending approval...")
		}
	}
}
