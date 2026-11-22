package node

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
)

// B runs "train" for the peer "alpha", which leaves model.pt in B's results folder
func trainedOnB(t *testing.T) (b *Node, taskID string) {
	t.Helper()
	b, _ = nodeForTasks(t)
	trust(t, b, "alpha")
	id, err := sendTaskID(t, b, "alpha", "train", nil)
	if err != nil {
		t.Fatal(err)
	}
	b.runs.Wait()
	return b, id
}

// A, the sender, with its own data folder and a "sent" row for the task
func senderA(t *testing.T, taskID string) *Node {
	t.Helper()
	a := nodeWithDir(t)
	store, err := db.Open(filepath.Join(a.cfg.DataDir, historyFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	a.history = store
	if err := store.RecordTaskStart(db.RoleSent, taskID, "bravo-id", "bravo", "train"); err != nil {
		t.Fatal(err)
	}
	return a
}

// A asks B through B's real handler, the way askResult does after dialing
func askB(t *testing.T, a, b *Node, taskID string) (task.ResultResponse, error) {
	t.Helper()
	asker, receiver := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.handlePeer(peerWho("alpha"), discovery.Hello{NodeID: "x", Intent: discovery.IntentResult}, receiver)
	}()
	resp, err := a.askOver(asker, taskID)
	asker.Close()
	<-done
	return resp, err
}

func TestSenderSavesTheFilesBeforeCallingItDone(t *testing.T) {
	b, id := trainedOnB(t)
	a := senderA(t, id)

	resp, err := askB(t, a, b, id)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != task.StatusDone || len(resp.Files) != 1 {
		t.Fatalf("resp = %+v", resp)
	}
	got, err := os.ReadFile(filepath.Join(a.ResultsDir(id), "model.pt"))
	if err != nil || string(got) != "trained" {
		t.Fatalf("model.pt on A = %q, %v", got, err)
	}

	a.recordSentResult(id, resp)
	row, _ := a.history.Task(db.RoleSent, id)
	if row.Status != db.TaskDone || len(row.Files) != 1 || row.Files[0].Name != "model.pt" {
		t.Errorf("A's row = %+v, want done listing model.pt", row)
	}
}

func TestAskingAgainDoesNotDownloadAgain(t *testing.T) {
	b, id := trainedOnB(t)
	a := senderA(t, id)
	if _, err := askB(t, a, b, id); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.ResultsDir(id), "model.pt")
	before, _ := os.Stat(path)

	// if A wanted model.pt again, B would send it and A would replace the file
	if _, err := askB(t, a, b, id); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	// a fresh download is saved as a new file and renamed over the old one, so it wouldn't be the same file
	if !os.SameFile(before, after) {
		t.Error("model.pt was downloaded a second time")
	}
}

func TestTwoAsksDoNotFetchTheSameTaskAtOnce(t *testing.T) {
	b, id := trainedOnB(t)
	a := senderA(t, id)
	a.fetching.Store(id, struct{}{}) // as if a background check were mid-download

	if _, err := askB(t, a, b, id); err == nil || !strings.Contains(err.Error(), "already being fetched") {
		t.Errorf("err = %v, want already being fetched", err)
	}
	if _, err := os.Stat(filepath.Join(a.ResultsDir(id), "model.pt")); !os.IsNotExist(err) {
		t.Error("a second fetch wrote model.pt")
	}
}

func TestNotEnoughSpaceIsAnErrorNotDone(t *testing.T) {
	b, id := trainedOnB(t)
	a := senderA(t, id)
	withFreeSpace(t, 100<<20) // under the 256 MB kept spare

	resp, err := askB(t, a, b, id)
	if err == nil || !strings.Contains(err.Error(), "not enough free space") {
		t.Fatalf("resp %+v, err %v; want a free space error", resp, err)
	}
	if resp.Status == task.StatusDone {
		t.Error("answer counted as done without its files")
	}
	if row, _ := a.history.Task(db.RoleSent, id); row.Status != db.TaskRunning {
		t.Errorf("A's row = %s, want still running so it's asked again", row.Status)
	}
}
