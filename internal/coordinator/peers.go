// Every kind of peers related Calls that a coordinator make too
// other coordinator http endpoints

package coordinator

import (
	"bytes"
	"context"
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

	// if announced to primary dont again
	if paired := nodeident.LoadToken(c.dataDir, "peer.primary_paired"); paired == c.joinURL {
		log.Printf("already paired with primary %s — skipping announce", c.joinURL)
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
		"http_url": c.selfHTTPURL, // so the primary can repair with us later, not just us with it
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

		if err := c.peerMgr.Put(peermgr.RosterEntry{
			NodeID:  peer.NodeID,
			NatsURL: peer.NatsURL,
			HttpURL: c.joinURL, // the address we just reached them on — already known, no guessing needed
			State:   peermgr.StateActive,
		}); err != nil {
			log.Printf("peer announce: store peer record: %v", err)
			return
		}

		if err := c.peerMgr.PutCred(peer.NodeID, peermgr.EdgeCred{
			TokenPresent: token,
		}); err != nil {
			log.Printf("peer announce: store peer credential: %v", err)
			return
		}
		// saving this means paring to primary is complete
		if err := nodeident.SaveToken(c.dataDir, "peer.primary_paired", c.joinURL); err != nil {
			log.Printf("peer announce: record pairing marker: %v", err)
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

const repairInterval = 60 * time.Second // v1: no push, so repair *is* propagation (docs, "Propagation")

// runRepairLoop is v1's anti-entropy backstop: periodically pull a
// digest-diffed delta from every peer we have an HTTP address for, and merge
// it in. This is the mechanism that actually guarantees convergence —
// everything else (the announce reply carrying the initial roster) is a
// latency optimization on top of it. Runs on every coordinator, not just
// secondaries — mesh repair is symmetric, a primary needs to catch up on
// what its secondaries know just as much as the reverse
func (c *Coordinator) runRepairLoop(ctx context.Context) {
	if c.tsnetServer == nil || c.peerMgr == nil {
		return
	}
	ticker := time.NewTicker(repairInterval)
	defer ticker.Stop()
	client := c.tsnetServer.HTTPClient()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.repairRound(client)
		}
	}
}

// reconcile with every peer node we know about
func (c *Coordinator) repairRound(client *http.Client) {
	peers, err := c.peerMgr.List()
	if err != nil {
		log.Printf("repair round: list peers: %v", err)
		return
	}
	for _, peer := range peers {
		if peer == nil || peer.NodeID == c.nodeID || peer.HttpURL == "" {
			continue
		}
		c.repairWith(client, peer.HttpURL)
	}
}

// repairWith reconciles with one peer: send our digest, merge back whatever
// they say we're behind on.
func (c *Coordinator) repairWith(client *http.Client, peerURL string) {
	digest, err := c.peerMgr.GetDigest()
	if err != nil {
		log.Printf("repair with %s: local digest: %v", peerURL, err)
		return
	}
	delta, err := pullRoster(client, peerURL, digest)
	if err != nil {
		log.Printf("repair with %s: %v", peerURL, err)
		return
	}
	for _, entry := range delta {
		if entry == nil || entry.NodeID == c.nodeID {
			continue // never let a peer's report overwrite our own entry — only PutSelf may do that
		}
		changed, err := c.peerMgr.Merge(*entry)
		if err != nil {
			log.Printf("repair with %s: merge %s: %v", peerURL, entry.NodeID, err)
			continue
		}
		if changed {
			log.Printf("repair with %s: learned update for %s (incarnation=%d)", peerURL, entry.NodeID, entry.Incarnation)
		}
	}
}

// pullRoster sends our digest to a peer's /peer/roster and returns only the
// entries they report we're behind on.
func pullRoster(client *http.Client, peerURL string, digest map[string]uint64) ([]*peermgr.RosterEntry, error) {
	body, err := json.Marshal(digest)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, peerURL+"/peer/roster", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var delta []*peermgr.RosterEntry
	if err := json.NewDecoder(resp.Body).Decode(&delta); err != nil {
		return nil, fmt.Errorf("decode reply: %w", err)
	}
	return delta, nil
}
