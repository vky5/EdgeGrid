// Package logsview is the log viewer overlay opened by the "/logs" command
// from either onboarding or the dashboard — one screen, reused by both, so
// there's exactly one place that knows how logs get rendered in the TUI.
package logsview

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/edgegrid/edgegrid/internal/nodelog"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

const maxLines = 1000

// CloseMsg is emitted when the user dismisses the overlay.
type CloseMsg struct{}

type Model struct {
	viewport viewport.Model
}

// New loads the current tail of dataDir's log file via the same
// nodelog.Tail function `edgegrid logs` uses.
func New(dataDir string, width, height int) Model {
	vp := viewport.New(width, height)
	content, err := nodelog.Tail(dataDir, maxLines)
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
