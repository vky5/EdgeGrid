package node

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/edgegrid/edgegrid/internal/blob"
	"github.com/edgegrid/edgegrid/internal/discovery"
)

// sends a file through the real handlePeer -> receiveBlob -> blob.Receive path
// a broken accept callback, e.g. one that always says yes, would pass every other test
func sendThroughHandlePeer(t *testing.T, n *Node, stableID string) (sendErr error) {
	t.Helper()

	src := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(src, []byte("the actual file bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := blob.BuildManifest(src, "application/octet-stream", 8)
	if err != nil {
		t.Fatal(err)
	}

	sender, receiver := net.Pipe()
	defer sender.Close()

	who := &apitype.WhoIsResponse{Node: &tailcfg.Node{StableID: tailcfg.StableNodeID(stableID), ComputedName: "peer"}}
	hello := discovery.Hello{NodeID: "claimed-id", Intent: discovery.IntentBlob}

	done := make(chan struct{})
	go func() {
		defer close(done)
		n.handlePeer(who, hello, receiver) // closes receiver when finished
	}()

	sendErr = blob.Send(sender, src, m, nil)
	<-done
	return sendErr
}

func TestHandlePeerRefusesAPeerWithNoACLEntry(t *testing.T) {
	n := nodeWithDir(t)

	err := sendThroughHandlePeer(t, n, "stranger")
	var refused *blob.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Send error = %v, want the receiver's refusal", err)
	}

	if entries, _ := os.ReadDir(filepath.Join(n.cfg.DataDir, "inbox")); len(entries) != 0 {
		t.Errorf("a refused transfer left %d file(s) in the inbox", len(entries))
	}
}

func TestHandlePeerAcceptsAllowedPeerAndKeepsItsFilename(t *testing.T) {
	n := nodeWithDir(t)
	if err := n.SetTrust("friend", "friend", true); err != nil {
		t.Fatal(err)
	}

	if err := sendThroughHandlePeer(t, n, "friend"); err != nil {
		t.Fatalf("an allowed peer's transfer failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(n.cfg.DataDir, "inbox", "movie.mkv"))
	if err != nil {
		t.Fatalf("file did not land under its own name: %v", err)
	}
	if string(got) != "the actual file bytes" {
		t.Errorf("received %q", got)
	}
}

// who counts is who Tailscale says, not who the hello claims
// e.g. trusting "claimed-id" must not let in a peer whose Tailscale ID has no entry
func TestHandlePeerIgnoresTheClaimedNodeID(t *testing.T) {
	n := nodeWithDir(t)
	if err := n.SetTrust("claimed-id", "liar", true); err != nil { // keyed on the claim
		t.Fatal(err)
	}

	err := sendThroughHandlePeer(t, n, "stranger") // hello says claimed-id, WhoIs says stranger
	var refused *blob.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Send error = %v, want a refusal: the claimed NodeID was trusted", err)
	}
}

// the manifest the TUI built is reused so the file isn't hashed twice, but only while it still matches the file
func TestManifestForReusesAValidManifestAndRebuildsAStaleOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	built, err := blob.BuildManifest(path, "application/octet-stream", 0)
	if err != nil {
		t.Fatal(err)
	}

	got, err := manifestFor(path, built)
	if err != nil || got != built {
		t.Errorf("a manifest matching the file should be reused as-is (same pointer); got %p err=%v", got, err)
	}

	// The file grew after it was hashed: the old manifest is now wrong.
	if err := os.WriteFile(path, []byte("0123456789 and more"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh, err := manifestFor(path, built)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == built || fresh.Size != int64(len("0123456789 and more")) {
		t.Errorf("a stale manifest was reused: size=%d", fresh.Size)
	}

	// No manifest at all still works: it builds one.
	if m, err := manifestFor(path, nil); err != nil || m == nil {
		t.Errorf("manifestFor(nil) = %v, %v", m, err)
	}
}

func TestTransferSnapshotCarriesVerifying(t *testing.T) {
	var r registry
	tr := r.start(Inbound, "peer")
	tr.progressFunc()(blob.Progress{ChunksDone: 3, ChunksTotal: 3, BytesDone: 30, BytesTotal: 30, Verifying: true})

	got := r.snapshot()
	if len(got) != 1 || !got[0].Progress.Verifying {
		t.Errorf("snapshot = %+v, want Verifying carried through", got)
	}
}

func TestRateMBps(t *testing.T) {
	if got := rateMBps(10<<20, 2*time.Second); got != 5 {
		t.Errorf("rateMBps = %v, want 5", got)
	}
	if got := rateMBps(1<<20, 0); got != 0 {
		t.Errorf("zero duration should give 0, got %v", got)
	}
}
