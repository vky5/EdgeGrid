package dashboard

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/tui/style"
	"github.com/edgegrid/edgegrid/internal/worker"
	"github.com/edgegrid/edgegrid/internal/worker/hardware"
)

// overviewModel is the node home screen for both workers and coordinators.
// Facts only from this profile, this process, and this machine — never
// admin HTTP / Stub fleet lists.
type overviewModel struct {
	nodeID   string
	natsURL  string
	isWorker bool
	width    int
	height   int

	cpu float64
	mem float64
	hw  hardware.Spec

	agentUp   bool
	agentBusy bool
	agentJobs []string
	doneOK    int
	doneFail  int
	recent    []worker.FinishedJob
}

func newOverviewModel(nodeID string, natsURL string, isWorker bool) overviewModel {
	m := overviewModel{
		nodeID:   nodeID,
		natsURL:  natsURL,
		isWorker: isWorker,
		hw:       hardware.Detect(),
	}
	return m.refreshLocal()
}

func (m overviewModel) Init() tea.Cmd { return nil }

func (m overviewModel) Update(msg tea.Msg) (overviewModel, tea.Cmd) {
	return m, nil
}

func (m overviewModel) WithAgentRuntime(
	agentUp, busy bool,
	jobIDs []string,
	doneOK, doneFail int,
	recent []worker.FinishedJob,
) overviewModel {
	m.agentUp = agentUp
	m.agentBusy = busy
	m.agentJobs = append([]string(nil), jobIDs...)
	m.doneOK = doneOK
	m.doneFail = doneFail
	m.recent = append([]worker.FinishedJob(nil), recent...)
	return m
}

func (m overviewModel) refreshLocal() overviewModel {
	m.cpu = getLocalCPUUsage()
	m.mem = getLocalMemUsage()
	return m
}

func truncateMiddle(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// pill is a compact status chip: " ● label ".
func pill(label string, fg, bg lipgloss.Color) string {
	return lipgloss.NewStyle().
		Foreground(fg).
		Background(bg).
		Bold(true).
		Padding(0, 1).
		Render(label)
}

func mutedPill(label string) string {
	return lipgloss.NewStyle().
		Foreground(style.Muted).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Muted).
		Padding(0, 1).
		Render(label)
}

func renderMetricBar(width int, val float64) string {
	if width < 4 {
		width = 4
	}
	if val < 0 {
		val = 0
	}
	if val > 1 {
		val = 1
	}
	filled := int(val * float64(width))
	if filled > width {
		filled = width
	}
	// Color by load: cool → warm → hot
	col := lipgloss.Color("42") // green
	if val >= 0.85 {
		col = style.Danger
	} else if val >= 0.55 {
		col = lipgloss.Color("214") // orange
	} else if val >= 0.3 {
		col = style.Accent
	}
	bar := lipgloss.NewStyle().Foreground(col).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Repeat("░", width-filled))
	return bar
}

func renderPane(title, content string, width, height int, accentBorder bool) string {
	if width < 10 {
		width = 10
	}
	if height < 3 {
		height = 3
	}
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(style.Accent)
	borderColor := style.Muted
	if accentBorder {
		borderColor = style.Accent
	}
	innerW := width - 2
	innerH := height - 2
	if innerW < 6 {
		innerW = 6
	}
	if innerH < 1 {
		innerH = 1
	}
	// Title with subtle underline of dashes inside the card
	line := lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Repeat("─", max(innerW-4, 4)))
	body := titleStyle.Render(title) + "\n" + line + "\n" + content
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(innerW).
		Height(innerH).
		Render(body)
}

func (m overviewModel) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	label := lipgloss.NewStyle().Foreground(style.Muted)
	val := lipgloss.NewStyle().Foreground(lipgloss.Color("255"))

	// ── Status strip: pills ─────────────────────────────────────────────
	nodeDisp := truncateMiddle(m.nodeID, 14)
	if nodeDisp == "" {
		nodeDisp = "—"
	}

	rolePill := mutedPill("COORDINATOR")
	if m.isWorker {
		rolePill = pill(" WORKER ", lipgloss.Color("0"), style.Accent)
	}

	var natsPill string
	if m.natsURL != "" {
		natsPill = pill(" NATS OK ", lipgloss.Color("0"), greenColor)
	} else {
		natsPill = pill(" NATS — ", lipgloss.Color("15"), style.Danger)
	}

	var agentPill string
	switch {
	case !m.agentUp:
		agentPill = mutedPill("agent off")
	case m.agentBusy:
		agentPill = pill(" BUSY ", lipgloss.Color("0"), style.Accent)
	default:
		agentPill = pill(" IDLE ", lipgloss.Color("0"), greenColor)
	}

	// Session mini-stats on the strip (right side feel)
	total := m.doneOK + m.doneFail
	sess := label.Render(fmt.Sprintf("session %d ok · %d fail", m.doneOK, m.doneFail))
	if total > 0 {
		rate := float64(m.doneOK) / float64(total) * 100
		sess = label.Render(fmt.Sprintf("session %d ok · %d fail · %.0f%%", m.doneOK, m.doneFail, rate))
	}

	stripLeft := lipgloss.JoinHorizontal(lipgloss.Center,
		label.Render(" node ")+val.Bold(true).Render(nodeDisp)+"  ",
		rolePill, " ", natsPill, " ", agentPill,
	)
	// Put session stats on the right if width allows
	pad := width - 4 - lipgloss.Width(stripLeft) - lipgloss.Width(sess)
	if pad < 2 {
		pad = 2
	}
	stripInner := stripLeft + strings.Repeat(" ", pad) + sess
	stripBorder := style.Muted
	if m.agentBusy {
		stripBorder = style.Accent
	}
	strip := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(stripBorder).
		Padding(0, 1).
		Width(width - 2).
		Render(stripInner)
	stripH := lipgloss.Height(strip)

	// Role one-liner
	var tagline string
	if m.isWorker {
		tagline = lipgloss.NewStyle().Foreground(style.Accent).Render(
			"▸  This node only accepts & runs jobs over NATS — no submit, fleet, or admin.",
		)
	} else {
		tagline = label.Render(
			"▸  This machine’s local view. Fleet jobs live under the Jobs tab · n to submit.",
		)
	}
	taglineH := 1

	// Height budget
	const gaps = 4
	remain := height - stripH - taglineH - gaps
	if remain < 8 {
		remain = 8
	}
	topH := remain * 38 / 100
	if topH < 7 {
		topH = 7
	}
	jobsH := remain - topH
	if jobsH < 6 {
		jobsH = 6
		topH = remain - jobsH
		if topH < 6 {
			topH = 6
		}
	}

	gap := 2
	leftW := (width - gap) * 50 / 100
	rightW := width - gap - leftW
	if leftW < 26 {
		leftW = 26
		rightW = width - gap - leftW
	}
	if rightW < 24 {
		rightW = 24
		leftW = width - gap - rightW
	}

	// ── NOW pane (hero status) ──────────────────────────────────────────
	var nowLines []string
	switch {
	case !m.agentUp:
		nowLines = append(nowLines,
			lipgloss.NewStyle().Bold(true).Foreground(style.Muted).Render("AGENT OFFLINE"),
			"",
			label.Render("No agent in this TUI process."),
			label.Render("Start Local Agent from the welcome screen"),
			label.Render("to accept and run jobs here."),
		)
	case m.agentBusy:
		nowLines = append(nowLines,
			lipgloss.NewStyle().Bold(true).Foreground(style.Accent).Render("●  RUNNING"),
			"",
		)
		if len(m.agentJobs) == 0 {
			nowLines = append(nowLines, label.Render("job id registering…"))
		}
		for _, id := range m.agentJobs {
			chip := lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("15")).
				Background(style.Accent).
				Padding(0, 1).
				Render("▶ " + truncateMiddle(id, max(leftW-10, 12)))
			nowLines = append(nowLines, chip)
		}
		nowLines = append(nowLines, "", label.Render("Logs stream under Jobs once assigned."))
	default:
		nowLines = append(nowLines,
			lipgloss.NewStyle().Bold(true).Foreground(greenColor).Render("○  IDLE"),
			"",
			label.Render("Waiting for work over NATS."),
			label.Render("Heartbeats are going out; free for the next job."),
		)
	}

	// Mini stat row
	statOK := lipgloss.NewStyle().Foreground(greenColor).Bold(true).Render(fmt.Sprintf("%d", m.doneOK))
	statFail := lipgloss.NewStyle().Foreground(style.Danger).Bold(true).Render(fmt.Sprintf("%d", m.doneFail))
	statRun := "0"
	if m.agentBusy {
		statRun = lipgloss.NewStyle().Foreground(style.Accent).Bold(true).Render(fmt.Sprintf("%d", max(len(m.agentJobs), 1)))
	} else {
		statRun = lipgloss.NewStyle().Foreground(style.Muted).Render("0")
	}
	nowLines = append(nowLines, "",
		fmt.Sprintf("%s ok   %s fail   %s running",
			statOK, statFail, statRun),
	)
	nowContent := strings.Join(nowLines, "\n")

	// ── MACHINE pane ────────────────────────────────────────────────────
	barW := rightW - 14
	if barW < 8 {
		barW = 8
	}
	cpuBar := renderMetricBar(barW, m.cpu)
	memBar := renderMetricBar(barW, m.mem)

	gpuLine := label.Render("none detected")
	if m.hw.HasGPU {
		g := m.hw.GPUName
		if m.hw.GPUVramGB > 0 {
			g = fmt.Sprintf("%s · %.0f GB", g, m.hw.GPUVramGB)
		}
		gpuLine = val.Render(truncateMiddle(g, max(rightW-8, 14)))
	}
	diskLine := label.Render("—")
	if m.hw.DiskFreeGB > 0 {
		diskLine = val.Render(fmt.Sprintf("%.0f GB free", m.hw.DiskFreeGB))
	}
	natsDetail := label.Render("not configured")
	if m.natsURL != "" {
		natsDetail = val.Render(truncateMiddle(m.natsURL, max(rightW-10, 14)))
	}

	metricRow := func(name, pct string, bar string) string {
		return fmt.Sprintf("%s %s  %s",
			label.Render(fmt.Sprintf("%-4s", name)),
			val.Render(fmt.Sprintf("%5s", pct)),
			bar,
		)
	}
	machineContent := strings.Join([]string{
		label.Render("host") + "  " + val.Render(truncateMiddle(m.nodeID, max(rightW-10, 12))),
		label.Render("nats") + "  " + natsDetail,
		"",
		metricRow("cpu", fmt.Sprintf("%.0f%%", m.cpu*100), cpuBar),
		metricRow("ram", fmt.Sprintf("%.0f%%", m.mem*100), memBar),
		"",
		label.Render("gpu ") + " " + gpuLine,
		label.Render("disk") + " " + diskLine,
	}, "\n")

	nowPane := renderPane("NOW", nowContent, leftW, topH, m.agentBusy)
	machinePane := renderPane("MACHINE", machineContent, rightW, topH, false)
	midRow := lipgloss.JoinHorizontal(lipgloss.Top, nowPane, strings.Repeat(" ", gap), machinePane)

	// ── JOBS table ──────────────────────────────────────────────────────
	jobsTitle := "SESSION JOBS"
	if m.isWorker {
		jobsTitle = "SESSION JOBS  ·  this worker process"
	} else {
		jobsTitle = "SESSION JOBS  ·  local agent only"
	}

	// Column layout
	innerJobsW := width - 6
	if innerJobsW < 30 {
		innerJobsW = 30
	}
	colStatus := 10
	colID := innerJobsW - colStatus - 2
	if colID < 12 {
		colID = 12
	}

	header := lipgloss.NewStyle().Bold(true).Foreground(style.Accent).Render(
		fmt.Sprintf("  %-*s  %s", colStatus, "STATUS", "JOB ID"),
	)
	sep := lipgloss.NewStyle().Foreground(style.Muted).Render("  " + strings.Repeat("─", max(innerJobsW-2, 8)))

	var jobLines []string
	jobLines = append(jobLines, header, sep)

	activeSet := map[string]bool{}
	for _, id := range m.agentJobs {
		activeSet[id] = true
		st := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(style.Accent).
			Render(fmt.Sprintf(" %-8s ", "RUNNING"))
		idS := val.Bold(true).Render(truncateMiddle(id, colID))
		jobLines = append(jobLines, fmt.Sprintf("  %s  %s", st, idS))
	}
	for i := len(m.recent) - 1; i >= 0; i-- {
		j := m.recent[i]
		if activeSet[j.ID] {
			continue
		}
		var st string
		if j.Success {
			st = lipgloss.NewStyle().Bold(true).Foreground(greenColor).Render(fmt.Sprintf("%-*s", colStatus, "OK"))
		} else {
			st = lipgloss.NewStyle().Bold(true).Foreground(style.Danger).Render(fmt.Sprintf("%-*s", colStatus, "FAILED"))
		}
		jobLines = append(jobLines, fmt.Sprintf("  %s  %s", st, val.Render(truncateMiddle(j.ID, colID))))
	}

	if len(m.agentJobs) == 0 && len(m.recent) == 0 {
		jobLines = append(jobLines,
			"",
			label.Render("  No jobs this session yet."),
			label.Render("  When work is assigned (or you submit from Jobs), rows appear here."),
		)
	}

	// Fit rows to pane
	rowBudget := jobsH - 5
	if rowBudget < 2 {
		rowBudget = 2
	}
	// header + sep already 2 lines of jobLines content inside pane title overhead
	contentLines := jobLines[2:] // after header+sep
	if len(contentLines) > rowBudget {
		kept := contentLines[:rowBudget-1]
		omitted := len(contentLines) - (rowBudget - 1)
		jobLines = append(jobLines[:2], kept...)
		jobLines = append(jobLines, label.Render(fmt.Sprintf("  … %d more this session", omitted)))
	}

	jobsPane := renderPane(jobsTitle, strings.Join(jobLines, "\n"), width, jobsH, len(m.agentJobs) > 0)

	help := style.Help.Render("Tab switch tabs   /logs   / command   q quit")
	if !m.isWorker {
		help = style.Help.Render("n submit job   Tab switch   /logs   / command   q quit")
	}

	out := lipgloss.JoinVertical(lipgloss.Left,
		strip,
		"",
		tagline,
		"",
		midRow,
		"",
		jobsPane,
		help,
	)
	return lipgloss.NewStyle().MaxHeight(height).MaxWidth(width).Render(out)
}

var (
	overviewPrevIdle, overviewPrevTotal uint64
)

func getLocalCPUUsage() float64 {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 5 && fields[0] == "cpu" {
			var total, idle uint64
			for i := 1; i < len(fields); i++ {
				v, _ := strconv.ParseUint(fields[i], 10, 64)
				total += v
				if i == 4 {
					idle = v
				}
			}
			diffIdle := idle - overviewPrevIdle
			diffTotal := total - overviewPrevTotal
			overviewPrevIdle = idle
			overviewPrevTotal = total
			if diffTotal > 0 {
				return 1.0 - (float64(diffIdle) / float64(diffTotal))
			}
		}
	}
	return 0
}

func getLocalMemUsage() float64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()

	var total, available float64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				total, _ = strconv.ParseFloat(fields[1], 64)
			}
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				available, _ = strconv.ParseFloat(fields[1], 64)
			}
		}
	}
	if total > 0 {
		return (total - available) / total
	}
	return 0
}
