// Package app is the TUI root bubbletea Model: the "/" command bar, the
// logs overlay, and the global quit key, wrapped around dashboard.Dashboard
// — the only content this program ever shows, since there's no onboarding
// wizard or role picker anymore. A node's identity and tailnet membership
// are resolved once at process start (node.New, driven by flags/env), not
// interactively, so the TUI has nothing left to onboard: it opens straight
// into the dashboard.
package app

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/node"
	"github.com/edgegrid/edgegrid/internal/tailscaleapi"
	"github.com/edgegrid/edgegrid/internal/tui/cmdbar"
	"github.com/edgegrid/edgegrid/internal/tui/dashboard"
	"github.com/edgegrid/edgegrid/internal/tui/logsview"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// commands is the fixed list the "/" bar autocompletes against.
var commands = []string{"logs", "profile"}

// App is the root bubbletea Model — the only thing cmd/edgegrid ever hands
// to tea.NewProgram.
type App struct {
	dataDir string

	dashboard dashboard.Dashboard
	cmdbar    cmdbar.Model
	logs      logsview.Model
	showLogs  bool

	// restartProfile is set once "/profile <name>" switches the active
	// profile — data dir is fixed at process startup, so main.go restarts
	// the whole process rather than trying to hot-swap it in place.
	restartProfile string

	width, height int
}

// New builds the App around a node already up on the tailnet. tsClient is
// nil when this node has no Tailscale API credentials configured — see
// tailscaleapi.LoadCredentials — in which case the dashboard simply has no
// Tokens tab.
func New(nodeID, tailscaleIP, dataDir string, tsClient *tailscaleapi.Client) App {
	return App{
		dataDir:   dataDir,
		dashboard: dashboard.New(nodeID, tailscaleIP, dataDir, tsClient),
		cmdbar:    cmdbar.New(commands...),
	}
}

// WantsRestart reports whether "/profile <name>" switched the active
// profile.
func (a App) WantsRestart() (profileName string, ok bool) {
	return a.restartProfile, a.restartProfile != ""
}

func (a App) Init() tea.Cmd {
	return tea.Batch(a.dashboard.Init(), tickSystemStats())
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(systemStatsTickMsg); ok {
		return a, tickSystemStats()
	}

	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "ctrl+c" {
		return a, tea.Quit
	}

	// Self-rescheduling tick — must always reach the dashboard, even while
	// an overlay is open, or the reschedule never happens and the local
	// stats poll dies for good instead of just pausing.
	if _, ok := msg.(dashboard.RefreshMsg); ok {
		var cmd tea.Cmd
		a.dashboard, cmd = a.dashboard.Update(msg)
		return a, cmd
	}

	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		a.width, a.height = wm.Width, wm.Height
		var cmd1, cmd2 tea.Cmd
		if a.cmdbar.Active() {
			a.dashboard, cmd1 = a.dashboard.Update(tea.WindowSizeMsg{Width: wm.Width, Height: wm.Height - 5})
		} else {
			a.dashboard, cmd1 = a.dashboard.Update(wm)
		}
		a.cmdbar, cmd2 = a.cmdbar.Update(wm)
		return a, tea.Batch(cmd1, cmd2)
	}

	if a.showLogs {
		if _, ok := msg.(logsview.CloseMsg); ok {
			a.showLogs = false
			return a, nil
		}
		var cmd tea.Cmd
		a.logs, cmd = a.logs.Update(msg)
		return a, cmd
	}

	if sub, ok := msg.(cmdbar.SubmitMsg); ok {
		a.dashboard, _ = a.dashboard.Update(tea.WindowSizeMsg{Width: a.width, Height: a.height})
		return a.runCommand(sub.Command)
	}

	if a.cmdbar.Active() {
		var cmd tea.Cmd
		a.cmdbar, cmd = a.cmdbar.Update(msg)
		if !a.cmdbar.Active() {
			// deactivated internally (esc / empty backspace)
			a.dashboard, _ = a.dashboard.Update(tea.WindowSizeMsg{Width: a.width, Height: a.height})
		}
		return a, cmd
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		// Same rule everywhere a free-form field could have focus: "/" and
		// "q" are literal keystrokes there, not global shortcuts.
		typing := a.dashboard.CapturesTextInput()
		switch key.String() {
		case "q":
			if !typing {
				return a, tea.Quit
			}
		case "/":
			if typing {
				break // let it reach the focused field below, literally
			}
			var cmd tea.Cmd
			a.cmdbar, cmd = a.cmdbar.Activate()
			a.dashboard, _ = a.dashboard.Update(tea.WindowSizeMsg{Width: a.width, Height: a.height - 5})
			return a, cmd
		}
	}

	var cmd tea.Cmd
	a.dashboard, cmd = a.dashboard.Update(msg)
	return a, cmd
}

func (a App) runCommand(command string) (tea.Model, tea.Cmd) {
	command = strings.TrimPrefix(command, "/")

	if name, ok := strings.CutPrefix(command, "profile "); ok {
		if name = strings.TrimSpace(name); name != "" && node.UseProfile(name) == nil {
			a.restartProfile = name
			return a, tea.Quit
		}
		return a, nil
	}

	switch command {
	case "logs":
		a.showLogs = true
		a.logs = logsview.New(a.dataDir, a.width, max(a.height-3, 3))
	}
	return a, nil
}

func (a App) renderHeader() string {
	w := max(a.width, 0)
	badge := lipgloss.NewStyle().
		Background(style.Accent).
		Foreground(lipgloss.Color("255")).
		Bold(true).
		Render(" EDGEGRID ")
	subtitle := "Dashboard"
	if a.showLogs {
		subtitle = "Logs"
	}
	rest := lipgloss.NewStyle().
		Background(style.Accent).
		Foreground(lipgloss.Color("255")).
		Render("  " + subtitle + " ")
	bar := lipgloss.JoinHorizontal(lipgloss.Top, badge, rest)
	pad := w - lipgloss.Width(bar)
	if pad > 0 {
		bar = bar + lipgloss.NewStyle().Background(style.Accent).Render(strings.Repeat(" ", pad))
	}
	return bar
}

func (a App) renderFooter() string {
	profileName := node.ActiveProfile()
	if profileName == "" {
		profileName = "default"
	}
	timeStr := time.Now().Format("15:04:05")
	cpu := getCPUUsage()
	mem := getMemUsage()
	cpuStr := fmt.Sprintf("CPU: %5.1f%%", cpu*100)
	memStr := fmt.Sprintf("RAM: %5.1f%%", mem*100)
	cpuMeter := renderSingleCharMeter(cpu)
	memMeter := renderSingleCharMeter(mem)

	helpKeys := a.dashboard.HelpText()
	if a.cmdbar.Active() {
		helpKeys = "enter run  esc cancel"
	} else if a.showLogs {
		helpKeys = "esc back"
	}

	left := style.FooterBar.Render(fmt.Sprintf("%s  %s%s  %s%s  %s", profileName, cpuStr, cpuMeter, memStr, memMeter, timeStr))
	right := style.FooterBar.Render(helpKeys)
	pad := max(a.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", pad) + right
}

func (a App) View() string {
	header := a.renderHeader()

	var footer string
	if a.cmdbar.Active() {
		footer = a.cmdbar.View()
	} else {
		footer = a.renderFooter()
	}

	var body string
	switch {
	case a.showLogs:
		body = a.logs.View()
	default:
		body = a.dashboard.View()
	}

	if a.width <= 0 || a.height <= 0 {
		return header + "\n\n" + body + "\n\n" + footer
	}
	bodyHeight := max(a.height-lipgloss.Height(header)-lipgloss.Height(footer), 1)
	placedBody := lipgloss.Place(a.width, bodyHeight, lipgloss.Center, lipgloss.Top, body)
	return lipgloss.JoinVertical(lipgloss.Left, header, placedBody, footer)
}

type systemStatsTickMsg struct{}

func tickSystemStats() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return systemStatsTickMsg{} })
}

var prevIdle, prevTotal uint64

func getCPUUsage() float64 {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0.05
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
			diffIdle := idle - prevIdle
			diffTotal := total - prevTotal
			prevIdle = idle
			prevTotal = total
			if diffTotal > 0 {
				return 1.0 - (float64(diffIdle) / float64(diffTotal))
			}
		}
	}
	return 0.05
}

func getMemUsage() float64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0.15
	}
	defer f.Close()
	var total, available float64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			if fields := strings.Fields(line); len(fields) >= 2 {
				total, _ = strconv.ParseFloat(fields[1], 64)
			}
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			if fields := strings.Fields(line); len(fields) >= 2 {
				available, _ = strconv.ParseFloat(fields[1], 64)
			}
		}
	}
	if total > 0 {
		return (total - available) / total
	}
	return 0.15
}

func renderSingleCharMeter(val float64) string {
	bars := []rune{' ', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	idx := int(val * float64(len(bars)))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(bars) {
		idx = len(bars) - 1
	}
	color := "42"
	if val > 0.8 {
		color = "196"
	} else if val > 0.5 {
		color = "214"
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(string(bars[idx]))
}
