package dashboard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/tui/client"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

var statusStyle = map[string]lipgloss.Style{
	"running":   lipgloss.NewStyle().Foreground(style.Accent).Bold(true),
	"completed": lipgloss.NewStyle().Foreground(greenColor).Bold(true),
	"failed":    lipgloss.NewStyle().Foreground(style.Danger).Bold(true),
	"pending":   lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true),
}

func renderJobStatus(s string) string {
	sLower := strings.ToLower(s)
	if st, ok := statusStyle[sLower]; ok {
		return st.Render(s)
	}
	return style.Help.Render(s)
}

// newJobMsg is emitted when the user opens the submit-job form.
type newJobMsg struct{}

type jobsListModel struct {
	client        client.Client
	table         table.Model
	jobs          []client.JobSummary
	width, height int
	frame         int
	err           error

	selectedJobID string
	viewport      viewport.Model
	logsLoading   bool
}

func newJobsListModel(c client.Client) jobsListModel {
	m := jobsListModel{
		client: c,
		table: table.New(
			table.WithColumns([]table.Column{
				{Title: "ID", Width: 14},
				{Title: "STATUS", Width: 10},
				{Title: "WORKER", Width: 26},
			}),
			table.WithFocused(true),
			table.WithHeight(10),
		),
		viewport: viewport.New(30, 8),
	}
	// Default Selected is only bold pink text — invisible on a multi-row list.
	// Use Accent blue background so the cursor row is obvious.
	st := table.DefaultStyles()
	st.Header = st.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(style.Muted).
		BorderBottom(true).
		Bold(true).
		Foreground(style.Accent)
	st.Selected = lipgloss.NewStyle().
		Foreground(lipgloss.Color("15")).
		Background(style.Accent).
		Bold(true)
	m.table.SetStyles(st)
	m.table.Focus()
	m.viewport.SetContent("Select a job")
	return m.refresh()
}

func (m jobsListModel) refresh() jobsListModel {
	cursor := m.table.Cursor()
	jobs, err := m.client.ListJobs()
	m.err = err
	m.jobs = jobs
	rows := make([]table.Row, 0, len(jobs))
	for _, j := range jobs {
		rows = append(rows, table.Row{j.ID, j.Status, j.Worker})
	}
	m.table.SetRows(rows)
	if len(rows) > 0 {
		m.table.SetCursor(min(cursor, len(rows)-1))
	}
	m.table.Focus() // keep selection styles after SetRows / poll refresh
	return m
}

func (m jobsListModel) WithSize(width, height int) jobsListModel {
	m.width, m.height = width, height
	m.table.SetWidth(workersListWidth)
	m.table.SetHeight(max(height-2, 3))

	remaining := width - workersListWidth - 2
	wMid := int(float64(remaining) * 0.55)
	m.viewport.Width = wMid - 4
	m.viewport.Height = max(height-13, 3)
	return m
}

func (m *jobsListModel) Init() tea.Cmd {
	var cmds []tea.Cmd
	cmds = append(cmds, dashboardAnimTickCmd())
	if w, ok := m.selected(); ok {
		m.selectedJobID = w.ID
		m.viewport.SetContent("Loading logs…")
		m.logsLoading = true
		cmds = append(cmds, fetchJobLogsCmd(m.client, w.ID))
	}
	return tea.Batch(cmds...)
}

func (m jobsListModel) selected() (client.JobSummary, bool) {
	i := m.table.Cursor()
	if i < 0 || i >= len(m.jobs) {
		return client.JobSummary{}, false
	}
	return m.jobs[i], true
}

func (m jobsListModel) Update(msg tea.Msg) (jobsListModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "n":
			return m, func() tea.Msg { return newJobMsg{} }
		case "x":
			if len(m.jobs) > 0 {
				row := m.table.Cursor()
				if err := m.client.CancelJob(m.jobs[row].ID); err != nil {
					m.err = err
					return m, nil
				}
				m = m.refresh()
				if w, ok := m.selected(); ok {
					m.selectedJobID = w.ID
					m.viewport.SetContent("Loading logs…")
					m.logsLoading = true
					cmds = append(cmds, fetchJobLogsCmd(m.client, w.ID))
				}
			}
		case "pgup", "ctrl+u", "pgdn", "ctrl+d":
			var vpCmd tea.Cmd
			m.viewport, vpCmd = m.viewport.Update(msg)
			cmds = append(cmds, vpCmd)
			return m, tea.Batch(cmds...)
		}
	case jobLogsFetchedMsg:
		if msg.jobID == m.selectedJobID {
			m.logsLoading = false
			if msg.err != nil {
				m.viewport.SetContent(style.ErrorText.Render(msg.err.Error()))
			} else {
				m.viewport.SetContent(msg.logs)
			}
		}
	case dashboardAnimTickMsg:
		m.frame++
		cmds = append(cmds, dashboardAnimTickCmd())
		// Re-fetch logs every ~2s while a job is selected so RUNNING jobs
		// fill in as lines arrive (single fetch on select was a one-shot).
		if m.frame%8 == 0 && m.selectedJobID != "" && !m.logsLoading {
			m.logsLoading = true
			cmds = append(cmds, fetchJobLogsCmd(m.client, m.selectedJobID))
		}
	}

	var tableCmd tea.Cmd
	m.table, tableCmd = m.table.Update(msg)
	cmds = append(cmds, tableCmd)

	if w, ok := m.selected(); ok {
		if w.ID != m.selectedJobID {
			m.selectedJobID = w.ID
			m.viewport.SetContent("Loading logs…")
			m.logsLoading = true
			cmds = append(cmds, fetchJobLogsCmd(m.client, w.ID))
		}
	} else {
		m.selectedJobID = ""
		m.viewport.SetContent("—")
	}

	return m, tea.Batch(cmds...)
}

func (m jobsListModel) View() string {
	wTotal := m.width
	if wTotal <= 0 {
		wTotal = 120
	}

	hTarget := m.height
	if hTarget < 12 {
		hTarget = 12
	}

	if m.err != nil {
		return lipgloss.Place(wTotal, hTarget, lipgloss.Center, lipgloss.Center, style.ErrorText.Render(m.err.Error()))
	}

	if len(m.jobs) == 0 {
		msg := "No jobs submitted to the cluster yet.\n\n" +
			"Press " + style.Selected.Render("N") + " to submit a new training job."
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(style.Muted).
			Padding(2, 4).
			Width(50).
			Align(lipgloss.Center).
			Render(msg)
		return lipgloss.Place(wTotal, hTarget, lipgloss.Center, lipgloss.Center, box)
	}

	left := m.table.View()

	remaining := wTotal - workersListWidth - 2
	if remaining < 40 {
		remaining = 40
	}
	wMid := int(float64(remaining) * 0.55)
	wRight := remaining - wMid

	var divLines []string
	for i := 0; i < hTarget; i++ {
		divLines = append(divLines, "│")
	}
	divider := lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Join(divLines, "\n"))

	var midContent, rightContent string

	if len(m.jobs) == 0 {
		midContent = lipgloss.Place(wMid, hTarget, lipgloss.Center, lipgloss.Center, style.Help.Render("—"))
		rightContent = lipgloss.Place(wRight, hTarget, lipgloss.Center, lipgloss.Center, style.Help.Render("—"))
	} else if w, ok := m.selected(); !ok {
		midContent = lipgloss.Place(wMid, hTarget, lipgloss.Center, lipgloss.Center, style.Help.Render("Select a job"))
		rightContent = lipgloss.Place(wRight, hTarget, lipgloss.Center, lipgloss.Center, style.Help.Render("Select a job"))
	} else {
		// Mid column: Job details + logs viewport
		var midLines []string
		midLines = append(midLines, style.Title.Render("Job Details"), "")
		midLines = append(midLines, contextLine("ID", w.ID))
		midLines = append(midLines, contextLine("Status", renderJobStatus(w.Status)))
		midLines = append(midLines, contextLine("Worker", w.Worker))
		midLines = append(midLines, contextLine("Time", w.Submitted))

		sepWidth := max(wMid-4, 10)
		midLines = append(midLines, "", style.Help.Render(strings.Repeat("┄", sepWidth)), "")
		midLines = append(midLines, style.Title.Render("Job Logs"), "")
		midLines = append(midLines, m.viewport.View())

		midContent = lipgloss.NewStyle().
			Width(wMid).
			Padding(1, 2).
			Render(lipgloss.JoinVertical(lipgloss.Left, midLines...))

		// Right column: Job distribution pipeline topology diagram
		var rightLines []string
		rightLines = append(rightLines, style.Title.Render("Execution Flow"), "")

		queueBox := lipgloss.NewStyle().Foreground(style.Accent).Bold(true).Render(
			`  ┌─────────┐
 /         /│
┌─────────┐ │
│  QUEUE  │ │
└─────────┘/`)

		coordBox := lipgloss.NewStyle().Foreground(style.Accent).Bold(true).Render(
			`┌───────────┐
│ [O]   ░░░ │
│ ▒▒▒▒ ▒▒▒▒ │
└───────────┘`)

		wABox := lipgloss.NewStyle().Foreground(greenColor).Bold(true).Render(
			`┌───────────┐
│ ┌───┐ ░░░ │
│ │CPU│ ░░░ │
└─└───┘─────┘`)

		wBBox := lipgloss.NewStyle().Foreground(greenColor).Bold(true).Render(
			`┌───────────┐
│ ┌───┐ ░░░ │
│ │CPU│ ░░░ │
└─└───┘─────┘`)

		// Spacing & connector scaling
		gapWidth := wRight - 4 - 26
		if gapWidth < 5 {
			gapWidth = 5
		} else if gapWidth > 20 {
			gapWidth = 20
		}
		gapStr := strings.Repeat(" ", gapWidth)
		dashCount := (13 + gapWidth - 2) / 2

		wRow := 26 + gapWidth
		queueLine := lipgloss.PlaceHorizontal(wRow, lipgloss.Center, queueBox)
		coordLine := lipgloss.PlaceHorizontal(wRow, lipgloss.Center, coordBox)

		arrow := lipgloss.NewStyle().Foreground(style.Accent).Bold(true).Render("▼")
		vLine := lipgloss.NewStyle().Foreground(style.Muted).Render("│")
		dot := lipgloss.NewStyle().Foreground(greenColor).Bold(true).Render("•")
		hLine := lipgloss.NewStyle().Foreground(style.Muted).Render("─")
		cornerL := lipgloss.NewStyle().Foreground(style.Muted).Render("┌")
		cornerR := lipgloss.NewStyle().Foreground(style.Muted).Render("┐")
		cornerBot := lipgloss.NewStyle().Foreground(style.Muted).Render("┴")

		arrowPad := strings.Repeat(" ", 7+dashCount)

		var topConn string
		switch m.frame % 4 {
		case 0:
			topConn = fmt.Sprintf("%s%s\n%s%s", arrowPad, arrow, arrowPad, arrow)
		case 1:
			topConn = fmt.Sprintf("%s%s\n%s%s", arrowPad, dot, arrowPad, arrow)
		case 2:
			topConn = fmt.Sprintf("%s%s\n%s%s", arrowPad, vLine, arrowPad, arrow)
		default:
			topConn = fmt.Sprintf("%s%s\n%s%s", arrowPad, vLine, arrowPad, dot)
		}

		var botConn string
		hDashes := strings.Repeat(hLine, dashCount)
		switch m.frame % 3 {
		case 0:
			botConn = fmt.Sprintf("%s%s\n      %s%s%s%s%s\n      %s%s%s",
				arrowPad, arrow,
				arrow, hDashes, cornerBot, hDashes, cornerR,
				vLine, strings.Repeat(" ", 2*dashCount+1), vLine)
		case 1:
			botConn = fmt.Sprintf("%s%s\n      %s%s%s%s%s\n      %s%s%s",
				arrowPad, vLine,
				cornerL, hDashes, cornerBot, hDashes, arrow,
				dot, strings.Repeat(" ", 2*dashCount+1), vLine)
		default:
			botConn = fmt.Sprintf("%s%s\n      %s%s%s%s%s\n      %s%s%s",
				arrowPad, vLine,
				cornerL, hDashes, cornerBot, hDashes, cornerR,
				vLine, strings.Repeat(" ", 2*dashCount+1), dot)
		}

		boxesLine := lipgloss.JoinHorizontal(lipgloss.Top, wABox, gapStr, wBBox)
		labelsLine := fmt.Sprintf("%-*s%-s", 13+gapWidth, "WORKER A", "WORKER B")
		diag := lipgloss.JoinVertical(lipgloss.Left, queueLine, topConn, coordLine, botConn, boxesLine, labelsLine)

		topContent := lipgloss.Place(wRight, hTarget-3, lipgloss.Center, lipgloss.Center, diag)
		rightLines = append(rightLines, topContent)
		rightContent = lipgloss.NewStyle().
			Width(wRight).
			Padding(1, 2).
			Render(lipgloss.JoinVertical(lipgloss.Left, rightLines...))
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, midContent, divider, rightContent)
}
