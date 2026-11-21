package db

import (
	"database/sql"
	"fmt"
	"time"
)

// which way a transfer went, from this node's side
type Direction string

const (
	Outbound Direction = "outbound"
	Inbound  Direction = "inbound"
)

// where a transfer stands, e.g. in_progress, done, failed
// an in_progress row left over from before a restart means a crash, not a running transfer
type Status string

const (
	StatusInProgress  Status = "in_progress"
	StatusDone        Status = "done"
	StatusFailed      Status = "failed"
	StatusRefused     Status = "refused"
	StatusInterrupted Status = "interrupted"
)

// adds a row for a transfer that just began and returns its id
// the hash starts empty, since a hash means the file was checked, not just promised
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

// saves how a transfer ended; the hash is kept only if it succeeded
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

// marks every in_progress transfer as interrupted; call once at startup
// such a row is from before this process started, so it was a crash
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
