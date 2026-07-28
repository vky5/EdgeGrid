package dashboard

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/edgegrid/edgegrid/internal/tui/client"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// jobSubmittedMsg is emitted once SubmitJob succeeds, returning to the list.
type jobSubmittedMsg struct{}

type submitJobModel struct {
	client client.Client
	script textarea.Model
}

func newSubmitJobModel(c client.Client) submitJobModel {
	ta := textarea.New()
	ta.Placeholder = "training_script.py contents"
	ta.Focus()
	return submitJobModel{client: c, script: ta}
}

func (m submitJobModel) Init() tea.Cmd { return textarea.Blink }

func (m submitJobModel) Update(msg tea.Msg) (submitJobModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return backToJobsMsg{} }
		case "ctrl+s":
			_ = m.client.SubmitJob(m.script.Value(), "")
			return m, func() tea.Msg { return jobSubmittedMsg{} }
		}
	}
	var cmd tea.Cmd
	m.script, cmd = m.script.Update(msg)
	return m, cmd
}

func (m submitJobModel) View() string {
	s := style.Title.Render("Submit job") + "\n\n"
	s += m.script.View() + "\n\n"
	s += style.Help.Render("ctrl+s submit   esc cancel")
	return s
}
