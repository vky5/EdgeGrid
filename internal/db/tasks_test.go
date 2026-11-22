package db

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskRoundTrip(t *testing.T) {
	s := openTest(t)
	if err := s.RecordTaskStart(RoleReceived, "t1", "peer-a", "alpha", "hello"); err != nil {
		t.Fatal(err)
	}
	r, err := s.Task(RoleReceived, "t1")
	if err != nil || r.Status != TaskRunning || !r.FinishedAt.IsZero() {
		t.Fatalf("after start: %+v, %v", r, err)
	}

	if err := s.RecordTaskFinish(RoleReceived, "t1", TaskDone, []byte("hi\n"), "", nil); err != nil {
		t.Fatal(err)
	}
	r, _ = s.Task(RoleReceived, "t1")
	if r.Status != TaskDone || string(r.Output) != "hi\n" || r.FinishedAt.IsZero() || r.Kind != "hello" || r.PeerHostname != "alpha" {
		t.Errorf("after finish: %+v", r)
	}
}

func TestSentAndReceivedRowsAreIndependent(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleSent, "t1", "peer-b", "bravo", "hello")
	s.RecordTaskStart(RoleReceived, "t1", "peer-a", "alpha", "hello")
	s.RecordTaskFinish(RoleSent, "t1", TaskFailed, nil, "boom", nil)

	r, _ := s.Task(RoleReceived, "t1")
	if r.Status != TaskRunning {
		t.Errorf("finishing the sent row touched the received row: %+v", r)
	}
}

func TestFailedTaskDropsOutputAndKeepsError(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleReceived, "t1", "p", "h", "k")
	s.RecordTaskFinish(RoleReceived, "t1", TaskFailed, []byte("partial"), "exit 3", nil)
	r, _ := s.Task(RoleReceived, "t1")
	if r.Output != nil || r.Error != "exit 3" {
		t.Errorf("got %+v", r)
	}
}

func TestLongOutputKeepsItsLastPart(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleReceived, "t1", "p", "h", "k")
	// about 165 KB of per-batch lines, like 5 epochs of MNIST, then the line that matters
	var log bytes.Buffer
	for e := 1; e <= 5; e++ {
		for b := 1; b <= 938; b++ {
			fmt.Fprintf(&log, "epoch %d batch %d/938 loss 0.1234\n", e, b)
		}
	}
	log.WriteString("test accuracy 0.9871\n")
	s.RecordTaskFinish(RoleReceived, "t1", TaskDone, log.Bytes(), "", nil)

	r, _ := s.Task(RoleReceived, "t1")
	out := string(r.Output)
	if r.Status != TaskDone || len(r.Output) > MaxTaskOutput {
		t.Fatalf("status %s, %d bytes; want done within %d", r.Status, len(r.Output), MaxTaskOutput)
	}
	if !strings.HasPrefix(out, outputCutNote+"epoch ") {
		t.Errorf("starts %q, want the note then a whole line", out[:min(len(out), 80)])
	}
	if !strings.HasSuffix(out, "epoch 5 batch 938/938 loss 0.1234\ntest accuracy 0.9871\n") {
		t.Errorf("ends %q, want the last lines kept", out[len(out)-60:])
	}

	s.RecordTaskStart(RoleReceived, "t2", "p", "h", "k")
	s.RecordTaskFinish(RoleReceived, "t2", TaskDone, bytes.Repeat([]byte("x"), MaxTaskOutput), "", nil)
	if r, _ := s.Task(RoleReceived, "t2"); r.Status != TaskDone {
		t.Errorf("output exactly at the limit was rejected: %+v", r)
	}
}

// Asking for another peer's task must look the same as asking for nothing.
func TestReceivedTaskForChecksTheDispatcher(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleReceived, "t1", "peer-a", "alpha", "k")

	if _, err := s.ReceivedTaskFor("t1", "peer-a"); err != nil {
		t.Errorf("owner refused: %v", err)
	}
	if _, err := s.ReceivedTaskFor("t1", "peer-evil"); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("other peer got %v, want ErrTaskNotFound", err)
	}
	if _, err := s.ReceivedTaskFor("nope", "peer-a"); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("missing id got %v, want ErrTaskNotFound", err)
	}
}

func TestFinishUnknownTaskIsAnError(t *testing.T) {
	s := openTest(t)
	if err := s.RecordTaskFinish(RoleReceived, "ghost", TaskDone, nil, "", nil); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("err = %v, want ErrTaskNotFound", err)
	}
}

func TestDuplicateTaskStartIsRejected(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleReceived, "t1", "p", "h", "k")
	if err := s.RecordTaskStart(RoleReceived, "t1", "p", "h", "k"); err == nil {
		t.Error("same task recorded twice")
	}
}

func TestReconcileStaleTasks(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleReceived, "run", "p", "h", "k")
	s.RecordTaskStart(RoleReceived, "fin", "p", "h", "k")
	s.RecordTaskFinish(RoleReceived, "fin", TaskDone, []byte("ok"), "", nil)

	if err := s.ReconcileStaleTasks(); err != nil {
		t.Fatal(err)
	}
	run, _ := s.Task(RoleReceived, "run")
	if run.Status != TaskLost || run.Error != "node restarted" || run.FinishedAt.IsZero() {
		t.Errorf("running row: %+v", run)
	}
	if fin, _ := s.Task(RoleReceived, "fin"); fin.Status != TaskDone {
		t.Errorf("finished row changed: %+v", fin)
	}
}

// A task sent to another node is still running there after this node restarts.
func TestReconcileLeavesSentTasksRunning(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleSent, "away", "p", "h", "k")

	if err := s.ReconcileStaleTasks(); err != nil {
		t.Fatal(err)
	}
	away, _ := s.Task(RoleSent, "away")
	if away.Status != TaskRunning || away.Error != "" || !away.FinishedAt.IsZero() {
		t.Errorf("sent row was touched: %+v", away)
	}
}

func TestPruneTasksKeepsRunningAndRecent(t *testing.T) {
	s := openTest(t)
	for _, id := range []string{"old-done", "old-running", "new-done"} {
		s.RecordTaskStart(RoleReceived, id, "p", "h", "k")
	}
	s.RecordTaskFinish(RoleReceived, "old-done", TaskDone, nil, "", nil)
	s.RecordTaskFinish(RoleReceived, "new-done", TaskDone, nil, "", nil)
	old := time.Now().Add(-10 * 24 * time.Hour).Unix()
	s.db.Exec(`UPDATE tasks SET started_at = ? WHERE task_id IN ('old-done', 'old-running')`, old)

	if _, err := s.PruneTasks(time.Now().Add(-7 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Task(RoleReceived, "old-done"); !errors.Is(err, ErrTaskNotFound) {
		t.Error("old finished row survived")
	}
	for _, id := range []string{"old-running", "new-done"} {
		if _, err := s.Task(RoleReceived, id); err != nil {
			t.Errorf("%s was pruned: %v", id, err)
		}
	}
}

// A real v1 file (transfers only, user_version 1) must upgrade in place.
func TestOpenUpgradesAV1Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "edgegrid.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(transfersSchema + `PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO transfers (direction, peer_id, peer_hostname, file_name, file_size, sha256, status, started_at)
		VALUES ('inbound', 'p', 'h', 'f', 1, '', 'done', 1)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	defer s.Close()

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM transfers`).Scan(&n); err != nil || n != 1 {
		t.Errorf("transfers after upgrade: n=%d err=%v", n, err)
	}
	if err := s.RecordTaskStart(RoleSent, "t", "p", "h", "k"); err != nil {
		t.Errorf("tasks table missing after upgrade: %v", err)
	}
}

func TestSentTaskGetsAFirstCheckAndReceivedDoesNot(t *testing.T) {
	s := openTest(t)
	before := time.Now().Add(-time.Second)
	s.RecordTaskStart(RoleSent, "out", "p", "h", "k")
	s.RecordTaskStart(RoleReceived, "in", "p", "h", "k")

	out, _ := s.Task(RoleSent, "out")
	if out.Attempts != 0 || out.NextCheckAt.Before(before.Add(FirstCheckAfter)) || out.NextCheckAt.After(time.Now().Add(FirstCheckAfter)) {
		t.Errorf("sent row: attempts %d, next check %v, want 0 and about %v from now", out.Attempts, out.NextCheckAt, FirstCheckAfter)
	}
	if in, _ := s.Task(RoleReceived, "in"); !in.NextCheckAt.IsZero() {
		t.Errorf("received row has a next check: %v", in.NextCheckAt)
	}
}

func TestDueSentTasksListsOnlyRunningSentTasksThatAreDue(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleSent, "due", "p", "h", "k")
	s.RecordTaskStart(RoleSent, "done", "p", "h", "k")
	s.RecordTaskFinish(RoleSent, "done", TaskDone, []byte("ok"), "", nil)
	s.RecordTaskStart(RoleReceived, "in", "p", "h", "k")

	if due, _ := s.DueSentTasks(time.Now(), 10); len(due) != 0 {
		t.Errorf("before the first check: %d due, want 0", len(due))
	}
	due, err := s.DueSentTasks(time.Now().Add(FirstCheckAfter+time.Minute), 10)
	if err != nil || len(due) != 1 || due[0].TaskID != "due" || due[0].PeerID != "p" {
		t.Errorf("after the first check: %+v, err %v, want only the running sent task", due, err)
	}
}

func TestDueSentTasksRespectsTheLimitAndMostOverdueFirst(t *testing.T) {
	s := openTest(t)
	later := time.Now().Add(FirstCheckAfter + time.Hour)
	for _, id := range []string{"a", "b", "c"} {
		s.RecordTaskStart(RoleSent, id, "p", "h", "k")
	}
	// push "a" later so "b" and "c" are the most overdue
	if ok, _ := s.ClaimSentTask("a", later, later.Add(-30*time.Minute)); !ok {
		t.Fatal("claim a")
	}
	due, err := s.DueSentTasks(later, 2)
	if err != nil || len(due) != 2 {
		t.Fatalf("due = %+v, err %v, want 2", due, err)
	}
	for _, r := range due {
		if r.TaskID == "a" {
			t.Errorf("the least overdue task was listed ahead of older ones: %+v", due)
		}
	}
}

func TestClaimSentTaskOnlyOncePerDueCheck(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleSent, "t", "p", "h", "k")
	now := time.Now().Add(FirstCheckAfter + time.Minute)
	next := now.Add(20 * time.Minute)

	if ok, err := s.ClaimSentTask("t", now, next); !ok || err != nil {
		t.Fatalf("first claim: %v, %v", ok, err)
	}
	if ok, _ := s.ClaimSentTask("t", now, next); ok {
		t.Error("claimed twice for the same check")
	}
	if due, _ := s.DueSentTasks(now, 10); len(due) != 0 {
		t.Errorf("a claimed task is still listed as due: %+v", due)
	}
	r, _ := s.Task(RoleSent, "t")
	if r.Attempts != 1 || r.NextCheckAt.Unix() != next.Unix() {
		t.Errorf("after claim: attempts %d, next %v, want 1 and %v", r.Attempts, r.NextCheckAt, next)
	}
}

func TestClaimSentTaskSkipsFinishedAndReceivedTasks(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleSent, "fin", "p", "h", "k")
	s.RecordTaskFinish(RoleSent, "fin", TaskDone, nil, "", nil)
	s.RecordTaskStart(RoleReceived, "in", "p", "h", "k")
	now := time.Now().Add(FirstCheckAfter + time.Minute)

	for _, id := range []string{"fin", "in", "missing"} {
		if ok, err := s.ClaimSentTask(id, now, now.Add(time.Hour)); ok || err != nil {
			t.Errorf("%s: claimed = %v, err = %v, want false", id, ok, err)
		}
	}
}

func TestDoneTaskKeepsItsFiles(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleReceived, "t1", "peer-a", "alpha", "train")
	files := []TaskFile{
		{Name: "model.pt", Size: 4200000, SHA256: "9f2c"},
		{Name: "metrics.json", Size: 812, SHA256: "a1b0"},
	}
	if err := s.RecordTaskFinish(RoleReceived, "t1", TaskDone, []byte("loss 0.03\n"), "", files); err != nil {
		t.Fatal(err)
	}
	r, err := s.Task(RoleReceived, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != TaskDone || string(r.Output) != "loss 0.03\n" || len(r.Files) != 2 || r.Files[0] != files[0] || r.Files[1] != files[1] {
		t.Errorf("got %+v", r)
	}

	// the same list comes back through the sender's query path too
	got, _ := s.ReceivedTaskFor("t1", "peer-a")
	if len(got.Files) != 2 {
		t.Errorf("ReceivedTaskFor files = %+v", got.Files)
	}
}

func TestTaskWithoutFilesHasNilFiles(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleReceived, "t1", "peer-a", "alpha", "hello")
	s.RecordTaskFinish(RoleReceived, "t1", TaskDone, []byte("hi\n"), "", nil)
	if r, _ := s.Task(RoleReceived, "t1"); r.Files != nil {
		t.Errorf("files = %+v, want nil", r.Files)
	}
	if r, _ := s.Task(RoleReceived, "t1"); r.Status != TaskDone {
		t.Errorf("status = %s", r.Status)
	}
}

func TestLongOutputKeepsTheFiles(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleReceived, "t1", "peer-a", "alpha", "train")
	s.RecordTaskFinish(RoleReceived, "t1", TaskDone, bytes.Repeat([]byte("x"), MaxTaskOutput+1), "",
		[]TaskFile{{Name: "model.pt", Size: 1, SHA256: "aa"}})
	r, _ := s.Task(RoleReceived, "t1")
	if r.Status != TaskDone || len(r.Files) != 1 {
		t.Errorf("got status %s files %+v, want done with model.pt", r.Status, r.Files)
	}
}

func TestFinishWithFilesOnMissingRow(t *testing.T) {
	s := openTest(t)
	if err := s.RecordTaskFinish(RoleReceived, "ghost", TaskDone, nil, "", []TaskFile{{Name: "a"}}); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("err = %v, want ErrTaskNotFound", err)
	}
}

func TestPruneTasksReturnsReceivedTasksWithFiles(t *testing.T) {
	s := openTest(t)
	model := []TaskFile{{Name: "model.pt", Size: 1, SHA256: "aa"}}
	s.RecordTaskStart(RoleReceived, "old-model", "p", "h", "train")
	s.RecordTaskFinish(RoleReceived, "old-model", TaskDone, nil, "", model)
	s.RecordTaskStart(RoleReceived, "old-nofiles", "p", "h", "hello")
	s.RecordTaskFinish(RoleReceived, "old-nofiles", TaskDone, nil, "", nil)
	s.RecordTaskStart(RoleSent, "old-sent", "p", "h", "train")
	s.RecordTaskFinish(RoleSent, "old-sent", TaskDone, nil, "", model)
	s.RecordTaskStart(RoleReceived, "new-model", "p", "h", "train")
	s.RecordTaskFinish(RoleReceived, "new-model", TaskDone, nil, "", model)
	old := time.Now().Add(-8 * 24 * time.Hour).Unix()
	s.db.Exec(`UPDATE tasks SET started_at = ? WHERE task_id LIKE 'old-%'`, old)

	got, err := s.PruneTasks(time.Now().Add(-7 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "old-model" {
		t.Errorf("got %v, want [old-model]", got)
	}
	if _, err := s.Task(RoleSent, "old-sent"); !errors.Is(err, ErrTaskNotFound) {
		t.Error("old sent row survived")
	}
	if _, err := s.Task(RoleReceived, "new-model"); err != nil {
		t.Errorf("new-model was pruned: %v", err)
	}
}
