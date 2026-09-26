package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesSchema(t *testing.T) {
	s := openTest(t)
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != schemaVersion {
		t.Errorf("user_version = %d, want %d", version, schemaVersion)
	}
}

// Reopening an existing database must not try to run CREATE TABLE again.
func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	s2.Close()
}

func TestRecordStartAndFinishRoundTrip(t *testing.T) {
	s := openTest(t)

	id, err := s.RecordStart(Outbound, "peer-1", "alpha", "payload.bin", 1024)
	if err != nil {
		t.Fatalf("RecordStart: %v", err)
	}

	var status, sha256 string
	var finishedAt sql.NullInt64
	row := s.db.QueryRow(`SELECT status, sha256, finished_at FROM transfers WHERE id = ?`, id)
	if err := row.Scan(&status, &sha256, &finishedAt); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status != string(StatusInProgress) {
		t.Errorf("status = %q, want %q", status, StatusInProgress)
	}
	if sha256 != "" {
		t.Errorf("sha256 = %q, want empty before RecordFinish", sha256)
	}
	if finishedAt.Valid {
		t.Error("finished_at set before RecordFinish")
	}

	if err := s.RecordFinish(id, StatusDone, "", "deadbeef"); err != nil {
		t.Fatalf("RecordFinish: %v", err)
	}

	var errVal sql.NullString
	row = s.db.QueryRow(`SELECT status, sha256, error, finished_at FROM transfers WHERE id = ?`, id)
	if err := row.Scan(&status, &sha256, &errVal, &finishedAt); err != nil {
		t.Fatalf("read row after finish: %v", err)
	}
	if status != string(StatusDone) {
		t.Errorf("status = %q, want %q", status, StatusDone)
	}
	if sha256 != "deadbeef" {
		t.Errorf("sha256 = %q, want %q", sha256, "deadbeef")
	}
	if errVal.Valid {
		t.Errorf("error = %q, want NULL for a successful transfer", errVal.String)
	}
	if !finishedAt.Valid {
		t.Error("finished_at not set after RecordFinish")
	}
}

func TestRecordFinishStoresError(t *testing.T) {
	s := openTest(t)
	id, _ := s.RecordStart(Inbound, "peer-2", "bravo", "movie.mkv", 2048)

	if err := s.RecordFinish(id, StatusFailed, "connection reset", ""); err != nil {
		t.Fatalf("RecordFinish: %v", err)
	}

	var errVal sql.NullString
	row := s.db.QueryRow(`SELECT error FROM transfers WHERE id = ?`, id)
	if err := row.Scan(&errVal); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if !errVal.Valid || errVal.String != "connection reset" {
		t.Errorf("error = %v, want \"connection reset\"", errVal)
	}
}

// A hash is only meaningful once verifyWhole has actually run, so
// RecordFinish must drop it for anything but a clean success — even if a
// caller passes one by mistake.
func TestRecordFinishDropsHashWhenNotDone(t *testing.T) {
	s := openTest(t)
	id, _ := s.RecordStart(Outbound, "peer-1", "alpha", "a.bin", 10)

	if err := s.RecordFinish(id, StatusFailed, "boom", "some-hash-that-was-never-checked"); err != nil {
		t.Fatalf("RecordFinish: %v", err)
	}

	var sha256 string
	if err := s.db.QueryRow(`SELECT sha256 FROM transfers WHERE id = ?`, id).Scan(&sha256); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if sha256 != "" {
		t.Errorf("sha256 = %q, want empty for a failed transfer", sha256)
	}
}

// The CHECK constraints are the real guarantee that a typo can't corrupt
// status or direction — this proves SQLite is actually enforcing them.
func TestInvalidStatusIsRejected(t *testing.T) {
	s := openTest(t)
	_, err := s.db.Exec(
		`INSERT INTO transfers (direction, peer_id, peer_hostname, file_name, file_size, sha256, status, started_at)
		 VALUES ('outbound', 'p', 'h', 'f', 1, 's', 'not_a_real_status', 0)`,
	)
	if err == nil {
		t.Error("insert with an invalid status succeeded")
	}
}

func TestReconcileStaleTransfersInterruptsInProgressRows(t *testing.T) {
	s := openTest(t)
	staleID, _ := s.RecordStart(Outbound, "peer-1", "alpha", "a.bin", 10)
	doneID, _ := s.RecordStart(Inbound, "peer-2", "bravo", "b.bin", 20)
	if err := s.RecordFinish(doneID, StatusDone, "", "bbbb"); err != nil {
		t.Fatalf("RecordFinish: %v", err)
	}

	if err := s.ReconcileStaleTransfers(); err != nil {
		t.Fatalf("ReconcileStaleTransfers: %v", err)
	}

	var status string
	if err := s.db.QueryRow(`SELECT status FROM transfers WHERE id = ?`, staleID).Scan(&status); err != nil {
		t.Fatalf("read stale row: %v", err)
	}
	if status != string(StatusInterrupted) {
		t.Errorf("stale row status = %q, want %q", status, StatusInterrupted)
	}

	if err := s.db.QueryRow(`SELECT status FROM transfers WHERE id = ?`, doneID).Scan(&status); err != nil {
		t.Fatalf("read done row: %v", err)
	}
	if status != string(StatusDone) {
		t.Errorf("already-done row status = %q, want unchanged %q", status, StatusDone)
	}
}
