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

// migrate brings a fresh or existing database up to schemaVersion.
// A fresh file reads back user_version 0, so that means "apply everything."
func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("db: read schema version: %w", err)
	}

	switch {
	case version == schemaVersion:
		return nil
	case version > schemaVersion:
		return fmt.Errorf("db: schema version %d is newer than this build supports (%d)", version, schemaVersion)
	case version != 0:
		// No migration chain yet — only 0 -> 1 exists.
		return fmt.Errorf("db: no migration path from schema version %d", version)
	}

	if _, err := s.db.Exec(transfersSchema); err != nil {
		return fmt.Errorf("db: create schema: %w", err)
	}
	if _, err := s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return fmt.Errorf("db: set schema version: %w", err)
	}
	return nil
}
