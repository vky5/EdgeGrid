package node

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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

// stableIDPattern is what a StableID looks like. It comes from Tailscale
// rather than the peer, but it is still a string about to become a filename,
// so it is checked rather than trusted.
var stableIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// ACLEntry is one peer's recorded decision.
type ACLEntry struct {
	StableID string
	Allow    bool
	Hostname string
	Decided  time.Time
}

func aclPath(dataDir, stableID string) (string, error) {
	if !stableIDPattern.MatchString(stableID) {
		return "", fmt.Errorf("acl: %q is not a valid stable id", stableID)
	}
	return filepath.Join(dataDir, aclDir, stableID), nil
}

// aclLookup returns the recorded decision for a peer, or ok=false when there
// is none and the default applies.
func aclLookup(dataDir, stableID string) (entry ACLEntry, ok bool, err error) {
	path, err := aclPath(dataDir, stableID)
	if err != nil {
		return ACLEntry{}, false, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return ACLEntry{}, false, nil
	}
	if err != nil {
		return ACLEntry{}, false, err
	}
	defer f.Close()

	entry = ACLEntry{StableID: stableID}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "allow":
			entry.Allow = v == "true"
		case "hostname":
			entry.Hostname = v
		case "decided":
			entry.Decided, _ = time.Parse(time.RFC3339, v)
		}
	}
	// A file that exists but can't be read is not "no entry" — falling back
	// to the default there could turn a deny into an allow.
	if err := sc.Err(); err != nil {
		return ACLEntry{}, false, err
	}
	return entry, true, nil
}

// aclSet records a decision, replacing any earlier one. The file is written
// to a temp name and renamed so a concurrent reader never sees half of it.
func aclSet(dataDir, stableID, hostname string, allow bool) error {
	path, err := aclPath(dataDir, stableID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	// A hostname is peer-influenced text going into a line-oriented file:
	// keep a newline in it from injecting a second key.
	hostname = strings.NewReplacer("\n", " ", "\r", " ").Replace(hostname)

	body := fmt.Sprintf("allow=%t\nhostname=%s\ndecided=%s\n",
		allow, hostname, time.Now().UTC().Format(time.RFC3339))

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// aclList returns every recorded decision, keyed by StableID.
func aclList(dataDir string) map[string]ACLEntry {
	out := map[string]ACLEntry{}
	entries, err := os.ReadDir(filepath.Join(dataDir, aclDir))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if entry, ok, err := aclLookup(dataDir, e.Name()); err == nil && ok {
			out[e.Name()] = entry
		}
	}
	return out
}

// aclDefaultAllows reports the policy for peers with no entry. Anything but
// an explicit "allow" is deny: a misspelt or empty file must fail closed.
func aclDefaultAllows(dataDir string) bool {
	return strings.TrimSpace(LoadToken(dataDir, aclDefaultFile)) == "allow"
}

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
	return nil
}
