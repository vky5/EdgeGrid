package dashboard

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/edgegrid/edgegrid/internal/tui/client"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// backToJobsMsg is emitted to leave the detail view.
type backToJobsMsg struct{}

// jobDetailMsg is emitted when the user opens a job's detail/logs view.
type jobDetailMsg struct {
	jobID string
}

// jobLogsFetchedMsg carries JobLogs' result back from the background fetch
// kicked off by jobDetailModel.Init(). jobID guards against a stale
// response landing after the user has already moved on to a different job.
type jobLogsFetchedMsg struct {
	jobID string
	logs  string
	err   error
}

func fetchJobLogsCmd(c client.Client, jobID string) tea.Cmd {
	return func() tea.Msg {
		logs, err := c.JobLogs(jobID)
		return jobLogsFetchedMsg{jobID: jobID, logs: logs, err: err}
	}
}

type jobDetailModel struct {
	jobID    string
	client   client.Client
	viewport viewport.Model
}

// newJobDetailModel does not fetch logs itself — JobLogs can block for up
// to several seconds against a still-running job (see its doc comment),
// and this constructor runs synchronously inside Update(). Fetching here
// would freeze the whole TUI for that long. Init() below kicks the fetch
// off as a tea.Cmd instead, off the UI goroutine.
func newJobDetailModel(c client.Client, jobID string) jobDetailModel {
	vp := viewport.New(60, 12)
	vp.SetContent("Loading logs…")
	return jobDetailModel{jobID: jobID, client: c, viewport: vp}
}

func (m jobDetailModel) Init() tea.Cmd {
	return fetchJobLogsCmd(m.client, m.jobID)
}

func (m jobDetailModel) Update(msg tea.Msg) (jobDetailModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "esc" {
			return m, func() tea.Msg { return backToJobsMsg{} }
		}
	case jobLogsFetchedMsg:
		if msg.jobID != m.jobID {
			return m, nil // stale response from a previously-viewed job
		}
		if msg.err != nil {
			m.viewport.SetContent(style.ErrorText.Render(msg.err.Error()))
		} else {
			m.viewport.SetContent(msg.logs)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m jobDetailModel) View() string {
	s := style.Title.Render("Job "+m.jobID) + "\n\n"
	s += m.viewport.View() + "\n\n"
	s += style.Help.Render("esc back")
	return s
}
