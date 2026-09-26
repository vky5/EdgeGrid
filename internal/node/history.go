package node

import "github.com/edgegrid/edgegrid/internal/db"

// HistoryTotals reports total bytes sent/received and average throughput.
// A node with no history database returns a zero Totals, not an error.
func (a *Node) HistoryTotals() (db.Totals, error) {
	if a.history == nil {
		return db.Totals{}, nil
	}
	return a.history.Totals()
}

// RecentTransfers lists the most recently recorded transfers, newest first.
func (a *Node) RecentTransfers(limit int) ([]db.TransferSummary, error) {
	if a.history == nil {
		return nil, nil
	}
	return a.history.RecentTransfers(limit)
}
