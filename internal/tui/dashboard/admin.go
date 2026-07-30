package dashboard

import (
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/edgegrid/edgegrid/internal/tui/client"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

type adminModel struct {
	client  client.Client
	table   table.Model
	pending []client.JoinRequestSummary
	err     error
}

func newAdminModel(c client.Client) adminModel {
	m := adminModel{
		client: c,
		table: table.New(
			table.WithColumns([]table.Column{
				{Title: "NODE ID", Width: 14},
				{Title: "ROLE", Width: 10},
				{Title: "HOSTNAME", Width: 16},
				{Title: "SUBMITTED", Width: 12},
			}),
			table.WithFocused(true),
			table.WithHeight(10),
		),
	}
	return m.refresh()
}

// refresh re-fetches the pending list and rebuilds the table rows — called
// on construction and again after every approve/reject, so the list never
// shows a node that was just decided on.
func (m adminModel) refresh() adminModel {
	pending, err := m.client.ListPendingJoins()
	m.err = err
	m.pending = pending
	rows := make([]table.Row, 0, len(pending))
	for _, p := range pending {
		rows = append(rows, table.Row{p.NodeID, p.Role, p.Hostname, p.Submitted})
	}
	m.table.SetRows(rows)
	m.table.SetCursor(0)
	return m
}

func (m adminModel) Init() tea.Cmd { return nil }

func (m adminModel) Update(msg tea.Msg) (adminModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && len(m.pending) > 0 {
		row := m.table.Cursor()
		switch key.String() {
		case "a":
			if err := m.client.ApproveJoin(m.pending[row].NodeID); err != nil {
				m.err = err
				return m, nil
			}
			return m.refresh(), nil
		case "r":
			if err := m.client.RejectJoin(m.pending[row].NodeID); err != nil {
				m.err = err
				return m, nil
			}
			return m.refresh(), nil
		}
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m adminModel) View() string {
	if m.err != nil {
		return m.table.View() + "\n\n" + style.ErrorText.Render(m.err.Error())
	}
	return m.table.View()
}
