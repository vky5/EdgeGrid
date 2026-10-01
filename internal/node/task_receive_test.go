package node

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/executor"
	"github.com/edgegrid/edgegrid/internal/task"
)

// nodeForTasks is nodeWithDir plus what New would set up for the task path:
// an executor with test kinds and a run context. marker is where the "write"
// kind writes its input, so a test can prove a run actually happened.
func nodeForTasks(t *testing.T) (n *Node, marker string) {
	t.Helper()
	n = nodeWithDir(t)
	marker = filepath.Join(t.TempDir(), "marker")

	e, err := executor.New([]executor.Definition{
		{Kind: "noop", Runtime: executor.RuntimeBare, Entrypoint: []string{"true"}},
		{Kind: "write", Runtime: executor.RuntimeBare, Entrypoint: []string{"sh", "-c", "cat > " + marker}},
		{Kind: "sleep", Runtime: executor.RuntimeBare, Entrypoint: []string{"sleep", "30"}},
	}, []executor.Runtime{executor.Bare{}}, filepath.Join(n.cfg.DataDir, taskRunsDir))
	if err != nil {
		t.Fatal(err)
	}
	n.executor = e
	n.runCtx, n.stopRuns = context.WithCancel(context.Background())
	t.Cleanup(func() {
		n.stopRuns()
		n.runs.Wait()
	})
	return n, marker
}

// Mirrors sendThroughHandlePeer in receive_test.go, but for the task path:
// drives the real handlePeer -> receiveTask -> task.ProcessTask wiring, not
// just checkTaskAccept in isolation.
func sendTaskThroughHandlePeer(t *testing.T, n *Node, stableID, kind string, input json.RawMessage) error {
	t.Helper()

	tk, err := task.New(kind, task.Requirements{})
	if err != nil {
		t.Fatal(err)
	}
	tk.Input = input

	sender, receiver := net.Pipe()
	defer sender.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		n.handlePeer(peerWho(stableID), taskHello(), receiver)
	}()

	sendErr := task.Offer(sender, tk)
	<-done
	return sendErr
}

func peerWho(stableID string) *apitype.WhoIsResponse {
	return &apitype.WhoIsResponse{Node: &tailcfg.Node{StableID: tailcfg.StableNodeID(stableID), ComputedName: "peer"}}
}

func taskHello() discovery.Hello {
	return discovery.Hello{NodeID: "claimed-id", Intent: discovery.IntentTask}
}

func trust(t *testing.T, n *Node, stableID string) {
	t.Helper()
	if err := n.SetTaskTrust(stableID, stableID, true); err != nil {
		t.Fatal(err)
	}
}

func TestHandlePeerRefusesATaskWithNoACLEntry(t *testing.T) {
	n, _ := nodeForTasks(t)

	err := sendTaskThroughHandlePeer(t, n, "stranger", "noop", nil)
	var refused *task.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Offer error = %v, want the receiver's refusal", err)
	}
}

func TestHandlePeerRunsATaskForAnAllowedPeer(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	if err := sendTaskThroughHandlePeer(t, n, "friend", "noop", nil); err != nil {
		t.Fatalf("an allowed peer's task was refused: %v", err)
	}
}

// Identity for the task ACL is what Tailscale attributed, not the peer's
// own claim in its hello.
func TestHandlePeerIgnoresTheClaimedNodeIDForTasks(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "claimed-id")

	err := sendTaskThroughHandlePeer(t, n, "stranger", "noop", nil) // hello says claimed-id, WhoIs says stranger
	var refused *task.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Offer error = %v, want a refusal: the claimed NodeID was trusted", err)
	}
}

// An unknown kind is refused before the verdict, so the dispatcher moves on
// to the next peer instead of getting an accept for something that can't run.
func TestHandlePeerRefusesAnUnknownKind(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	err := sendTaskThroughHandlePeer(t, n, "friend", "resnet", nil)
	var refused *task.RefusedError
	if !errors.As(err, &refused) || !strings.Contains(refused.Reason, "resnet") {
		t.Fatalf("Offer error = %v, want a refusal naming the kind", err)
	}
	if !n.taskSlot.TryClaim() {
		t.Error("an unrunnable kind left the slot claimed")
	}
}

// Input travels from the dispatcher's Task all the way to the process's stdin.
func TestHandlePeerRunsTheTaskWithItsInput(t *testing.T) {
	n, marker := nodeForTasks(t)
	trust(t, n, "friend")

	if err := sendTaskThroughHandlePeer(t, n, "friend", "write", json.RawMessage(`{"x":1}`)); err != nil {
		t.Fatal(err)
	}
	n.runs.Wait()

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the task never ran: %v", err)
	}
	if string(got) != `{"x":1}` {
		t.Errorf("run saw input %q, want %q", got, `{"x":1}`)
	}
}

// The slot is released when the run finishes, not when the connection
// closes — so a second task is accepted once the first run is done.
func TestHandlePeerReleasesTheTaskSlotAfterTheRun(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	if err := sendTaskThroughHandlePeer(t, n, "friend", "noop", nil); err != nil {
		t.Fatalf("first task failed: %v", err)
	}
	n.runs.Wait()
	if err := sendTaskThroughHandlePeer(t, n, "friend", "noop", nil); err != nil {
		t.Fatalf("second task after the first finished was refused: %v", err)
	}
}

// While a run is still going, the slot stays held even though receiveTask
// has already returned and the connection is closed.
func TestHandlePeerRefusesATaskWhileAnotherIsRunning(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	if err := sendTaskThroughHandlePeer(t, n, "friend", "sleep", nil); err != nil {
		t.Fatalf("first task failed: %v", err)
	}
	err := sendTaskThroughHandlePeer(t, n, "friend", "noop", nil)
	var refused *task.RefusedError
	if !errors.As(err, &refused) || !strings.Contains(refused.Reason, "already running") {
		t.Fatalf("Offer error = %v, want a busy refusal", err)
	}
}

// accept claims the slot before the verdict is written. If that write fails
// because the dispatcher hung up, the slot must not stay claimed forever.
func TestHandlePeerFreesTheSlotWhenTheVerdictCannotBeSent(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	tk, err := task.New("noop", task.Requirements{})
	if err != nil {
		t.Fatal(err)
	}
	sender, receiver := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		n.handlePeer(peerWho("friend"), taskHello(), receiver)
	}()

	if err := task.WriteTask(sender, tk); err != nil {
		t.Fatal(err)
	}
	sender.Close() // hang up before reading the verdict
	<-done

	if !n.taskSlot.TryClaim() {
		t.Error("slot still claimed after the verdict write failed")
	}
}

// Close cancels a running task instead of waiting out its full duration.
func TestCloseStopsARunningTask(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	if err := sendTaskThroughHandlePeer(t, n, "friend", "sleep", nil); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	n.Close()
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("Close took %v, want it to cancel the 30s task", took)
	}
	if !n.taskSlot.TryClaim() {
		t.Error("slot still claimed after Close stopped the task")
	}
}
