package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// the table layout version this code expects, saved inside the database file
const schemaVersion = 2

// this node's local database of transfers and tasks
// SQLite lets one writer in at a time; the settings below make others wait instead of fail
type Store struct {
	db *sql.DB
}

// opens the database file, creating it and its folder if needed, and updates its tables
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

// closes the database file
func (s *Store) Close() error {
	return s.db.Close()
}
