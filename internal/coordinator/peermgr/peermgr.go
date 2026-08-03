// Package peermgr manages the coordinator-side registry of peer coordinators.

package peermgr

import (
	"encoding/json"
	"fmt"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/nats-io/nats.go"
)

const (
	StateActive  = "active"
	StateRemoved = "removed" // tombstone — wins regardless of incarnation
)

// RosterEntry is one coordinator's public view of a peer
type RosterEntry struct {
	NodeID      string `json:"node_id"`
	NatsURL     string `json:"url"`         // peer's NATS client URL, for dialing
	HttpURL     string `json:"http_url"`    // peer's HTTP URL, for mesh repair
	Incarnation uint64 `json:"incarnation"` // counter; higher wins on merge
	State       string `json:"state"`       // StateActive or StateRemoved
	Cert        []byte `json:"cert"`        // signed membership cert proving this peer was vouched for
}

// Credeential pair for one peer (local)
type EdgeCred struct {
	TokenPresent string `json:"token_present"` // credential this coordinator presents to that peer
	TokenAccept  string `json:"token_accept"`  // credential that peer presents to this coordinator
}

type Manager struct {
	roster nats.KeyValue
	creds  nats.KeyValue
}

func New(b *broker.Broker) (*Manager, error) {
	roster, err := b.GetOrCreateKV("roster", 0) // no TTL — permanent
	if err != nil {
		return nil, err
	}
	creds, err := b.GetOrCreateKV("peer_creds", 0) // no TTL — permanent, local-only
	if err != nil {
		return nil, err
	}
	return &Manager{roster: roster, creds: creds}, nil
}

// Put stores or replaces a peer's roster entry.
func (m *Manager) Put(p RosterEntry) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = m.roster.Put(p.NodeID, data)
	return err
}

// Get returns the roster entry for one peer.
func (m *Manager) Get(nodeID string) (*RosterEntry, error) {
	entry, err := m.roster.Get(nodeID)
	if err != nil {
		return nil, err
	}
	var p RosterEntry
	if err := json.Unmarshal(entry.Value(), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// List returns every known peer's roster entry.
func (m *Manager) List() ([]*RosterEntry, error) {
	keys, err := m.roster.Keys()
	if err != nil {
		if err == nats.ErrNoKeysFound {
			return nil, nil
		}
		return nil, err
	}
	var peers []*RosterEntry
	for _, key := range keys {
		p, err := m.Get(key)
		if err != nil {
			continue
		}
		peers = append(peers, p)
	}
	return peers, nil
}

// Merge applies a peer-reported roster entry using the single-writer rule:
// an entry is only ever authoritative from its own origin, and the highest
// Incarnation wins.
func (m *Manager) Merge(incoming RosterEntry) (bool, error) {
	existing, err := m.Get(incoming.NodeID)
	if err != nil {
		if err != nats.ErrKeyNotFound {
			return false, err
		}
		if err := m.Put(incoming); err != nil {
			return false, err
		}
		return true, nil
	}

	if existing.State == StateRemoved {
		return false, nil
	}
	if incoming.State != StateRemoved && incoming.Incarnation <= existing.Incarnation {
		return false, nil
	}
	if err := m.Put(incoming); err != nil {
		return false, err
	}
	return true, nil
}

// PutSelf writes selfID's own roster entry, bumping Incarnation past
// whatever is currently stored so the change wins the merge against any copy
// a peer may already hold of the old entry.
// Refuses to write any NodeID other than selfID — this is the only gate
// that makes Incarnation mean anything: without it, any caller could mint a
// new incarnation for a peer it does not own, and Merge (which trusts
// whichever value is higher) would have no way to tell that write apart
// from a legitimate one made by the peer itself.
func (m *Manager) PutSelf(selfID string, entry RosterEntry) error {
	if entry.NodeID != selfID {
		return fmt.Errorf("peermgr: PutSelf(%q) may not write entry for %q", selfID, entry.NodeID)
	}
	existing, err := m.Get(entry.NodeID)
	if err != nil && err != nats.ErrKeyNotFound {
		return err
	}
	if existing != nil {
		entry.Incarnation = existing.Incarnation + 1
	} else {
		entry.Incarnation = 1
	}
	return m.Put(entry)
}

// PutCred stores or replaces the local credential pair for one peer.
func (m *Manager) PutCred(nodeID string, c EdgeCred) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = m.creds.Put(nodeID, data)
	return err
}

// GetCred returns the local credential pair for one peer.
func (m *Manager) GetCred(nodeID string) (*EdgeCred, error) {
	entry, err := m.creds.Get(nodeID)
	if err != nil {
		return nil, err
	}
	var c EdgeCred
	if err := json.Unmarshal(entry.Value(), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// GetDigest returns this coordinator's version map — {nodeID: incarnation}
// for every peer it knows about — sent to a peer during repair so it can
// compute a delta against its own state.
func (m *Manager) GetDigest() (map[string]uint64, error) {
	digest := make(map[string]uint64)
	peers, err := m.List()
	if err != nil {
		return digest, err
	}
	for _, peer := range peers {
		digest[peer.NodeID] = peer.Incarnation
	}
	return digest, nil
}
