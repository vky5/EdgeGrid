package dashboard

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// historyRefreshInterval is how often the History tab re-reads the
// transfer database.
const historyRefreshInterval = 5 * time.Second

// historyLimit bounds how many recent rows are shown — no scrolling yet.
const historyLimit = 20

// TotalsFunc reports total bytes sent/received and average throughput.
// node.Node.HistoryTotals satisfies it.
type TotalsFunc func() (db.Totals, error)

// RecentTransfersFunc lists the most recent recorded transfers, newest
// first. node.Node.RecentTransfers satisfies it.
type RecentTransfersFunc func(limit int) ([]db.TransferSummary, error)

// HistoryFuncs connects the History tab to this node's transfer database.
type HistoryFuncs struct {
	Totals TotalsFunc
	Recent RecentTransfersFunc
}

type historyRefreshMsg struct{}

func historyRefreshCmd() tea.Cmd {
	return tea.Tick(historyRefreshInterval, func(time.Time) tea.Msg { return historyRefreshMsg{} })
}

type historyModel struct {
	funcs  HistoryFuncs
	totals db.Totals
	recent []db.TransferSummary
	err    error

	width, height int
}

func newHistoryModel(f HistoryFuncs) historyModel {
	return historyModel{funcs: f}
}

func (m historyModel) WithSize(w, h int) historyModel {
	m.width, m.height = w, h
	return m
}

func (m historyModel) refresh() historyModel {
	if m.funcs.Totals != nil {
		if t, err := m.funcs.Totals(); err != nil {
			m.err = err
		} else {
			m.totals, m.err = t, nil
		}
	}
	if m.funcs.Recent != nil {
		if r, err := m.funcs.Recent(historyLimit); err == nil {
			m.recent = r
		}
	}
	return m
}

func (m historyModel) Init() tea.Cmd { return historyRefreshCmd() }

func (m historyModel) Update(msg tea.Msg) (historyModel, tea.Cmd) {
	if _, ok := msg.(historyRefreshMsg); ok {
		return m.refresh(), historyRefreshCmd()
	}
	return m, nil
}

func (m historyModel) View() string {
	if m.funcs.Totals == nil && m.funcs.Recent == nil {
		return lipgloss.NewStyle().Foreground(style.Muted).Render("history is not available on this node")
	}
	if m.err != nil {
		return style.ErrorText.Render("history: " + m.err.Error())
	}

	muted := lipgloss.NewStyle().Foreground(style.Muted)
	summary := fmt.Sprintf("sent %s (%d, avg %s/s)    received %s (%d, avg %s/s)",
		humanBytes(m.totals.SentBytes), m.totals.SentCount, humanBytes(int64(m.totals.SentThroughput)),
		humanBytes(m.totals.ReceivedBytes), m.totals.ReceivedCount, humanBytes(int64(m.totals.ReceivedThroughput)),
	)

	lines := []string{style.Title.Render("Transfer history"), summary, ""}

	if len(m.recent) == 0 {
		lines = append(lines, muted.Render("no transfers recorded yet"))
		return strings.Join(lines, "\n")
	}

	for _, t := range m.recent {
		arrow := "→"
		if t.Direction == db.Inbound {
			arrow = "←"
		}

		var status string
		switch t.Status {
		case db.StatusDone:
			status = lipgloss.NewStyle().Foreground(greenColor).Render("done  " + humanBytes(int64(t.Throughput())) + "/s")
		case db.StatusInProgress:
			status = lipgloss.NewStyle().Foreground(style.Accent).Render("in progress")
		default:
			status = lipgloss.NewStyle().Foreground(style.Danger).Render(string(t.Status))
		}

		lines = append(lines, fmt.Sprintf("  %s %-16s %-20s %8s  %s",
			arrow, truncate(t.PeerHostname, 16), truncate(t.FileName, 20), humanBytes(t.FileSize), status))
	}

	return strings.Join(lines, "\n")
}
