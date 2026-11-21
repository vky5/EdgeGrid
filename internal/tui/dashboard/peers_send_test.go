package dashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/edgegrid/edgegrid/internal/discovery"
)

func onlinePeer(id, host string) discovery.Peer {
	return discovery.Peer{ID: id, Hostname: host, Online: true}
}

// a Peers tab on the browsing step that never asks Tailscale for peers
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

// the cursor must stay on the list when it shrinks; the 5s refresh can drop a peer any time
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

// the peer picked for a send stays the same when the 5s refresh reorders the list
// or the file would go to a different machine
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

// keys go to the path field instead of the shortcuts, but only while it's focused
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

// a cancel during a long hash stays cancelled when the hash finishes
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

// a file dropped onto a terminal arrives quoted or with backslashes, depending on the terminal
func TestExpandPathHandlesDraggedInPaths(t *testing.T) {
	cases := map[string]string{
		`'/home/v/my file.bin'`: "/home/v/my file.bin",
		`"/home/v/my file.bin"`: "/home/v/my file.bin",
		`/home/v/my\ file.bin`:  "/home/v/my file.bin",
		`  /home/v/plain.bin  `: "/home/v/plain.bin",
	}
	for in, want := range cases {
		if got := expandPath(in); got != want {
			t.Errorf("expandPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// two dropped files glued into one path are refused
// or os.Open fails later with a confusing error about a path nobody typed
func TestMultipleDroppedFilesAreRejected(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}

	if err := checkSinglePath(filepath.Join(home, "Downloads/one.mp4")); err != nil {
		t.Errorf("single path rejected: %v", err)
	}

	concatenated := filepath.Join(home, "Downloads/one.mp4") + filepath.Join(home, "Downloads/two.mkv")
	if err := checkSinglePath(concatenated); err == nil {
		t.Error("two concatenated paths were accepted")
	}

	spaced := "/tmp/one.mp4 /tmp/two.mkv"
	if err := checkSinglePath(spaced); err == nil {
		t.Error("two space-separated paths were accepted")
	}

	if err := checkSinglePath("/mnt/backup/home/vky5/file.bin"); err != nil {
		t.Errorf("legitimate path containing /home/ rejected: %v", err)
	}
}

// "a" flips a peer between allowed and blocked
// keyed by Tailscale's ID for the peer, not anything the peer says about itself
func TestTrustToggleAllowsThenBlocks(t *testing.T) {
	store := map[string]bool{}
	m := modelWith(onlinePeer("stable-1", "alpha"))
	m.trust = TrustFuncs{
		List: func() map[string]bool { return store },
		Set: func(id, _ string, allow bool) error {
			store[id] = allow
			return nil
		},
	}

	m, _ = m.updateBrowsing(key("a"))
	if !store["stable-1"] {
		t.Fatal("first press should allow an undecided peer")
	}
	if !strings.Contains(m.sendNote, "may now send") {
		t.Errorf("note = %q", m.sendNote)
	}

	m, _ = m.updateBrowsing(key("a"))
	if allowed, decided := store["stable-1"]; !decided || allowed {
		t.Errorf("second press should block, store = %v", store)
	}

	if !strings.Contains(stripANSI(m.trustMarker("stable-1")), "✗") {
		t.Error("blocked peer should show ✗")
	}
	if !strings.Contains(stripANSI(m.trustMarker("nobody")), "·") {
		t.Error("undecided peer should show ·")
	}
}

// at 100% the receiver is still checking the file, so the panel says so instead of a full bar that looks stuck
func TestTransferPanelSaysVerifyingAtOneHundredPercent(t *testing.T) {
	m := modelWith()
	m.active = []Transfer{{Direction: "receiving", Peer: "alpha", Frac: 1, BytesDone: 100, Total: 100, Verifying: true}}

	view := stripANSI(m.transfersView())
	if !strings.Contains(view, "verifying") {
		t.Errorf("view = %q, want it to say verifying", view)
	}
	if strings.Contains(view, "100%") {
		t.Errorf("view still shows a 100%% bar: %q", view)
	}
}

func TestPeerRowShowsTheRoute(t *testing.T) {
	direct := discovery.Peer{ID: "a", Hostname: "alpha", Online: true, CurAddr: "1.2.3.4:5"}
	relayed := discovery.Peer{ID: "b", Hostname: "bravo", Online: true, Relay: "mum"}
	m := modelWith(direct, relayed)

	view := stripANSI(m.View())
	if !strings.Contains(view, "direct") || !strings.Contains(view, "relay") {
		t.Errorf("peer rows should show direct and relay, got:\n%s", view)
	}
}

func TestSpeedSuffix(t *testing.T) {
	start := time.Now()
	tr := Transfer{Started: start, BytesDone: 100 << 20, Total: 1000 << 20}

	if got := speedSuffix(tr, start.Add(500*time.Millisecond)); got != "" {
		t.Errorf("first second showed %q, want nothing", got)
	}
	got := speedSuffix(tr, start.Add(10*time.Second))
	if !strings.Contains(got, "10.0 MiB/s") || !strings.Contains(got, "ETA 1m30s") {
		t.Errorf("speedSuffix = %q, want 10.0 MiB/s and ETA 1m30s", got)
	}
	if got := speedSuffix(Transfer{BytesDone: 5, Total: 10}, start); got != "" {
		t.Errorf("unknown start showed %q", got)
	}
}
