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
)

// greenColor is the "healthy/ok" accent — was internal/tui/dashboard/workers.go's
// package var; that file is gone with the worker fleet concept, but Overview
// and Tokens still want it for their status pills.
var greenColor = lipgloss.Color("42")

// overviewModel is the node home screen — facts only from this profile,
// this process, and this machine. There is no fleet to poll: node-only,
// no coordinator, no workers.
type overviewModel struct {
	nodeID      string
	tailscaleIP string
	width       int
	height      int

	cpu float64
	mem float64
}

func newOverviewModel(nodeID, tailscaleIP string) overviewModel {
	m := overviewModel{
		nodeID:      nodeID,
		tailscaleIP: tailscaleIP,
	}
	return m.refreshLocal()
}

func (m overviewModel) Init() tea.Cmd { return nil }

func (m overviewModel) Update(msg tea.Msg) (overviewModel, tea.Cmd) {
	return m, nil
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

func renderPane(title, content string, width, height int) string {
	if width < 10 {
		width = 10
	}
	if height < 3 {
		height = 3
	}
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(style.Accent)
	innerW := width - 2
	innerH := height - 2
	if innerW < 6 {
		innerW = 6
	}
	if innerH < 1 {
		innerH = 1
	}
	line := lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Repeat("─", max(innerW-4, 4)))
	body := titleStyle.Render(title) + "\n" + line + "\n" + content
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Muted).
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

	// ── Status strip ─────────────────────────────────────────────────
	nodeDisp := truncateMiddle(m.nodeID, 20)
	if nodeDisp == "" {
		nodeDisp = "—"
	}

	var tsPill string
	if m.tailscaleIP != "" {
		tsPill = pill(" TAILSCALE OK ", lipgloss.Color("0"), greenColor)
	} else {
		tsPill = pill(" TAILSCALE — ", lipgloss.Color("15"), style.Danger)
	}

	stripInner := label.Render(" node ") + val.Bold(true).Render(nodeDisp) + "  " + tsPill
	if m.tailscaleIP != "" {
		stripInner += "  " + label.Render(m.tailscaleIP)
	}
	strip := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Muted).
		Padding(0, 1).
		Width(width - 2).
		Render(stripInner)
	stripH := lipgloss.Height(strip)

	// ── MACHINE pane ────────────────────────────────────────────────
	const gaps = 3
	remain := height - stripH - gaps
	if remain < 6 {
		remain = 6
	}

	barW := width - 20
	if barW < 8 {
		barW = 8
	}
	cpuBar := renderMetricBar(barW, m.cpu)
	memBar := renderMetricBar(barW, m.mem)

	metricRow := func(name, pct string, bar string) string {
		return fmt.Sprintf("%s %s  %s",
			label.Render(fmt.Sprintf("%-4s", name)),
			val.Render(fmt.Sprintf("%5s", pct)),
			bar,
		)
	}
	machineContent := strings.Join([]string{
		metricRow("cpu", fmt.Sprintf("%.0f%%", m.cpu*100), cpuBar),
		metricRow("ram", fmt.Sprintf("%.0f%%", m.mem*100), memBar),
	}, "\n")

	machinePane := renderPane("MACHINE", machineContent, width, remain)

	help := style.Help.Render("Tab switch tabs   /logs   / command   q quit")

	out := lipgloss.JoinVertical(lipgloss.Left,
		strip,
		"",
		machinePane,
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
