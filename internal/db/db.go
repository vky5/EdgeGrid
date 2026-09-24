package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// schemaVersion is tracked in the database itself via PRAGMA user_version.
const schemaVersion = 1

// Store is this node's local database. SQLite serializes writers on its
// own; the pragmas below just make that wait instead of fail.
type Store struct {
	db *sql.DB
}

// Open creates path's parent directory if needed, opens or creates the
// database there, and brings its schema up to date.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("db: create %s: %w", filepath.Dir(path), err)
	}

	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}

	if _, err := sqlDB.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("db: enable WAL: %w", err)
	}
	if _, err := sqlDB.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("db: set busy_timeout: %w", err)
	}

	s := &Store{db: sqlDB}
	if err := s.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}
