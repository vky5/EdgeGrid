// Package nodeident generates and persists a stable node identity (UUID-style
// hex ID and an optional secret token). Both are stored in the node's data
// directory and reused across restarts.
package nodeident

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const identFile = "node.id"

type Identity struct {
	NodeID string `json:"node_id"`
}

// LoadOrCreate reads data/node.id, or generates and persists node ID (same for coordinator + worker)
func LoadOrCreate(dataDir string) (*Identity, error) {
	if raw := LoadToken(dataDir, identFile); raw != "" {
		var id Identity
		if json.Unmarshal([]byte(raw), &id) == nil && id.NodeID != "" {
			return &id, nil
		}
	}

	nodeID, err := RandomToken(16)
	if err != nil {
		return nil, fmt.Errorf("generate node ID: %w", err)
	}
	id := &Identity{NodeID: nodeID}

	data, _ := json.Marshal(id)
	if err := SaveToken(dataDir, identFile, string(data)); err != nil {
		return nil, fmt.Errorf("save node identity: %w", err)
	}
	fmt.Printf("[edgegrid] new node identity: %s\n", id.NodeID)
	return id, nil
}

// RandomToken returns a cryptographically random hex string (n bytes → 2n hex chars).
func RandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// EnsurePollNonce returns this node's join-poll nonce, generating and
// persisting one on first call — proof to the coordinator of node identity, not just node ID knowledge.
func EnsurePollNonce(dataDir string) (string, error) {
	if n := LoadToken(dataDir, "node.nonce"); n != "" {
		return n, nil
	}
	nonce, err := RandomToken(32)
	if err != nil {
		return "", fmt.Errorf("generate poll nonce: %w", err)
	}
	if err := SaveToken(dataDir, "node.nonce", nonce); err != nil {
		return "", fmt.Errorf("save poll nonce: %w", err)
	}
	return nonce, nil
}

// LoadToken reads a saved token from dataDir/filename, or returns "".
func LoadToken(dataDir, filename string) string {
	data, err := os.ReadFile(filepath.Join(dataDir, filename))
	if err != nil {
		return ""
	}
	return string(data)
}

// SaveToken writes a token to dataDir/filename with 0600 permissions.
func SaveToken(dataDir, filename, token string) error {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, filename), []byte(token), 0600)
}
