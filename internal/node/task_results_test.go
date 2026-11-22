package node

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
)

func TestDoneRunKeepsItsFilesAndListsThem(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	id, err := sendTaskID(t, n, "friend", "train", nil)
	if err != nil {
		t.Fatal(err)
	}
	n.runs.Wait()

	r, err := n.history.Task(db.RoleReceived, id)
	if err != nil {
		t.Fatal(err)
	}
	// only model.pt: the folder and the link are skipped
	want := db.TaskFile{
		Name:   "model.pt",
		Size:   7,
		SHA256: "33fbcd8470eae125a265eb3f4375137f38ea4dc42f75ac09594bfe1a1c951af6", // printf trained | sha256sum,
	}
	if r.Status != db.TaskDone || string(r.Output) != "loss 0.03\n" || len(r.Files) != 1 || r.Files[0] != want {
		t.Fatalf("row = %+v", r)
	}
	dir, _ := resultsDirFor(n.cfg.DataDir, id)
	b, err := os.ReadFile(filepath.Join(dir, "model.pt"))
	if err != nil || string(b) != "trained" {
		t.Errorf("model.pt = %q, %v; want it kept", b, err)
	}
}

func TestRunWithoutFilesLeavesNoFolder(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	id, err := sendTaskID(t, n, "friend", "echo", nil)
	if err != nil {
		t.Fatal(err)
	}
	n.runs.Wait()

	if r, _ := n.history.Task(db.RoleReceived, id); r.Status != db.TaskDone || r.Files != nil {
		t.Errorf("row = %+v, want done with no files", r)
	}
	dir, _ := resultsDirFor(n.cfg.DataDir, id)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("results folder still there: %v", err)
	}
}

func TestFailedRunDropsItsFiles(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")

	id, err := sendTaskID(t, n, "friend", "crash", nil)
	if err != nil {
		t.Fatal(err)
	}
	n.runs.Wait()

	if r, _ := n.history.Task(db.RoleReceived, id); r.Status != db.TaskFailed || r.Files != nil {
		t.Errorf("row = %+v, want failed with no files", r)
	}
	dir, _ := resultsDirFor(n.cfg.DataDir, id)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("half-written results folder still there: %v", err)
	}
}

func TestListResultFilesHashesEachFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\n"), 0o600)
	files, err := listResultFiles("t1", dir)
	if err != nil {
		t.Fatal(err)
	}
	// sha256 of "hello world\n", as printed by `printf 'hello world\n' | sha256sum`
	want := db.TaskFile{Name: "a.txt", Size: 12, SHA256: "a948904f2f0f479b8f8197694b30184b0d2ed1c1cd2a1ec0fb85d299a192a447"}
	if len(files) != 1 || files[0] != want {
		t.Errorf("files = %+v, want [%+v]", files, want)
	}
}

func TestSenderFetchesTheFilesThroughHandlePeer(t *testing.T) {
	n, _ := nodeForTasks(t)
	trust(t, n, "friend")
	id, err := sendTaskID(t, n, "friend", "train", nil)
	if err != nil {
		t.Fatal(err)
	}
	n.runs.Wait()

	asker, receiver := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		n.handlePeer(peerWho("friend"), discovery.Hello{NodeID: "x", Intent: discovery.IntentResult}, receiver)
	}()
	resp, err := task.AskResult(asker, id)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != task.StatusDone || len(resp.Files) != 1 || resp.Files[0].Name != "model.pt" {
		t.Fatalf("resp = %+v, want done listing model.pt", resp)
	}
	dest := t.TempDir()
	if err := task.FetchResultFiles(asker, resp.Files, []string{"model.pt"}, dest, nil); err != nil {
		t.Fatal(err)
	}
	asker.Close()
	<-done

	b, err := os.ReadFile(filepath.Join(dest, "model.pt"))
	if err != nil || string(b) != "trained" {
		t.Errorf("fetched model.pt = %q, %v", b, err)
	}
}
