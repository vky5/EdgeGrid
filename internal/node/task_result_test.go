package node

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
)

// askResultThroughHandlePeer drives the real handlePeer -> receiveResult path.
func askResultThroughHandlePeer(t *testing.T, n *Node, stableID, taskID string) task.ResultResponse {
	t.Helper()
	asker, receiver := net.Pipe()
	defer asker.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		n.handlePeer(peerWho(stableID), discovery.Hello{NodeID: "x", Intent: discovery.IntentResult}, receiver)
	}()
	resp, err := task.AskResult(asker, taskID)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestResultIsStoredAndServedToTheDispatcher(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	id, err := sendTaskID(t, n, "friend", "write", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	n.runs.Wait()

	resp := askResultThroughHandlePeer(t, n, "friend", id)
	if resp.Status != task.StatusDone {
		t.Fatalf("status = %q, want done (%+v)", resp.Status, resp)
	}
}

func TestResultCarriesTheProcessOutput(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	id, err := sendTaskID(t, n, "friend", "echo", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	n.runs.Wait()

	resp := askResultThroughHandlePeer(t, n, "friend", id)
	if resp.Status != task.StatusDone || string(resp.Output) != `{"x":1}` {
		t.Errorf("resp = %+v, want done with the input echoed back", resp)
	}
}

func TestResultIsRunningWhileTheTaskRuns(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	id, err := sendTaskID(t, n, "friend", "sleep", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp := askResultThroughHandlePeer(t, n, "friend", id); resp.Status != task.StatusRunning {
		t.Errorf("status = %q, want running", resp.Status)
	}
}

func TestResultReportsAFailedRun(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	id, err := sendTaskID(t, n, "friend", "fail", nil)
	if err != nil {
		t.Fatal(err)
	}
	n.runs.Wait()

	resp := askResultThroughHandlePeer(t, n, "friend", id)
	if resp.Status != task.StatusFailed || !strings.Contains(resp.Error, "boom") {
		t.Errorf("resp = %+v, want failed with stderr in the error", resp)
	}
}

// Another peer asking for a real task ID must get the same answer as for an
// ID that doesn't exist.
func TestResultIsUnknownToAnyoneButTheDispatcher(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	id, err := sendTaskID(t, n, "friend", "noop", nil)
	if err != nil {
		t.Fatal(err)
	}
	n.runs.Wait()

	if resp := askResultThroughHandlePeer(t, n, "stranger", id); resp.Status != task.StatusUnknown {
		t.Errorf("a stranger got %+v for a real task", resp)
	}
	if resp := askResultThroughHandlePeer(t, n, "friend", "00000000-0000-4000-8000-000000000000"); resp.Status != task.StatusUnknown {
		t.Errorf("an unknown id got %+v", resp)
	}
}

func TestResultWithNoDatabaseIsUnknown(t *testing.T) {
	n := nodeWithDir(t)
	if resp := askResultThroughHandlePeer(t, n, "friend", "00000000-0000-4000-8000-000000000000"); resp.Status != task.StatusUnknown {
		t.Errorf("resp = %+v, want unknown", resp)
	}
}

func TestTaskIsRefusedWhenResultsCannotBeRecorded(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")
	n.history = nil

	err := sendTaskThroughHandlePeer(t, n, "friend", "noop", nil)
	var refused *task.RefusedError
	if !asRefused(err, &refused) || !strings.Contains(refused.Reason, "record results") {
		t.Fatalf("err = %v, want a can't-record refusal", err)
	}
	if !n.taskSlot.TryClaim() {
		t.Error("the refusal left the slot claimed")
	}
}

func TestShutdownMarksARunningTaskInterrupted(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	id, err := sendTaskID(t, n, "friend", "sleep", nil)
	if err != nil {
		t.Fatal(err)
	}
	n.stopRuns()
	n.runs.Wait()

	rec, err := n.history.Task(db.RoleReceived, id)
	if err != nil || rec.Status != db.TaskInterrupted {
		t.Errorf("rec = %+v, err = %v, want interrupted", rec, err)
	}
}
