package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/joinmgr"
	"github.com/edgegrid/edgegrid/internal/nodeident"
)

// requestAndWaitForApproval submits a join request and polls until approved/rejected. Blocks.
func requestAndWaitForApproval(cfg *config.Config, ident *nodeident.Identity, role string) (*joinmgr.JoinRequest, error) { //nolint:unparam
	hostname, _ := os.Hostname()
	nonce, err := nodeident.EnsurePollNonce(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("prepare poll nonce: %w", err)
	}
	reqBody, _ := json.Marshal(map[string]string{
		"node_id":  ident.NodeID,
		"role":     role,
		"hostname": hostname,
		"nonce":    nonce,
	})

	joinURL := cfg.JoinURL
	submitURL := joinURL + "/join"
	pollURL := fmt.Sprintf("%s/join/%s", joinURL, ident.NodeID)

	// submit join request
	resp, err := http.Post(submitURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("join request to %s failed: %w", submitURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != 409 { // 409 = already pending, OK
		return nil, fmt.Errorf("join request rejected with status %d", resp.StatusCode)
	}

	fmt.Printf("\n[edgegrid] join request submitted\n")
	fmt.Printf("  node id : %s\n", ident.NodeID)
	fmt.Printf("  role    : %s\n", role)
	if cfg.DashboardURL != "" {
		fmt.Printf("\n  ➜  claim your node (link GitHub account):\n")
		fmt.Printf("     %s/claim/%s\n", strings.TrimRight(cfg.DashboardURL, "/"), ident.NodeID)
	}
	fmt.Printf("\n  waiting for admin approval...\n\n")

	// poll until approved or rejected
	for {
		time.Sleep(5 * time.Second)

		pollReq, err := http.NewRequest(http.MethodGet, pollURL, nil)
		if err != nil {
			log.Printf("building poll request: %v (retrying...)", err)
			continue
		}
		pollReq.Header.Set("X-Node-Nonce", nonce)
		r, err := http.DefaultClient.Do(pollReq)
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
			fmt.Printf("[edgegrid] join approved — connecting...\n")
			return &result, nil
		case joinmgr.StatusRejected:
			return nil, fmt.Errorf("join request rejected by admin")
		default:
			fmt.Printf("[edgegrid] still pending approval...\n")
		}
	}
}
