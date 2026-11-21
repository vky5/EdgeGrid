package node

import "github.com/edgegrid/edgegrid/internal/db"

// total bytes sent and received, and the average speed
// a node with no database returns zeros, not an error
func (a *Node) HistoryTotals() (db.Totals, error) {
	if a.history == nil {
		return db.Totals{}, nil
	}
	return a.history.Totals()
}

// the latest saved transfers, newest first
func (a *Node) RecentTransfers(limit int) ([]db.TransferSummary, error) {
	if a.history == nil {
		return nil, nil
	}
	return a.history.RecentTransfers(limit)
}
