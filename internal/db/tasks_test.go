package db

import (
	"bytes"
	"database/sql"
	"errors"
	"path/filepath"
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

	if err := s.RecordTaskFinish(RoleReceived, "t1", TaskDone, []byte("hi\n"), ""); err != nil {
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
	s.RecordTaskFinish(RoleSent, "t1", TaskFailed, nil, "boom")

	r, _ := s.Task(RoleReceived, "t1")
	if r.Status != TaskRunning {
		t.Errorf("finishing the sent row touched the received row: %+v", r)
	}
}

func TestFailedTaskDropsOutputAndKeepsError(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleReceived, "t1", "p", "h", "k")
	s.RecordTaskFinish(RoleReceived, "t1", TaskFailed, []byte("partial"), "exit 3")
	r, _ := s.Task(RoleReceived, "t1")
	if r.Output != nil || r.Error != "exit 3" {
		t.Errorf("got %+v", r)
	}
}

func TestOversizedOutputBecomesAFailure(t *testing.T) {
	s := openTest(t)
	s.RecordTaskStart(RoleReceived, "t1", "p", "h", "k")
	s.RecordTaskFinish(RoleReceived, "t1", TaskDone, bytes.Repeat([]byte("x"), MaxTaskOutput+1), "")
	r, _ := s.Task(RoleReceived, "t1")
	if r.Status != TaskFailed || r.Output != nil || r.Error == "" {
		t.Errorf("got %+v, want failed with no output", r)
	}

	s.RecordTaskStart(RoleReceived, "t2", "p", "h", "k")
	s.RecordTaskFinish(RoleReceived, "t2", TaskDone, bytes.Repeat([]byte("x"), MaxTaskOutput), "")
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
	if err := s.RecordTaskFinish(RoleReceived, "ghost", TaskDone, nil, ""); !errors.Is(err, ErrTaskNotFound) {
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
	s.RecordTaskFinish(RoleReceived, "fin", TaskDone, []byte("ok"), "")

	if err := s.ReconcileStaleTasks(); err != nil {
		t.Fatal(err)
	}
	run, _ := s.Task(RoleReceived, "run")
	if run.Status != TaskInterrupted || run.Error != "node restarted" || run.FinishedAt.IsZero() {
		t.Errorf("running row: %+v", run)
	}
	if fin, _ := s.Task(RoleReceived, "fin"); fin.Status != TaskDone {
		t.Errorf("finished row changed: %+v", fin)
	}
}

func TestPruneTasksKeepsRunningAndRecent(t *testing.T) {
	s := openTest(t)
	for _, id := range []string{"old-done", "old-running", "new-done"} {
		s.RecordTaskStart(RoleReceived, id, "p", "h", "k")
	}
	s.RecordTaskFinish(RoleReceived, "old-done", TaskDone, nil, "")
	s.RecordTaskFinish(RoleReceived, "new-done", TaskDone, nil, "")
	old := time.Now().Add(-10 * 24 * time.Hour).Unix()
	s.db.Exec(`UPDATE tasks SET started_at = ? WHERE task_id IN ('old-done', 'old-running')`, old)

	if err := s.PruneTasks(time.Now().Add(-7 * 24 * time.Hour)); err != nil {
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
