package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/edgegrid/edgegrid/internal/blob"
)

func TestACLRoundTrip(t *testing.T) {
	dir := t.TempDir()

	if _, ok, err := aclLookup(dir, "nPvV3wCNTRL"); ok || err != nil {
		t.Fatalf("empty ACL: ok=%v err=%v, want no entry and no error", ok, err)
	}

	if err := aclSet(dir, "nPvV3wCNTRL", "alpha5-297b", true); err != nil {
		t.Fatal(err)
	}
	e, ok, err := aclLookup(dir, "nPvV3wCNTRL")
	if err != nil || !ok {
		t.Fatalf("lookup after set: ok=%v err=%v", ok, err)
	}
	if !e.Allow || e.Hostname != "alpha5-297b" || e.Decided.IsZero() {
		t.Errorf("entry = %+v", e)
	}

	// Flipping replaces, it doesn't append.
	if err := aclSet(dir, "nPvV3wCNTRL", "alpha5-297b", false); err != nil {
		t.Fatal(err)
	}
	if e, _, _ := aclLookup(dir, "nPvV3wCNTRL"); e.Allow {
		t.Error("entry still allowed after being blocked")
	}
}

// StableID becomes a filename. Anything that isn't shaped like one must be
// refused rather than joined into a path.
func TestACLRejectsPathLikeStableIDs(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"", "../escape", "a/b", `a\b`, "..", "id with space", strings.Repeat("a", 65)} {
		if err := aclSet(dir, id, "x", true); err == nil {
			t.Errorf("aclSet accepted %q", id)
		}
		if _, _, err := aclLookup(dir, id); err == nil {
			t.Errorf("aclLookup accepted %q", id)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); err == nil {
		t.Error("a traversal id wrote outside the acl directory")
	}
}

// A hostname with a newline must not be able to add a second key to the
// file, e.g. flipping allow.
func TestACLHostnameCannotInjectKeys(t *testing.T) {
	dir := t.TempDir()
	if err := aclSet(dir, "peer1", "evil\nallow=true", false); err != nil {
		t.Fatal(err)
	}
	e, _, _ := aclLookup(dir, "peer1")
	if e.Allow {
		t.Error("newline in hostname injected allow=true")
	}
}

func TestACLListSkipsTempFiles(t *testing.T) {
	dir := t.TempDir()
	_ = aclSet(dir, "peer1", "one", true)
	_ = aclSet(dir, "peer2", "two", false)
	_ = os.WriteFile(filepath.Join(dir, aclDir, "peer3.tmp"), []byte("allow=true\n"), 0o600)

	got := aclList(dir)
	if len(got) != 2 || !got["peer1"].Allow || got["peer2"].Allow {
		t.Errorf("aclList = %+v", got)
	}
}

func nodeWithDir(t *testing.T) *Node {
	t.Helper()
	// Policy tests shouldn't depend on how full the machine running them is.
	withFreeSpace(t, 1<<60)
	return &Node{cfg: &Config{DataDir: t.TempDir()}}
}

func manifestOf(size int64) *blob.Manifest {
	return &blob.Manifest{Size: size}
}

func TestCheckAcceptDefaultsToDeny(t *testing.T) {
	n := nodeWithDir(t)
	if err := n.checkAccept("stranger", "stranger", manifestOf(10)); err == nil {
		t.Error("a peer with no entry was accepted under the default policy")
	}
}

func TestCheckAcceptHonoursEntries(t *testing.T) {
	n := nodeWithDir(t)
	_ = n.SetTrust("friend", "friend", true)
	_ = n.SetTrust("foe", "foe", false)

	if err := n.checkAccept("friend", "friend", manifestOf(10)); err != nil {
		t.Errorf("allowed peer refused: %v", err)
	}
	if err := n.checkAccept("foe", "foe", manifestOf(10)); err == nil {
		t.Error("blocked peer accepted")
	}
	// An explicit block beats a permissive default.
	_ = SaveToken(n.cfg.DataDir, aclDefaultFile, "allow")
	if err := n.checkAccept("foe", "foe", manifestOf(10)); err == nil {
		t.Error("default=allow overrode an explicit block")
	}
	if err := n.checkAccept("unknown", "unknown", manifestOf(10)); err != nil {
		t.Errorf("default=allow refused an unknown peer: %v", err)
	}
}

// A garbled default file must fail closed, not open.
func TestACLDefaultFailsClosed(t *testing.T) {
	n := nodeWithDir(t)
	for _, v := range []string{"", "ALLOW!", "yes", "true", "  "} {
		_ = SaveToken(n.cfg.DataDir, aclDefaultFile, v)
		if aclDefaultAllows(n.cfg.DataDir) {
			t.Errorf("default file %q was read as allow", v)
		}
	}
	_ = SaveToken(n.cfg.DataDir, aclDefaultFile, "allow\n")
	if !aclDefaultAllows(n.cfg.DataDir) {
		t.Error(`"allow\n" should allow`)
	}
}

func TestCheckAcceptEnforcesSizeCap(t *testing.T) {
	n := nodeWithDir(t)
	_ = n.SetTrust("friend", "friend", true)

	if err := n.checkAccept("friend", "friend", manifestOf(defaultMaxBlobBytes+1)); err == nil {
		t.Error("a blob over the default cap was accepted")
	}

	_ = SaveToken(n.cfg.DataDir, maxBlobBytesFile, "100")
	if err := n.checkAccept("friend", "friend", manifestOf(101)); err == nil {
		t.Error("the per-profile cap override was ignored")
	}
	if err := n.checkAccept("friend", "friend", manifestOf(100)); err != nil {
		t.Errorf("a blob exactly at the cap was refused: %v", err)
	}
}

// A peer with no attested identity (empty StableID) has no ACL entry and
// can't be given one, so it falls to the default — deny.
func TestCheckAcceptRefusesUnattestedPeer(t *testing.T) {
	n := nodeWithDir(t)
	if err := n.checkAccept("", "unknown", manifestOf(10)); err == nil {
		t.Error("a peer with no StableID was accepted")
	}
}

func TestReserveInboxFileNeverReusesAName(t *testing.T) {
	dir := t.TempDir()
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		p, err := reserveInboxFile(dir, "movie.mkv")
		if err != nil {
			t.Fatal(err)
		}
		if seen[p] {
			t.Fatalf("path %q handed out twice", p)
		}
		seen[p] = true
	}
	if _, ok := seen[filepath.Join(dir, "movie (1).mkv")]; !ok {
		t.Errorf("collisions should become 'movie (1).mkv', got %v", seen)
	}
	if p, _ := reserveInboxFile(dir, ""); filepath.Base(p) != "blob" {
		t.Errorf("empty name should fall back to %q, got %q", "blob", p)
	}
}
