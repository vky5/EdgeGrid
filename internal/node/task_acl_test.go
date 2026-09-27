package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskACLRoundTrip(t *testing.T) {
	dir := t.TempDir()

	if _, ok, err := taskACLLookup(dir, "nPvV3wCNTRL"); ok || err != nil {
		t.Fatalf("empty ACL: ok=%v err=%v, want no entry and no error", ok, err)
	}

	if err := taskACLSet(dir, "nPvV3wCNTRL", "alpha5-297b", true); err != nil {
		t.Fatal(err)
	}
	e, ok, err := taskACLLookup(dir, "nPvV3wCNTRL")
	if err != nil || !ok {
		t.Fatalf("lookup after set: ok=%v err=%v", ok, err)
	}
	if !e.Allow || e.Hostname != "alpha5-297b" || e.Decided.IsZero() {
		t.Errorf("entry = %+v", e)
	}

	// Flipping replaces, it doesn't append.
	if err := taskACLSet(dir, "nPvV3wCNTRL", "alpha5-297b", false); err != nil {
		t.Fatal(err)
	}
	if e, _, _ := taskACLLookup(dir, "nPvV3wCNTRL"); e.Allow {
		t.Error("entry still allowed after being blocked")
	}
}

func TestTaskACLRejectsPathLikeStableIDs(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"", "../escape", "a/b", `a\b`, "..", "id with space", strings.Repeat("a", 65)} {
		if err := taskACLSet(dir, id, "x", true); err == nil {
			t.Errorf("taskACLSet accepted %q", id)
		}
		if _, _, err := taskACLLookup(dir, id); err == nil {
			t.Errorf("taskACLLookup accepted %q", id)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); err == nil {
		t.Error("a traversal id wrote outside the acl-tasks directory")
	}
}

func TestTaskACLHostnameCannotInjectKeys(t *testing.T) {
	dir := t.TempDir()
	if err := taskACLSet(dir, "peer1", "evil\nallow=true", false); err != nil {
		t.Fatal(err)
	}
	e, _, _ := taskACLLookup(dir, "peer1")
	if e.Allow {
		t.Error("newline in hostname injected allow=true")
	}
}

func TestTaskACLListSkipsTempFiles(t *testing.T) {
	dir := t.TempDir()
	_ = taskACLSet(dir, "peer1", "one", true)
	_ = taskACLSet(dir, "peer2", "two", false)
	_ = os.WriteFile(filepath.Join(dir, taskACLDir, "peer3.tmp"), []byte("allow=true\n"), 0o600)

	got := taskACLList(dir)
	if len(got) != 2 || !got["peer1"].Allow || got["peer2"].Allow {
		t.Errorf("taskACLList = %+v", got)
	}
}

// A garbled default file must fail closed, not open.
func TestTaskACLDefaultFailsClosed(t *testing.T) {
	n := nodeWithDir(t)
	for _, v := range []string{"", "ALLOW!", "yes", "true", "  "} {
		_ = SaveToken(n.cfg.DataDir, taskACLDefaultFile, v)
		if taskACLDefaultAllows(n.cfg.DataDir) {
			t.Errorf("default file %q was read as allow", v)
		}
	}
	_ = SaveToken(n.cfg.DataDir, taskACLDefaultFile, "allow\n")
	if !taskACLDefaultAllows(n.cfg.DataDir) {
		t.Error(`"allow\n" should allow`)
	}
}

func TestTrustedTaskPeersAndSetTaskTrust(t *testing.T) {
	n := nodeWithDir(t)
	if got := n.TrustedTaskPeers(); len(got) != 0 {
		t.Fatalf("fresh node has trusted task peers: %+v", got)
	}

	if err := n.SetTaskTrust("friend", "friend-host", true); err != nil {
		t.Fatal(err)
	}
	if err := n.SetTaskTrust("foe", "foe-host", false); err != nil {
		t.Fatal(err)
	}

	got := n.TrustedTaskPeers()
	if !got["friend"] || got["foe"] {
		t.Errorf("TrustedTaskPeers = %+v", got)
	}

	// This ACL is independent of the file ACL — granting task trust must
	// not also grant, or read from, file-transfer trust.
	if got := n.TrustedPeers(); len(got) != 0 {
		t.Errorf("SetTaskTrust leaked into the file ACL: %+v", got)
	}
}
