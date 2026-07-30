package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/tui/client"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

type dashboardAnimTickMsg struct{}

func dashboardAnimTickCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		return dashboardAnimTickMsg{}
	})
}

var greenColor = lipgloss.Color("42")

var stateStyle = map[string]lipgloss.Style{
	"free":    lipgloss.NewStyle().Foreground(greenColor).Bold(true),
	"busy":    lipgloss.NewStyle().Foreground(style.Accent).Bold(true),
	"offline": lipgloss.NewStyle().Foreground(style.Danger).Bold(true),
}

func renderState(s string) string {
	if st, ok := stateStyle[s]; ok {
		return st.Render(s)
	}
	return style.Help.Render(s)
}

func bar(used, total float32, width int) string {
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	filled := min(int(float32(width)*used/total), width)
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

const workersListWidth = 56

type workersListModel struct {
	client        client.Client
	table         table.Model
	workers       []client.WorkerSummary
	allJobs       []client.JobSummary
	width, height int
	frame         int
}

func newWorkersListModel(c client.Client) workersListModel {
	m := workersListModel{
		client: c,
		table: table.New(
			table.WithColumns([]table.Column{
				{Title: "ID", Width: 26},
				{Title: "STATE", Width: 10},
				{Title: "LAST SEEN", Width: 12},
			}),
			table.WithFocused(true),
			table.WithHeight(10),
		),
	}
	return m.refresh()
}

// tableHeight sizes the table to its actual row count (capped to whatever
// pane height is available) instead of always stretching to fill it — no
// floor below that, since any spare row in the viewport gives the cursor
// room to move into empty space, which reads as unwanted scrolling when
// there's only one or two real rows.
func (m workersListModel) tableHeight() int {
	h := len(m.workers) + 1 // +1 for the header row
	if m.height > 0 && h > m.height {
		h = m.height
	}
	return h
}

func (m workersListModel) refresh() workersListModel {
	cursor := m.table.Cursor()
	workers, _ := m.client.ListWorkers()
	m.workers = workers
	m.allJobs, _ = m.client.ListJobs()

	rows := make([]table.Row, 0, len(workers))
	for _, w := range workers {
		rows = append(rows, table.Row{w.ID, w.State, w.LastSeen})
	}
	m.table.SetRows(rows)
	m.table.SetCursor(min(cursor, max(len(rows)-1, 0)))
	m.table.SetHeight(m.tableHeight())
	return m
}

func (m workersListModel) WithSize(width, height int) workersListModel {
	m.width, m.height = width, height
	m.table.SetWidth(workersListWidth)
	m.table.SetHeight(m.tableHeight())
	return m
}

func (m workersListModel) Init() tea.Cmd { return dashboardAnimTickCmd() }

// jobsFor returns every job whose Worker field matches workerID, newest
// first (ListJobs already sorts that way) — the selected worker's job
// history, not just whatever it's running right now.
func (m workersListModel) jobsFor(workerID string) []client.JobSummary {
	var jobs []client.JobSummary
	for _, j := range m.allJobs {
		if j.Worker == workerID {
			jobs = append(jobs, j)
		}
	}
	return jobs
}

func (m workersListModel) selected() (client.WorkerSummary, bool) {
	i := m.table.Cursor()
	if i < 0 || i >= len(m.workers) {
		return client.WorkerSummary{}, false
	}
	return m.workers[i], true
}

func (m workersListModel) Update(msg tea.Msg) (workersListModel, tea.Cmd) {
	switch msg.(type) {
	case dashboardAnimTickMsg:
		m.frame++
		return m, dashboardAnimTickCmd()
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m workersListModel) View() string {
	left := m.table.View()
	if len(m.workers) == 0 {
		left = style.Help.Render("No workers connected yet.")
	}

	wTotal := m.width
	if wTotal <= 0 {
		wTotal = 120 // fallback
	}

	remaining := max(wTotal-workersListWidth-2, 40)
	wMid := int(float64(remaining) * 0.50)
	wRight := remaining - wMid

	var midContent, rightContent string

	if len(m.workers) == 0 {
		midContent = lipgloss.NewStyle().Width(wMid).Padding(1, 2).Render(style.Help.Render("—"))
		rightContent = lipgloss.NewStyle().Width(wRight).Padding(1, 2).Render(style.Help.Render("—"))
	} else if w, ok := m.selected(); !ok {
		midContent = lipgloss.NewStyle().Width(wMid).Padding(1, 2).Render(style.Help.Render("Select a worker"))
		rightContent = lipgloss.NewStyle().Width(wRight).Padding(1, 2).Render(style.Help.Render("Select a worker"))
	} else {
		// Group 1: Worker Details + Resources
		var midLines []string
		midLines = append(midLines, style.Title.Render("Worker Details"), "")
		midLines = append(midLines, contextLine("ID", w.ID))
		midLines = append(midLines, contextLine("State", renderState(w.State)))

		jobText := "idle"
		if w.JobID != "" {
			jobText = w.JobID
			if len(jobText) > 20 {
				jobText = jobText[:17] + "..."
			}
			jobText += fmt.Sprintf(" (%s)", w.JobStatus)
		}
		midLines = append(midLines, contextLine("Job", jobText))
		midLines = append(midLines, contextLine("Seen", w.LastSeen))

		sepWidth := max(wMid-4, 10)
		midLines = append(midLines, "", style.Help.Render(strings.Repeat("┄", sepWidth)), "")

		midLines = append(midLines, style.Title.Render("Resources"), "")

		ramBar := bar(w.RAMUsedGB, w.RAMGB, 12)
		midLines = append(midLines,
			contextLine("RAM", fmt.Sprintf("[%s]  %.1f / %.0f GB", ramBar, w.RAMUsedGB, w.RAMGB)),
		)

		diskBar := bar(w.DiskUsedGB, w.DiskTotalGB, 12)
		midLines = append(midLines,
			"",
			contextLine("Disk", fmt.Sprintf("[%s]  %.0f / %.0f GB", diskBar, w.DiskUsedGB, w.DiskTotalGB)),
		)

		midLines = append(midLines, "", style.Help.Render(strings.Repeat("┄", sepWidth)), "")
		midLines = append(midLines, style.Title.Render("Jobs"), "")

		jobs := m.jobsFor(w.ID)
		if len(jobs) == 0 {
			midLines = append(midLines, style.Help.Render("No jobs on this worker yet."))
		} else {
			for _, j := range jobs {
				id := j.ID
				if len(id) > 12 {
					id = id[:12]
				}
				midLines = append(midLines, fmt.Sprintf("%-14s %-10s %s", id, j.Status, j.Submitted))
			}
		}

		midContent = lipgloss.NewStyle().
			Width(wMid).
			Padding(1, 2).
			Render(lipgloss.JoinVertical(lipgloss.Left, midLines...))

		// Group 2: Topology diagram
		var rightLines []string
		rightLines = append(rightLines, style.Title.Render("Topology"), "")

		ramLabel := fmt.Sprintf("%.0fG", w.RAMGB)
		dskLabel := fmt.Sprintf("%.0fG", w.DiskTotalGB)
		var diag string

		cpuBox := lipgloss.NewStyle().Foreground(style.Accent).Bold(true).Render(
			`     _  _  _  _  _
   ┌┴──┴──┴──┴──┴┐
  ─┤             ├─
  ─┤     CPU     ├─
  ─┤             ├─
   └┬──┬──┬──┬──┬┘`)

		ramBox := lipgloss.NewStyle().Foreground(greenColor).Bold(true).Render(
			`┌───────────┐
│█ █ █ █ █ █│
│█ █ █ █ █ █│
└╨─╨─╨─╨─╨─╨┘`)

		dskBox := lipgloss.NewStyle().Foreground(greenColor).Bold(true).Render(
			`┌───────────┐
│  /═════\  │
│ ║(  O  )║/│
└─ \═════/─┘`)

		if w.HasGPU {
			gpuName := w.GPUName
			if strings.Contains(strings.ToLower(gpuName), "nvidia") {
				gpuName = "NVIDIA"
			} else if len(gpuName) > 9 {
				gpuName = gpuName[:9]
			}
			if gpuName == "" {
				gpuName = "GPU"
			}

			gpuBox := lipgloss.NewStyle().Foreground(style.Accent).Bold(true).Render(
				`┌───────────┐
│ ░░░░  /══\│
│ ░░░░ ║(O)║│
└╨───── \══/┘`)

			// Dynamic spacing calculation for 3-way layout
			gapWidth := (wRight - 4 - 39) / 2
			if gapWidth < 3 {
				gapWidth = 3
			} else if gapWidth > 12 {
				gapWidth = 12
			}
			gapStr := strings.Repeat(" ", gapWidth)
			dashCount := 12 + gapWidth

			cpuPad := 10 + gapWidth
			cpuLine := strings.Repeat(" ", cpuPad) + strings.ReplaceAll(cpuBox, "\n", "\n"+strings.Repeat(" ", cpuPad))

			// Animated 3-way connector pulse
			pCol := style.Accent
			arrow := lipgloss.NewStyle().Foreground(pCol).Bold(true).Render("▼")
			vLine := lipgloss.NewStyle().Foreground(style.Muted).Render("│")
			hLine := lipgloss.NewStyle().Foreground(style.Muted).Render("─")
			cross := lipgloss.NewStyle().Foreground(style.Muted).Render("┼")
			cornerL := lipgloss.NewStyle().Foreground(style.Muted).Render("┌")
			cornerR := lipgloss.NewStyle().Foreground(style.Muted).Render("┐")

			var connLine string
			f := m.frame % 4

			arrowPad := strings.Repeat(" ", 7+dashCount)
			hDashes := strings.Repeat(hLine, dashCount)
			spPad := strings.Repeat(" ", dashCount)

			switch f {
			case 0:
				connLine = fmt.Sprintf("%s%s\n      %s%s%s%s%s\n      %s%s%s%s%s",
					arrowPad, arrow,
					arrow, hDashes, cross, hDashes, cornerR,
					vLine, spPad, vLine, spPad, vLine)
			case 1:
				connLine = fmt.Sprintf("%s%s\n      %s%s%s%s%s\n      %s%s%s%s%s",
					arrowPad, vLine,
					cornerL, hDashes, arrow, hDashes, cornerR,
					vLine, spPad, vLine, spPad, vLine)
			case 2:
				connLine = fmt.Sprintf("%s%s\n      %s%s%s%s%s\n      %s%s%s%s%s",
					arrowPad, vLine,
					cornerL, hDashes, cross, hDashes, arrow,
					vLine, spPad, vLine, spPad, vLine)
			default:
				connLine = fmt.Sprintf("%s%s\n      %s%s%s%s%s\n      %s%s%s%s%s",
					arrowPad, vLine,
					cornerL, hDashes, cross, hDashes, cornerR,
					vLine, spPad, vLine, spPad, vLine)
			}

			boxesLine := lipgloss.JoinHorizontal(lipgloss.Top, ramBox, gapStr, gpuBox, gapStr, dskBox)
			labelsLine := fmt.Sprintf("%-*s%-*s%-s", 13+gapWidth, ramLabel+" RAM", 13+gapWidth, gpuName+" GPU", dskLabel+" DSK")
			diag = lipgloss.JoinVertical(lipgloss.Left, cpuLine, connLine, boxesLine, labelsLine)
		} else {
			// Dynamic spacing calculation for 2-way layout
			gapWidth := wRight - 4 - 26
			if gapWidth < 5 {
				gapWidth = 5
			} else if gapWidth > 20 {
				gapWidth = 20
			}
			gapStr := strings.Repeat(" ", gapWidth)

			dashCount := (13 + gapWidth - 2) / 2
			cpuPad := dashCount - 2
			cpuLine := strings.Repeat(" ", cpuPad) + strings.ReplaceAll(cpuBox, "\n", "\n"+strings.Repeat(" ", cpuPad))

			// Animated 2-way connector pulse
			pCol := style.Accent
			arrow := lipgloss.NewStyle().Foreground(pCol).Bold(true).Render("▼")
			vLine := lipgloss.NewStyle().Foreground(style.Muted).Render("│")
			hLine := lipgloss.NewStyle().Foreground(style.Muted).Render("─")
			cornerL := lipgloss.NewStyle().Foreground(style.Muted).Render("┌")
			cornerR := lipgloss.NewStyle().Foreground(style.Muted).Render("┐")
			cornerBot := lipgloss.NewStyle().Foreground(style.Muted).Render("┴")

			var connLine string
			f := m.frame % 3
			arrowPad := strings.Repeat(" ", 7+dashCount)
			hDashes := strings.Repeat(hLine, dashCount)

			switch f {
			case 0:
				connLine = fmt.Sprintf("%s%s\n      %s%s%s%s%s\n      %s%s%s",
					arrowPad, arrow,
					arrow, hDashes, cornerBot, hDashes, cornerR,
					vLine, strings.Repeat(" ", 2*dashCount+1), vLine)
			case 1:
				connLine = fmt.Sprintf("%s%s\n      %s%s%s%s%s\n      %s%s%s",
					arrowPad, vLine,
					cornerL, hDashes, cornerBot, hDashes, arrow,
					vLine, strings.Repeat(" ", 2*dashCount+1), vLine)
			default:
				connLine = fmt.Sprintf("%s%s\n      %s%s%s%s%s\n      %s%s%s",
					arrowPad, vLine,
					cornerL, hDashes, cornerBot, hDashes, cornerR,
					vLine, strings.Repeat(" ", 2*dashCount+1), vLine)
			}

			boxesLine := lipgloss.JoinHorizontal(lipgloss.Top, ramBox, gapStr, dskBox)
			labelsLine := fmt.Sprintf("%-*s%-s", 13+gapWidth, ramLabel+" RAM", dskLabel+" DSK")
			diag = lipgloss.JoinVertical(lipgloss.Left, cpuLine, connLine, boxesLine, labelsLine)
		}

		topContent := lipgloss.PlaceHorizontal(wRight, lipgloss.Center, diag)
		rightLines = append(rightLines, topContent)
		rightContent = lipgloss.NewStyle().
			Width(wRight).
			Padding(1, 2).
			Render(lipgloss.JoinVertical(lipgloss.Left, rightLines...))
	}

	// Divider runs the full pane height (real column separators), even
	// though the content blocks themselves stay their natural size —
	// content stretching is what left the earlier "half page blank" boxes;
	// a couple of thin separator lines running to the bottom don't.
	divHeight := max(lipgloss.Height(left), lipgloss.Height(midContent), lipgloss.Height(rightContent), m.height)
	divLines := make([]string, divHeight)
	for i := range divLines {
		divLines[i] = "│"
	}
	divider := lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Join(divLines, "\n"))

	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, midContent, divider, rightContent)
}

func contextLine(label, value string) string {
	pad := max(10-len(label), 1)
	return style.Help.Render(label) + strings.Repeat(" ", pad) + value
}
