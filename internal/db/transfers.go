package db

import (
	"database/sql"
	"fmt"
	"time"
)

// Direction is which way a transfer moved relative to this node.
type Direction string

const (
	Outbound Direction = "outbound"
	Inbound  Direction = "inbound"
)

// Status is where a transfer row stands. A stale InProgress row is a
// crash, not a transfer still running — see ReconcileStaleTransfers.
type Status string

const (
	StatusInProgress  Status = "in_progress"
	StatusDone        Status = "done"
	StatusFailed      Status = "failed"
	StatusRefused     Status = "refused"
	StatusInterrupted Status = "interrupted"
)

// RecordStart inserts a row with status in_progress and returns its id.
// sha256 starts empty — it means "confirmed", not "claimed".
func (s *Store) RecordStart(dir Direction, peerID, peerHostname, fileName string, fileSize int64) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO transfers (direction, peer_id, peer_hostname, file_name, file_size, sha256, status, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		dir, peerID, peerHostname, fileName, fileSize, "", StatusInProgress, time.Now().UTC().Unix(),
	)
	if err != nil {
		return 0, fmt.Errorf("db: record transfer start: %w", err)
	}
	return res.LastInsertId()
}

// RecordFinish updates a row's status and finished_at. sha256 is kept
// only when status is StatusDone — anything else, it's dropped.
func (s *Store) RecordFinish(id int64, status Status, errMsg, sha256 string) error {
	var errVal sql.NullString
	if errMsg != "" {
		errVal = sql.NullString{String: errMsg, Valid: true}
	}
	if status != StatusDone {
		sha256 = ""
	}
	_, err := s.db.Exec(
		`UPDATE transfers SET status = ?, error = ?, sha256 = ?, finished_at = ? WHERE id = ?`,
		status, errVal, sha256, time.Now().UTC().Unix(), id,
	)
	if err != nil {
		return fmt.Errorf("db: record transfer finish: %w", err)
	}
	return nil
}

// ReconcileStaleTransfers marks every in_progress row interrupted. Call
// once at startup — such a row predates this process, so it's a crash.
func (s *Store) ReconcileStaleTransfers() error {
	_, err := s.db.Exec(
		`UPDATE transfers SET status = ?, finished_at = ? WHERE status = ?`,
		StatusInterrupted, time.Now().UTC().Unix(), StatusInProgress,
	)
	if err != nil {
		return fmt.Errorf("db: reconcile stale transfers: %w", err)
	}
	return nil
}
