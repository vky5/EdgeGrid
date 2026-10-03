package db

import "fmt"

// transfersSchema is schema version 1: one table for every send and
// receive this node has taken part in.
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

// tasksSchema is schema version 2: one row per task, on both ends. role says
// which end this node was — "sent" rows track tasks it dispatched, "received"
// rows tasks it was asked to run.
const tasksSchema = `
CREATE TABLE tasks (
	task_id       TEXT    NOT NULL,
	role          TEXT    NOT NULL CHECK (role IN ('sent', 'received')),
	peer_id       TEXT    NOT NULL,
	peer_hostname TEXT    NOT NULL,
	kind          TEXT    NOT NULL,
	status        TEXT    NOT NULL CHECK (status IN ('running', 'done', 'failed', 'interrupted')),
	output        BLOB,
	error         TEXT,
	started_at    INTEGER NOT NULL,
	finished_at   INTEGER,
	PRIMARY KEY (task_id, role)
);
CREATE INDEX idx_tasks_started_at ON tasks(started_at);
`

// migrations[i] takes the schema from version i to i+1.
var migrations = []string{transfersSchema, tasksSchema}

// migrate brings a fresh or existing database up to schemaVersion.
// A fresh file reads back user_version 0, so that means "apply everything."
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
