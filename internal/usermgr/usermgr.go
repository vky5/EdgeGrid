// Package usermgr manages the "approved_users" allowlist (dashboard/job-submission
// access) — separate from joinmgr, which only gates NATS connectivity.
// Granted automatically (claimed node approved) or directly by an admin.
package usermgr

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/nats-io/nats.go"
)

// ApprovedUser is stored in KV keyed by GitHub username.
type ApprovedUser struct {
	GitHubUsername string    `json:"github_username"`
	ApprovedAt     time.Time `json:"approved_at"`
	ApprovedVia    string    `json:"approved_via"` // "node:<nodeID>" or "admin"
}

type Manager struct {
	kv nats.KeyValue
}

func New(b *broker.Broker) (*Manager, error) {
	kv, err := b.GetOrCreateKV("approved_users", 0)
	if err != nil {
		return nil, fmt.Errorf("approved_users KV: %w", err)
	}
	return &Manager{kv: kv}, nil
}

// Approve grants dashboard access. No-op if already approved — keeps the original grant reason.
func (m *Manager) Approve(username, via string) error {
	if username == "" {
		return fmt.Errorf("username required")
	}
	if _, err := m.kv.Get(username); err == nil {
		return nil
	}
	data, err := json.Marshal(ApprovedUser{
		GitHubUsername: username,
		ApprovedAt:     time.Now(),
		ApprovedVia:    via,
	})
	if err != nil {
		return err
	}
	_, err = m.kv.Put(username, data)
	return err
}

// IsApproved reports whether username has dashboard access, and the record if so.
func (m *Manager) IsApproved(username string) (*ApprovedUser, bool) {
	entry, err := m.kv.Get(username)
	if err != nil {
		return nil, false
	}
	var u ApprovedUser
	if err := json.Unmarshal(entry.Value(), &u); err != nil {
		return nil, false
	}
	return &u, true
}

// List returns every approved user (admin view).
func (m *Manager) List() ([]*ApprovedUser, error) {
	keys, err := m.kv.Keys()
	if err != nil {
		if err == nats.ErrNoKeysFound {
			return nil, nil
		}
		return nil, err
	}
	users := make([]*ApprovedUser, 0, len(keys))
	for _, key := range keys {
		entry, err := m.kv.Get(key)
		if err != nil {
			continue
		}
		var u ApprovedUser
		if err := json.Unmarshal(entry.Value(), &u); err != nil {
			continue
		}
		users = append(users, &u)
	}
	return users, nil
}

// Revoke removes a user's dashboard access.
func (m *Manager) Revoke(username string) error {
	return m.kv.Delete(username)
}
