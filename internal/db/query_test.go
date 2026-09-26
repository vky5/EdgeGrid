package db

import (
	"testing"
	"time"
)

// seedRow inserts a row directly, bypassing RecordStart/RecordFinish, so
// the test controls started_at/finished_at exactly.
func seedRow(t *testing.T, s *Store, dir Direction, peer string, size int64, status Status, startedAt, finishedAt int64) {
	t.Helper()
	_, err := s.db.Exec(
		`INSERT INTO transfers (direction, peer_id, peer_hostname, file_name, file_size, sha256, status, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		dir, peer, peer, "f.bin", size, "", status, startedAt, finishedAt,
	)
	if err != nil {
		t.Fatalf("seedRow: %v", err)
	}
}

func TestTotalsOnlyCountsDoneRows(t *testing.T) {
	s := openTest(t)
	seedRow(t, s, Outbound, "alpha", 100, StatusDone, 0, 10)   // 10 bytes/sec
	seedRow(t, s, Outbound, "alpha", 900, StatusFailed, 0, 10) // must not count
	seedRow(t, s, Inbound, "bravo", 200, StatusDone, 0, 20)    // 10 bytes/sec

	totals, err := s.Totals()
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	if totals.SentBytes != 100 || totals.SentCount != 1 {
		t.Errorf("sent = %d bytes, %d count; want 100, 1", totals.SentBytes, totals.SentCount)
	}
	if totals.SentThroughput != 10 {
		t.Errorf("sent throughput = %v, want 10", totals.SentThroughput)
	}
	if totals.ReceivedBytes != 200 || totals.ReceivedCount != 1 {
		t.Errorf("received = %d bytes, %d count; want 200, 1", totals.ReceivedBytes, totals.ReceivedCount)
	}
}

func TestRecentTransfersOrdersNewestFirstAndRespectsLimit(t *testing.T) {
	s := openTest(t)
	seedRow(t, s, Outbound, "oldest", 1, StatusDone, 100, 110)
	seedRow(t, s, Outbound, "middle", 1, StatusDone, 200, 210)
	seedRow(t, s, Outbound, "newest", 1, StatusDone, 300, 310)

	got, err := s.RecentTransfers(2)
	if err != nil {
		t.Fatalf("RecentTransfers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if got[0].PeerHostname != "newest" || got[1].PeerHostname != "middle" {
		t.Errorf("order = %q, %q; want newest, middle", got[0].PeerHostname, got[1].PeerHostname)
	}
}

func TestTransferSummaryThroughput(t *testing.T) {
	done := TransferSummary{
		Status: StatusDone, FileSize: 100,
		StartedAt: time.Unix(0, 0), FinishedAt: time.Unix(10, 0),
	}
	if got := done.Throughput(); got != 10 {
		t.Errorf("Throughput = %v, want 10", got)
	}

	unfinished := TransferSummary{Status: StatusInProgress, FileSize: 100}
	if got := unfinished.Throughput(); got != 0 {
		t.Errorf("Throughput of an in-progress row = %v, want 0", got)
	}

	failed := TransferSummary{
		Status: StatusFailed, FileSize: 100,
		StartedAt: time.Unix(0, 0), FinishedAt: time.Unix(10, 0),
	}
	if got := failed.Throughput(); got != 0 {
		t.Errorf("Throughput of a failed row = %v, want 0", got)
	}
}
