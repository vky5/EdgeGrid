// Package peermgr manages the coordinator-side registry of peer coordinators.
// Peer state is persisted in the JetStream KV bucket named "peers", keyed by NodeID
package peermgr

import (
	"encoding/json"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/nats-io/nats.go"
)


type Peer struct {
	NodeID string `json:"node_id"`
	URL    string `json:"url"`   // peer's NATS client URL, for dialing
	Token  string `json:"token"` // credential this coordinator presents to that peer
}

type Manager struct {
	kv nats.KeyValue
}

func New(b *broker.Broker) (*Manager, error) {
	kv, err := b.GetOrCreateKV("peers", 0) // no TTL — permanent
	if err != nil {
		return nil, err
	}
	return &Manager{kv: kv}, nil
}

// Put stores or replaces a peer's record.
func (m *Manager) Put(p Peer) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = m.kv.Put(p.NodeID, data)
	return err
}

// Get returns the record for one peer.
func (m *Manager) Get(nodeID string) (*Peer, error) {
	entry, err := m.kv.Get(nodeID)
	if err != nil {
		return nil, err
	}
	var p Peer
	if err := json.Unmarshal(entry.Value(), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// List returns every known peer.
func (m *Manager) List() ([]*Peer, error) {
	keys, err := m.kv.Keys()
	if err != nil {
		if err == nats.ErrNoKeysFound {
			return nil, nil
		}
		return nil, err
	}
	var peers []*Peer
	for _, key := range keys {
		p, err := m.Get(key)
		if err != nil {
			continue
		}
		peers = append(peers, p)
	}
	return peers, nil
}
