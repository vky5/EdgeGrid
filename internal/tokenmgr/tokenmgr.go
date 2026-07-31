// Package tokenmgr manages minted Tailscale auth key records, stored in the
// NATS KV bucket "minted_tokens". The raw key itself is never persisted —
// only a SHA-256 hash, just enough to recognize the same key again when a
// joining node reports it back as part of its own join request (see
// joinmgr's AuthKeyHash field). This is what lets the coordinator show
// whether a minted token has been activated, and by which node, without
// ever storing the secret at rest or depending on Tailscale's own API to
// report a key's consumption state.
package tokenmgr

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/nats-io/nats.go"
)

// TokenRecord is one minted key's metadata.
type TokenRecord struct {
	ID        string    `json:"id"`        // our own record ID
	TSKeyID   string    `json:"ts_key_id"` // Tailscale's key ID, needed to revoke it later
	KeyHash   string    `json:"key_hash"`  // sha256 hex of the raw key — never the key itself
	CreatedAt time.Time `json:"created_at"`
	Revoked   bool      `json:"revoked"`
	RevokedAt time.Time `json:"revoked_at,omitempty"`
}

type Manager struct {
	kv nats.KeyValue
}

func New(b *broker.Broker) (*Manager, error) {
	kv, err := b.GetOrCreateKV("minted_tokens", 0) // no TTL — a mint record's history persists like join_requests
	if err != nil {
		return nil, fmt.Errorf("minted_tokens KV: %w", err)
	}
	return &Manager{kv: kv}, nil
}

func (m *Manager) Put(rec TokenRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = m.kv.Put(rec.ID, data)
	return err
}

func (m *Manager) Get(id string) (*TokenRecord, error) {
	entry, err := m.kv.Get(id)
	if err != nil {
		return nil, fmt.Errorf("token %s not found: %w", id, err)
	}
	var rec TokenRecord
	if err := json.Unmarshal(entry.Value(), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (m *Manager) List() ([]*TokenRecord, error) {
	keys, err := m.kv.Keys()
	if err != nil {
		if err == nats.ErrNoKeysFound {
			return nil, nil
		}
		return nil, err
	}
	var recs []*TokenRecord
	for _, key := range keys {
		rec, err := m.Get(key)
		if err != nil {
			continue
		}
		recs = append(recs, rec)
	}
	sort.Slice(recs, func(i, j int) bool {
		return recs[i].CreatedAt.After(recs[j].CreatedAt)
	})
	return recs, nil
}

func (m *Manager) Revoke(id string) error {
	rec, err := m.Get(id)
	if err != nil {
		return err
	}
	rec.Revoked = true
	rec.RevokedAt = time.Now()
	return m.Put(*rec)
}
