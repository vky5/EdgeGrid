package dashboard

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/edgegrid/edgegrid/internal/db"
)

func TestHistoryViewShowsNotAvailableWithNoFuncs(t *testing.T) {
	m := newHistoryModel(HistoryFuncs{})
	view := stripANSI(m.View())
	if !strings.Contains(view, "not available") {
		t.Errorf("view = %q", view)
	}
}

func TestHistoryViewShowsTotalsAndRecentRows(t *testing.T) {
	f := HistoryFuncs{
		Totals: func() (db.Totals, error) {
			return db.Totals{
				SentBytes: 10 << 20, SentCount: 2, SentThroughput: 1 << 20,
				ReceivedBytes: 5 << 20, ReceivedCount: 1, ReceivedThroughput: 2 << 20,
			}, nil
		},
		Recent: func(limit int) ([]db.TransferSummary, error) {
			return []db.TransferSummary{
				{Direction: db.Outbound, PeerHostname: "alpha", FileName: "movie.mkv", FileSize: 10 << 20,
					Status: db.StatusDone, StartedAt: time.Now(), FinishedAt: time.Now().Add(time.Second)},
				{Direction: db.Inbound, PeerHostname: "bravo", FileName: "doc.pdf", FileSize: 1 << 20,
					Status: db.StatusRefused},
			}, nil
		},
	}
	m := newHistoryModel(f).refresh()
	view := stripANSI(m.View())

	for _, want := range []string{"alpha", "bravo", "movie.mkv", "doc.pdf", "refused"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestHistoryViewShowsError(t *testing.T) {
	f := HistoryFuncs{Totals: func() (db.Totals, error) { return db.Totals{}, errors.New("boom") }}
	m := newHistoryModel(f).refresh()
	view := stripANSI(m.View())
	if !strings.Contains(view, "boom") {
		t.Errorf("view = %q", view)
	}
}

func TestHistoryViewShowsEmptyState(t *testing.T) {
	f := HistoryFuncs{
		Totals: func() (db.Totals, error) { return db.Totals{}, nil },
		Recent: func(limit int) ([]db.TransferSummary, error) { return nil, nil },
	}
	m := newHistoryModel(f).refresh()
	view := stripANSI(m.View())
	if !strings.Contains(view, "no transfers recorded") {
		t.Errorf("view = %q", view)
	}
}

// The History tab's own tick must keep rescheduling even while another tab
// is active, the same gotcha peersRefreshMsg already has to work around.
func TestHistoryRefreshSurvivesBeingOnAnotherTab(t *testing.T) {
	calls := 0
	d := New("node", "1.2.3.4", t.TempDir(), nil, nil, nil, nil).WithHistory(HistoryFuncs{
		Totals: func() (db.Totals, error) { calls++; return db.Totals{}, nil },
	})
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab}) // Overview -> Peers
	before := calls
	d, cmd := d.Update(historyRefreshMsg{})
	if calls != before+1 {
		t.Errorf("historyRefreshMsg was not routed to History while on another tab")
	}
	if cmd == nil {
		t.Error("historyRefreshMsg did not reschedule itself")
	}
}
