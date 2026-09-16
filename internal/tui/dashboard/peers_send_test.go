package dashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/edgegrid/edgegrid/internal/discovery"
)

func onlinePeer(id, host string) discovery.Peer {
	return discovery.Peer{ID: id, Hostname: host, Online: true}
}

// modelWith builds a browsing-state peers model without touching Tailscale
// — newPeersModel would try to read live membership.
func modelWith(peers ...discovery.Peer) peersModel {
	return peersModel{peers: peers, width: 100, height: 24}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestCursorMovesAndClampsAtBothEnds(t *testing.T) {
	m := modelWith(onlinePeer("a", "alpha"), onlinePeer("b", "bravo"))

	m, _ = m.updateBrowsing(key("up")) // already at top
	if m.cursor != 0 {
		t.Errorf("cursor went above the first row: %d", m.cursor)
	}
	m, _ = m.updateBrowsing(key("down"))
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1", m.cursor)
	}
	m, _ = m.updateBrowsing(key("down")) // already at bottom
	if m.cursor != 1 {
		t.Errorf("cursor ran past the last row: %d", m.cursor)
	}
}

// A shrinking peer list must not leave the cursor pointing past the end —
// the 5s refresh can drop a peer at any moment.
func TestRefreshClampsCursorWhenListShrinks(t *testing.T) {
	m := modelWith(onlinePeer("a", "alpha"), onlinePeer("b", "bravo"))
	m.cursor = 1
	m.peers = m.peers[:1]
	if m.cursor >= len(m.peers) {
		m.cursor = max(len(m.peers)-1, 0)
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 after the list shrank", m.cursor)
	}
}

func TestSendRefusesOfflinePeer(t *testing.T) {
	offline := discovery.Peer{ID: "b", Hostname: "bravo", Online: false}
	m := modelWith(offline)

	m, _ = m.updateBrowsing(key("s"))

	if m.mode != peersBrowsing {
		t.Errorf("entered the send flow for an offline peer, mode = %v", m.mode)
	}
	if m.flowErr == nil || !strings.Contains(m.flowErr.Error(), "offline") {
		t.Errorf("expected an offline error, got %v", m.flowErr)
	}
}

// The send target is captured by value when the flow starts. If it were an
// index into peers, the 5s refresh reordering the list mid-flow would
// silently retarget the send at a different machine.
func TestTargetSurvivesPeerListReordering(t *testing.T) {
	m := modelWith(onlinePeer("a", "alpha"), onlinePeer("b", "bravo"))
	m.cursor = 1

	m, _ = m.updateBrowsing(key("s"))
	if m.target.Hostname != "bravo" {
		t.Fatalf("target = %q, want bravo", m.target.Hostname)
	}

	// Refresh returns the same peers in the opposite order.
	m.peers = []discovery.Peer{onlinePeer("b", "bravo"), onlinePeer("a", "alpha")}

	if m.target.Hostname != "bravo" {
		t.Errorf("target changed to %q when the list reordered", m.target.Hostname)
	}
}

// Keystrokes must reach the path field instead of the dashboard's own
// shortcuts, but only while that field is focused.
func TestCapturesTextInputOnlyWhileTypingAPath(t *testing.T) {
	m := modelWith(onlinePeer("a", "alpha"))
	if m.capturesTextInput() {
		t.Error("captured input while merely browsing")
	}

	m, _ = m.updateBrowsing(key("s"))
	if !m.capturesTextInput() {
		t.Error("did not capture input while typing a path")
	}

	m = m.cancelFlow()
	if m.capturesTextInput() {
		t.Error("still capturing input after the flow was cancelled")
	}
}

func TestBuildsManifestAndShowsItsChunks(t *testing.T) {
	content := strings.Repeat("x", 10)
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.bin")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	m := modelWith(onlinePeer("a", "alpha"))
	m, _ = m.updateBrowsing(key("s"))
	m.input.SetValue(path)

	m, cmd := m.updatePickFile(key("enter"))
	if m.mode != peersBuilding {
		t.Fatalf("mode = %v, want peersBuilding", m.mode)
	}
	if cmd == nil {
		t.Fatal("no hashing command was scheduled")
	}

	m, _ = m.Update(cmd()) // run the hash, feed the result back
	if m.mode != peersReady {
		t.Fatalf("mode = %v, want peersReady (flowErr=%v)", m.mode, m.flowErr)
	}
	if m.manifest == nil {
		t.Fatal("no manifest on the model")
	}
	if m.manifest.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", m.manifest.Size, len(content))
	}

	sum := sha256.Sum256([]byte(content))
	if m.manifest.SHA256 != hex.EncodeToString(sum[:]) {
		t.Error("whole-blob hash does not match the file")
	}

	view := stripANSI(m.View())
	for _, want := range []string{"alpha", "chunks", "sha256"} {
		if !strings.Contains(view, want) {
			t.Errorf("confirm view missing %q: %s", want, view)
		}
	}
}

func TestMissingFileReturnsToThePrompt(t *testing.T) {
	m := modelWith(onlinePeer("a", "alpha"))
	m, _ = m.updateBrowsing(key("s"))
	m.input.SetValue("/does/not/exist")

	m, cmd := m.updatePickFile(key("enter"))
	m, _ = m.Update(cmd())

	if m.mode != peersPickFile {
		t.Errorf("mode = %v, want back at the path prompt", m.mode)
	}
	if m.flowErr == nil {
		t.Error("no error surfaced for a missing file")
	}
	if !m.capturesTextInput() {
		t.Error("path field lost focus after a failed hash")
	}
}

// Cancelling during a long hash must not be undone when the result lands.
func TestLateManifestResultDoesNotResurrectACancelledFlow(t *testing.T) {
	content := "abc"
	path := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	m := modelWith(onlinePeer("a", "alpha"))
	m, _ = m.updateBrowsing(key("s"))
	m.input.SetValue(path)
	m, cmd := m.updatePickFile(key("enter"))

	m = m.cancelFlow()
	m, _ = m.Update(cmd()) // hashing finishes after the cancel

	if m.mode != peersBrowsing {
		t.Errorf("a cancelled flow was resurrected, mode = %v", m.mode)
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}
	if got := expandPath("~/x.bin"); got != filepath.Join(home, "x.bin") {
		t.Errorf("expandPath(~/x.bin) = %q", got)
	}
	if got := expandPath("/abs/x.bin"); got != "/abs/x.bin" {
		t.Errorf("absolute path was rewritten: %q", got)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{4 << 20, "4.0 MiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.n); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
