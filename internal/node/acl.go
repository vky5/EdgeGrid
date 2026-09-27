package node

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/edgegrid/edgegrid/internal/blob"
)

// The ACL decides which peers may send this node a blob. It lives in the
// profile, one small file per peer:
//
//	<DataDir>/acl/<StableID>
//
//	# laptop, added after the office move
//	allow=true
//	hostname=alpha5-297b
//	decided=2026-09-20T01:12:00Z
//
// One file per peer, not one file holding a list, so adding or removing a
// peer is a single create or delete with nothing to parse and rewrite — the
// read-modify-write shape that already produced a real race on the global
// app.json. Plain key=value with # comments rather than JSON or YAML: it
// allows a note on why a peer is trusted, and needs no third-party parser.
//
// Entries are keyed by Tailscale's StableID, which the control plane
// attributes via WhoIs. Never by Hello.NodeID: that is the peer's own
// claim, and an allowlist keyed on a value the peer chooses is no allowlist.
const aclDir = "acl"

// aclDefaultFile holds the policy for peers with no entry: "allow" or
// "deny". Absent or unrecognised means deny.
const aclDefaultFile = "acl.default"

// maxBlobBytesFile optionally overrides defaultMaxBlobBytes.
const maxBlobBytesFile = "max_blob_bytes"

// defaultMaxBlobBytes caps how large a blob a peer may push. It is a guess,
// not derived from anything — big enough for a movie, small enough that one
// peer can't fill a disk with a single declared size. Override per profile
// by writing a byte count to max_blob_bytes.
const defaultMaxBlobBytes int64 = 10 << 30

// fileACL is the file-transfer ACL's storage. See aclstore.go for the
// shared mechanics, and task_acl.go for the separate task ACL.
var fileACL = aclKind{dir: aclDir, defaultFile: aclDefaultFile}

func aclLookup(dataDir, stableID string) (ACLEntry, bool, error) {
	return fileACL.lookup(dataDir, stableID)
}

func aclSet(dataDir, stableID, hostname string, allow bool) error {
	return fileACL.set(dataDir, stableID, hostname, allow)
}

func aclList(dataDir string) map[string]ACLEntry { return fileACL.list(dataDir) }

func aclDefaultAllows(dataDir string) bool { return fileACL.defaultAllows(dataDir) }

func maxBlobBytes(dataDir string) int64 {
	var n int64
	if _, err := fmt.Sscanf(strings.TrimSpace(LoadToken(dataDir, maxBlobBytesFile)), "%d", &n); err == nil && n > 0 {
		return n
	}
	return defaultMaxBlobBytes
}

// TrustedPeers reports every recorded decision as StableID -> allowed, for
// the TUI. Peers with no entry are absent, not false.
func (a *Node) TrustedPeers() map[string]bool {
	out := map[string]bool{}
	for id, e := range aclList(a.cfg.DataDir) {
		out[id] = e.Allow
	}
	return out
}

// SetTrust records whether stableID may send this node blobs. hostname is
// only for display — the entry is keyed on stableID.
func (a *Node) SetTrust(stableID, hostname string, allow bool) error {
	return aclSet(a.cfg.DataDir, stableID, hostname, allow)
}

// checkAccept applies this node's inbound policy to a manifest a peer is
// offering. The error text goes back to the sender, so it says what was
// decided without leaking local paths or config.
func (a *Node) checkAccept(stableID, label string, m *blob.Manifest) error {
	dataDir := a.cfg.DataDir

	allowed := aclDefaultAllows(dataDir)
	entry, found, err := aclLookup(dataDir, stableID)
	switch {
	case err != nil:
		// Fail closed: an unreadable entry is not "no entry", and falling
		// back to the default there could turn a deny into an allow.
		log.Printf("blob: refusing %s: acl lookup for %q: %v", label, stableID, err)
		allowed = false
	case found:
		allowed = entry.Allow
	}
	if !allowed {
		log.Printf("blob: refused %s (%s) — allow them with 'a' on the Peers tab, or create %s",
			label, stableID, filepath.Join(dataDir, aclDir, stableID))
		return errors.New("this node is not accepting files from you")
	}

	if limit := maxBlobBytes(dataDir); m.Size > limit {
		log.Printf("blob: refused %s: %d bytes exceeds the %d-byte limit", label, m.Size, limit)
		return fmt.Errorf("file is %d bytes; this node accepts at most %d", m.Size, limit)
	}
	return a.checkSpace(label, m.Size)
}
