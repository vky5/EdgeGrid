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

// what a Tailscale peer ID looks like, e.g. nPvV3wCNTRL
// it becomes a filename, so anything else is refused
var stableIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// one peer's saved decision, from either the file or the task ACL
//
//	// ~/.edgegrid/primary/acl/nPvV3wCNTRL holds allow=true, hostname=bravo, decided=2026-11-20T09:15:00Z
//	ACLEntry{StableID: "nPvV3wCNTRL", Allow: true, Hostname: "bravo", Decided: 2026-11-20 09:15 UTC}
//	// ~/.edgegrid/primary/acl/nP3mR8CNTRL holds allow=false, blocked with 'a' on the Peers tab
//	ACLEntry{StableID: "nP3mR8CNTRL", Allow: false, Hostname: "old-laptop", Decided: 2026-11-19 22:10 UTC}
//	// a file saying allow=yes counts as deny: only the exact text "true" allows
//	ACLEntry{StableID: "nT9wL4CNTRL", Allow: false}
type ACLEntry struct {
	StableID string
	Allow    bool
	Hostname string
	Decided  time.Time
}

// one ACL folder with one file per peer, e.g. <DataDir>/acl/<id>
// a peer counts as denied unless a file clearly says allow
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

// the saved decision for a peer, or ok=false if there is none and the default applies
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
	// a file we can't read is an error, not "no entry", so a broken entry never becomes allow
	if err := sc.Err(); err != nil {
		return ACLEntry{}, false, err
	}
	return entry, true, nil
}

// saves a decision, replacing any old one
// written to a temp file first, so a reader never sees half of it
func (k aclKind) set(dataDir, stableID, hostname string, allow bool) error {
	path, err := k.path(dataDir, stableID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	// a newline in the hostname could add a second key, e.g. allow=true
	hostname = strings.NewReplacer("\n", " ", "\r", " ").Replace(hostname)

	body := fmt.Sprintf("allow=%t\nhostname=%s\ndecided=%s\n",
		allow, hostname, time.Now().UTC().Format(time.RFC3339))

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// every saved decision, by peer ID
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

// the rule for peers with no file: only an exact "allow" allows
// so a misspelt or empty default file means deny
func (k aclKind) defaultAllows(dataDir string) bool {
	return strings.TrimSpace(LoadToken(dataDir, k.defaultFile)) == "allow"
}
