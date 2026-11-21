package node

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const identFile = "node.id"

type Identity struct {
	NodeID string `json:"node_id"`
}

// reads this node's ID from node.id, or makes and saves one the first time
// the same ID is reused across restarts
func LoadOrCreateIdentity(dataDir string) (*Identity, error) {
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
	log.Printf("[edgegrid] new node identity: %s", id.NodeID)
	return id, nil
}

// whether this data folder has joined the tailnet before
// tailscale.ip is only written after a join works; tsnet's own state file exists even after a failed login
func HasJoined(dataDir string) bool {
	return LoadToken(dataDir, "tailscale.ip") != ""
}

// a random hex string, safe for secrets, e.g. 2 bytes gives 4 characters like 3f9a
func RandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// this node's join-poll secret, made and saved on first call
// proves to the coordinator that it is this node, not just someone who knows the ID
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

// reads a saved value from a file in the data folder, or "" if there is none
func LoadToken(dataDir, filename string) string {
	data, err := os.ReadFile(filepath.Join(dataDir, filename))
	if err != nil {
		return ""
	}
	return string(data)
}

// writes a value to a file in the data folder that only this user can read
func SaveToken(dataDir, filename, token string) error {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, filename), []byte(token), 0600)
}
