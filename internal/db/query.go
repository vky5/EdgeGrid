package db

import (
	"database/sql"
	"fmt"
	"time"
)

// TransferSummary is one recorded transfer, for display.
type TransferSummary struct {
	Direction    Direction
	PeerHostname string
	FileName     string
	FileSize     int64
	Status       Status
	Error        string
	StartedAt    time.Time
	FinishedAt   time.Time // zero if still in_progress
}

// Throughput is bytes/sec over how long this transfer actually took, or 0
// for anything that never finished successfully.
func (t TransferSummary) Throughput() float64 {
	if t.Status != StatusDone || t.FinishedAt.IsZero() {
		return 0
	}
	secs := t.FinishedAt.Sub(t.StartedAt).Seconds()
	if secs <= 0 {
		return 0
	}
	return float64(t.FileSize) / secs
}

// Totals summarizes every finished transfer, split by direction. Only
// StatusDone rows count — anything else never confirmed how many bytes
// actually landed. Throughput is one rate over all bytes and all time, not
// an average of each row's own rate.
type Totals struct {
	SentBytes, ReceivedBytes           int64
	SentCount, ReceivedCount           int
	SentThroughput, ReceivedThroughput float64 // bytes/sec
}

// Totals reads Totals from the transfers table.
func (s *Store) Totals() (Totals, error) {
	rows, err := s.db.Query(
		`SELECT direction, COALESCE(SUM(file_size), 0), COUNT(*), COALESCE(SUM(finished_at - started_at), 0)
		 FROM transfers WHERE status = ? GROUP BY direction`,
		StatusDone,
	)
	if err != nil {
		return Totals{}, fmt.Errorf("db: read totals: %w", err)
	}
	defer rows.Close()

	var t Totals
	for rows.Next() {
		var dir string
		var bytes, count, secs int64
		if err := rows.Scan(&dir, &bytes, &count, &secs); err != nil {
			return Totals{}, fmt.Errorf("db: read totals: %w", err)
		}
		rate := throughput(bytes, secs)
		switch Direction(dir) {
		case Outbound:
			t.SentBytes, t.SentCount, t.SentThroughput = bytes, int(count), rate
		case Inbound:
			t.ReceivedBytes, t.ReceivedCount, t.ReceivedThroughput = bytes, int(count), rate
		}
	}
	return t, rows.Err()
}

func throughput(bytes, secs int64) float64 {
	if secs <= 0 {
		return 0
	}
	return float64(bytes) / float64(secs)
}

// RecentTransfers returns up to limit rows, newest first.
func (s *Store) RecentTransfers(limit int) ([]TransferSummary, error) {
	rows, err := s.db.Query(
		`SELECT direction, peer_hostname, file_name, file_size, status, error, started_at, finished_at
		 FROM transfers ORDER BY started_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("db: read recent transfers: %w", err)
	}
	defer rows.Close()

	var out []TransferSummary
	for rows.Next() {
		var dir, peerHostname, fileName, status string
		var fileSize, startedAt int64
		var errVal sql.NullString
		var finishedAt sql.NullInt64
		if err := rows.Scan(&dir, &peerHostname, &fileName, &fileSize, &status, &errVal, &startedAt, &finishedAt); err != nil {
			return nil, fmt.Errorf("db: read recent transfers: %w", err)
		}
		ts := TransferSummary{
			Direction:    Direction(dir),
			PeerHostname: peerHostname,
			FileName:     fileName,
			FileSize:     fileSize,
			Status:       Status(status),
			Error:        errVal.String,
			StartedAt:    time.Unix(startedAt, 0),
		}
		if finishedAt.Valid {
			ts.FinishedAt = time.Unix(finishedAt.Int64, 0)
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}
