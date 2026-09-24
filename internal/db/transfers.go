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

// Status is where a transfer row stands. InProgress rows still open at
// startup are stale — a crash, not a transfer still running — and get
// reconciled to Interrupted before anything reads totals.
type Status string

const (
	StatusInProgress  Status = "in_progress"
	StatusDone        Status = "done"
	StatusFailed      Status = "failed"
	StatusRefused     Status = "refused"
	StatusInterrupted Status = "interrupted"
)

// RecordStart inserts a new transfer row with status in_progress and
// returns its id, for a later RecordFinish to update.
func (s *Store) RecordStart(dir Direction, peerID, peerHostname, fileName string, fileSize int64, sha256 string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO transfers (direction, peer_id, peer_hostname, file_name, file_size, sha256, status, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		dir, peerID, peerHostname, fileName, fileSize, sha256, StatusInProgress, time.Now().UTC().Unix(),
	)
	if err != nil {
		return 0, fmt.Errorf("db: record transfer start: %w", err)
	}
	return res.LastInsertId()
}

// RecordFinish marks a transfer row done, setting finished_at to now.
// errMsg is stored only when non-empty.
func (s *Store) RecordFinish(id int64, status Status, errMsg string) error {
	var errVal sql.NullString
	if errMsg != "" {
		errVal = sql.NullString{String: errMsg, Valid: true}
	}
	_, err := s.db.Exec(
		`UPDATE transfers SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
		status, errVal, time.Now().UTC().Unix(), id,
	)
	if err != nil {
		return fmt.Errorf("db: record transfer finish: %w", err)
	}
	return nil
}

// ReconcileStaleTransfers marks every in_progress row as interrupted. Call
// once at startup: an in_progress row that predates this process is a
// crash, not a transfer still running.
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
