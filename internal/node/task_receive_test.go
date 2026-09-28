package node

import (
	"errors"
	"net"
	"testing"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
)

// Mirrors sendThroughHandlePeer in receive_test.go, but for the task path:
// drives the real handlePeer -> receiveTask -> task.ProcessTask wiring, not
// just checkTaskAccept in isolation.
func sendTaskThroughHandlePeer(t *testing.T, n *Node, stableID string) error {
	t.Helper()

	tk, err := task.New("noop", task.Requirements{})
	if err != nil {
		t.Fatal(err)
	}

	sender, receiver := net.Pipe()
	defer sender.Close()

	who := &apitype.WhoIsResponse{Node: &tailcfg.Node{StableID: tailcfg.StableNodeID(stableID), ComputedName: "peer"}}
	hello := discovery.Hello{NodeID: "claimed-id", Intent: discovery.IntentTask}

	done := make(chan struct{})
	go func() {
		defer close(done)
		n.handlePeer(who, hello, receiver)
	}()

	sendErr := task.Offer(sender, tk)
	<-done
	return sendErr
}

func TestHandlePeerRefusesATaskWithNoACLEntry(t *testing.T) {
	n := nodeWithDir(t)

	err := sendTaskThroughHandlePeer(t, n, "stranger")
	var refused *task.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Offer error = %v, want the receiver's refusal", err)
	}
}

func TestHandlePeerRunsATaskForAnAllowedPeer(t *testing.T) {
	n := nodeWithDir(t)
	if err := n.SetTaskTrust("friend", "friend", true); err != nil {
		t.Fatal(err)
	}

	if err := sendTaskThroughHandlePeer(t, n, "friend"); err != nil {
		t.Fatalf("an allowed peer's task was refused: %v", err)
	}
}

// Identity for the task ACL is what Tailscale attributed, not the peer's
// own claim in its hello.
func TestHandlePeerIgnoresTheClaimedNodeIDForTasks(t *testing.T) {
	n := nodeWithDir(t)
	if err := n.SetTaskTrust("claimed-id", "liar", true); err != nil {
		t.Fatal(err)
	}

	err := sendTaskThroughHandlePeer(t, n, "stranger") // hello says claimed-id, WhoIs says stranger
	var refused *task.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Offer error = %v, want a refusal: the claimed NodeID was trusted", err)
	}
}

// TryClaim without a matching Release would strand the node permanently
// busy after its first task — this proves the slot actually frees up.
func TestHandlePeerReleasesTheTaskSlotAfterRunning(t *testing.T) {
	n := nodeWithDir(t)
	if err := n.SetTaskTrust("friend", "friend", true); err != nil {
		t.Fatal(err)
	}

	if err := sendTaskThroughHandlePeer(t, n, "friend"); err != nil {
		t.Fatalf("first task failed: %v", err)
	}
	if err := sendTaskThroughHandlePeer(t, n, "friend"); err != nil {
		t.Fatalf("second task after the first released was refused: %v", err)
	}
}

// A second task offered while one is still running gets refused as busy,
// not silently double-accepted.
func TestHandlePeerRefusesATaskWhileAnotherIsRunning(t *testing.T) {
	n := nodeWithDir(t)
	if !n.taskSlot.TryClaim() {
		t.Fatal("could not claim the slot to set up the test")
	}
	defer n.taskSlot.Release()

	if err := n.SetTaskTrust("friend", "friend", true); err != nil {
		t.Fatal(err)
	}

	err := sendTaskThroughHandlePeer(t, n, "friend")
	var refused *task.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Offer error = %v, want a busy refusal", err)
	}
}
