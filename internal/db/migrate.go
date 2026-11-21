package db

import "fmt"

// table layout version 1: one row for every file this node sent or received
const transfersSchema = `
CREATE TABLE transfers (
	id            INTEGER PRIMARY KEY,
	direction     TEXT    NOT NULL CHECK (direction IN ('outbound', 'inbound')),
	peer_id       TEXT    NOT NULL,
	peer_hostname TEXT    NOT NULL,
	file_name     TEXT    NOT NULL,
	file_size     INTEGER NOT NULL,
	sha256        TEXT    NOT NULL,
	status        TEXT    NOT NULL CHECK (status IN ('in_progress', 'done', 'failed', 'refused', 'interrupted')),
	error         TEXT,
	started_at    INTEGER NOT NULL,
	finished_at   INTEGER
);
CREATE INDEX idx_transfers_peer_id ON transfers(peer_id);
CREATE INDEX idx_transfers_status  ON transfers(status);
`

// table layout version 2: one row per task, on both ends
// role is "sent" for tasks this node sent, "received" for tasks it was asked to run
const tasksSchema = `
CREATE TABLE tasks (
	task_id       TEXT    NOT NULL,
	role          TEXT    NOT NULL CHECK (role IN ('sent', 'received')),
	peer_id       TEXT    NOT NULL,
	peer_hostname TEXT    NOT NULL,
	kind          TEXT    NOT NULL,
	status        TEXT    NOT NULL CHECK (status IN ('running', 'done', 'failed', 'lost')),
	output        BLOB,
	error         TEXT,
	started_at    INTEGER NOT NULL,
	finished_at   INTEGER,
	PRIMARY KEY (task_id, role)
);
CREATE INDEX idx_tasks_started_at ON tasks(started_at);
`

// each step upgrades the tables one version, e.g. step 0 goes from 0 to 1
var migrations = []string{transfersSchema, tasksSchema}

// brings a new or existing database up to the current table layout
// a new file reads as version 0, so every step runs
func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("db: read schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("db: schema version %d is newer than this build supports (%d)", version, schemaVersion)
	}

	for ; version < schemaVersion; version++ {
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("db: migrate from %d: %w", version, err)
		}
		if _, err := tx.Exec(migrations[version]); err != nil {
			tx.Rollback()
			return fmt.Errorf("db: migrate %d -> %d: %w", version, version+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, version+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("db: set schema version: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("db: migrate %d -> %d: %w", version, version+1, err)
		}
	}
	return nil
}
