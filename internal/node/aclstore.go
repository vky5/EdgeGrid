package node

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// stableIDPattern is what a StableID looks like — checked, not trusted,
// since it comes from Tailscale but is about to become a filename.
var stableIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// ACLEntry is one peer's recorded decision, in whichever ACL it came from.
type ACLEntry struct {
	StableID string
	Allow    bool
	Hostname string
	Decided  time.Time
}

// aclKind is one independently-stored ACL: a directory of per-peer files
// under DataDir, keyed by StableID, fail-closed by default.
type aclKind struct {
	dir         string
	defaultFile string
}

func (k aclKind) path(dataDir, stableID string) (string, error) {
	if !stableIDPattern.MatchString(stableID) {
		return "", fmt.Errorf("acl: %q is not a valid stable id", stableID)
	}
	return filepath.Join(dataDir, k.dir, stableID), nil
}

// lookup returns the recorded decision for a peer, or ok=false when there
// is none and the default applies.
func (k aclKind) lookup(dataDir, stableID string) (entry ACLEntry, ok bool, err error) {
	path, err := k.path(dataDir, stableID)
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
		key, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(key) {
		case "allow":
			entry.Allow = v == "true"
		case "hostname":
			entry.Hostname = v
		case "decided":
			entry.Decided, _ = time.Parse(time.RFC3339, v)
		}
	}
	// An unreadable file is not "no entry" — falling back to the default
	// there could turn a deny into an allow.
	if err := sc.Err(); err != nil {
		return ACLEntry{}, false, err
	}
	return entry, true, nil
}

// set records a decision, replacing any earlier one. Written to a temp name
// and renamed so a concurrent reader never sees half of it.
func (k aclKind) set(dataDir, stableID, hostname string, allow bool) error {
	path, err := k.path(dataDir, stableID)
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

// list returns every recorded decision, keyed by StableID.
func (k aclKind) list(dataDir string) map[string]ACLEntry {
	out := map[string]ACLEntry{}
	entries, err := os.ReadDir(filepath.Join(dataDir, k.dir))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if entry, ok, err := k.lookup(dataDir, e.Name()); err == nil && ok {
			out[e.Name()] = entry
		}
	}
	return out
}

// defaultAllows reports the policy for peers with no entry. Anything but an
// explicit "allow" is deny: a misspelt or empty file must fail closed.
func (k aclKind) defaultAllows(dataDir string) bool {
	return strings.TrimSpace(LoadToken(dataDir, k.defaultFile)) == "allow"
}
