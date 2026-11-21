package db

import (
	"database/sql"
	"fmt"
	"time"
)

// one saved transfer, for the TUI to show
//
//	// alpha5-297b sent bravo a 10 MiB file in 2s
//	{Direction: "outbound", PeerHostname: "bravo", FileName: "report.pdf", FileSize: 10485760,
//		Status: "done", StartedAt: 21:30:00, FinishedAt: 21:30:02}            // Throughput() 5242880
//	// old-laptop tried to send us a file without being allowed
//	{Direction: "inbound", PeerHostname: "old-laptop", FileName: "movie.mkv", FileSize: 2147483648,
//		Status: "refused", Error: "blob: refused: this node is not accepting files from you"} // Throughput() 0
//	// alpha5-297b was receiving when it crashed; found at the next start
//	{Direction: "inbound", PeerHostname: "alpha5-a720", FileName: "a.txt", FileSize: 4096,
//		Status: "interrupted"}
type TransferSummary struct {
	Direction    Direction
	PeerHostname string // e.g. alpha5-297b
	FileName     string
	FileSize     int64
	Status       Status
	Error        string
	StartedAt    time.Time
	FinishedAt   time.Time // zero if still in_progress
}

// speed in bytes per second over how long the transfer took, e.g. 100 bytes in 10s gives 10
// 0 for anything that didn't finish successfully
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

// bytes and speed for finished transfers, split by direction
// only successful ones count, since others never confirmed how many bytes landed
// speed is all bytes over all time, not an average of each transfer's speed
//
//	// done outbound rows: 10 MiB in 2s and 2 MiB in 2s; done inbound: none; one refused inbound row
//	db.Totals{
//		SentBytes: 12582912, SentCount: 2, SentThroughput: 3145728,   // 12 MiB over 4s, not the average of 5 and 1 MiB/s
//		ReceivedBytes: 0, ReceivedCount: 0, ReceivedThroughput: 0,  // the refused row doesn't count
//	}
type Totals struct {
	SentBytes, ReceivedBytes           int64
	SentCount, ReceivedCount           int
	SentThroughput, ReceivedThroughput float64 // bytes/sec
}

// adds up every successful transfer into Totals
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

// the latest transfers, newest first, up to limit
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
