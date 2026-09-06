// Package logsview is the log viewer overlay opened by the "/logs" command
// from the dashboard — reads through node.TailLog, the same function the
// plain `edgegrid logs` subcommand uses, so there's one place that knows
// where a node's logs live and how they're tailed.
package logsview

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/edgegrid/edgegrid/internal/node"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

const maxLines = 1000

// CloseMsg is emitted when the user dismisses the overlay.
type CloseMsg struct{}

type Model struct {
	viewport viewport.Model
}

// New loads the current tail of dataDir's log file via node.TailLog.
func New(dataDir string, width, height int) Model {
	vp := viewport.New(width, height)
	content, err := node.TailLog(dataDir, maxLines)
	if err != nil {
		content = style.ErrorText.Render("reading logs: " + err.Error())
	}
	vp.SetContent(content)
	vp.GotoBottom()
	return Model{viewport: vp}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
		return m, func() tea.Msg { return CloseMsg{} }
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	return style.Title.Render("Logs") + "\n\n" + m.viewport.View()
}
