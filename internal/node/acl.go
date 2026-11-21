package node

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/edgegrid/edgegrid/internal/blob"
)

// the folder that says which peers may send this node files, one small file per peer:
//
//	<DataDir>/acl/<StableID>
//
//	# laptop, added after the office move
//	allow=true
//	hostname=alpha5-297b
//	decided=2026-09-20T01:12:00Z
//
// why one file per peer, and why key=value: docs/blob-transfer.md
// keyed by Tailscale's ID for the peer, never the ID the peer sends, or a peer could pick its own key
const aclDir = "acl"

// the rule for peers with no file: "allow" or "deny"; missing or anything else means deny
const aclDefaultFile = "acl.default"

// a file holding a byte count that replaces the default size limit, e.g. 1073741824 for 1 GiB
const maxBlobBytesFile = "max_blob_bytes"

// the biggest file a peer may send, 10 GB, a guess; max_blob_bytes overrides it
// big enough for a movie, small enough that one peer can't fill a disk
const defaultMaxBlobBytes int64 = 10 << 30

// who may send this node files; who may send tasks is kept apart in task_acl.go
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

// every saved file decision, peer ID to allowed, for the TUI, e.g. nPvV3wCNTRL: allow
// a peer with no file is missing from the map, not false
func (a *Node) TrustedPeers() map[string]bool {
	out := map[string]bool{}
	for id, e := range aclList(a.cfg.DataDir) {
		out[id] = e.Allow
	}
	return out
}

// saves whether a peer may send this node files; the hostname is only for display
func (a *Node) SetTrust(stableID, hostname string, allow bool) error {
	return aclSet(a.cfg.DataDir, stableID, hostname, allow)
}

// decides whether to take a file a peer is offering: ACL, size limit, free space
// e.g. "this node is not accepting files from you", or "file is 20000000000 bytes; this node accepts at most 10737418240"
// the error text goes to the sender, so it never shows local paths or settings
func (a *Node) checkAccept(stableID, label string, m *blob.Manifest) error {
	dataDir := a.cfg.DataDir

	allowed := aclDefaultAllows(dataDir)
	entry, found, err := aclLookup(dataDir, stableID)
	switch {
	case err != nil:
		// a file we can't read counts as deny, so a broken entry never becomes allow
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
