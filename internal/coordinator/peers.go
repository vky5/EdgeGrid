package coordinator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/edgegrid/edgegrid/internal/coordinator/peermgr"
	"github.com/edgegrid/edgegrid/internal/nodeident"
)

const (
	announceRetryInterval = 5 * time.Second
	announceMaxAttempts   = 12
)

// announceToPrimary tells the coordinator this one joined how to dial it back.
// Runs only on a secondary (joinURL set).
func (c *Coordinator) announceToPrimary() {
	if c.joinURL == "" || c.tsnetServer == nil || c.natsServer == nil || c.peerMgr == nil {
		return
	}

	// Already paired — avoid minting a second credential on restart.
	if existing, err := c.peerMgr.List(); err == nil && len(existing) > 0 {
		log.Printf("peer pair already established (%d peer(s)) — skipping announce", len(existing))
		return
	}

	token, err := nodeident.RandomToken(32)
	if err != nil {
		log.Printf("peer announce: generate peer token: %v", err)
		return
	}
	nonce := nodeident.LoadToken(c.dataDir, "node.nonce")
	if nonce == "" {
		log.Printf("peer announce: no poll nonce on disk — cannot authenticate to %s", c.joinURL)
		return
	}

	body, err := json.Marshal(map[string]string{
		"node_id":  c.nodeID,
		"nats_url": c.natsServer.AdvertisedClientURL(),
		"token":    token,
	})
	if err != nil {
		log.Printf("peer announce: encode body: %v", err)
		return
	}

	client := c.tsnetServer.HTTPClient()
	for attempt := 1; attempt <= announceMaxAttempts; attempt++ {
		peer, err := postAnnounce(client, c.joinURL, nonce, body)
		if err != nil {
			log.Printf("peer announce to %s (attempt %d/%d): %v", c.joinURL, attempt, announceMaxAttempts, err)
			time.Sleep(announceRetryInterval)
			continue
		}

		if kv, err := c.jsBroker.GetOrCreateKV("node_auth", 0); err == nil {
			if _, err := kv.Put(peer.NodeID, []byte(token)); err != nil {
				log.Printf("peer announce: store peer credential: %v", err)
			}
		}

		if err := c.peerMgr.Put(peermgr.Peer{
			NodeID: peer.NodeID,
			URL:    peer.NatsURL,
			Token:  nodeident.LoadToken(c.dataDir, "node.token"),
		}); err != nil {
			log.Printf("peer announce: store peer record: %v", err)
			return
		}
		log.Printf("peer pair established with %s (node=%s)", peer.NatsURL, peer.NodeID)
		return
	}
	log.Printf("peer announce to %s gave up after %d attempts", c.joinURL, announceMaxAttempts)
}

type announceReply struct {
	NodeID  string `json:"node_id"`
	NatsURL string `json:"nats_url"`
}

func postAnnounce(client *http.Client, joinURL, nonce string, body []byte) (*announceReply, error) {
	req, err := http.NewRequest(http.MethodPost, joinURL+"/peer/announce", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Nonce", nonce)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var reply announceReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, fmt.Errorf("decode reply: %w", err)
	}
	if reply.NodeID == "" || reply.NatsURL == "" {
		return nil, fmt.Errorf("incomplete reply")
	}
	return &reply, nil
}
