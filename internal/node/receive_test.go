package node

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/edgegrid/edgegrid/internal/blob"
	"github.com/edgegrid/edgegrid/internal/discovery"
)

// The unit tests cover checkAccept and blob.Receive separately. This one
// drives the real handlePeer -> receiveBlob -> blob.Receive path, because
// the wiring between them (the accept closure handed to Receive) is the
// part a refactor could silently break: pass a callback that always says
// yes and every other test would still pass.
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

// The identity that counts is the one Tailscale attributed, not what the
// peer claimed in its hello. Trusting "claimed-id" must not admit a peer
// whose StableID has no entry.
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
