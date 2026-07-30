// Package app is the single unified TUI program: one bubbletea root Model
// that owns the "/" command bar, the logs overlay, and the global quit
// key, and switches between the dashboard and onboarding content depending
// on which command was run — instead of those being two separately
// launched programs with their own copies of that chrome.
package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/agent"
	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"github.com/edgegrid/edgegrid/internal/profile"
	"github.com/edgegrid/edgegrid/internal/tui/client"
	"github.com/edgegrid/edgegrid/internal/tui/cmdbar"
	"github.com/edgegrid/edgegrid/internal/tui/dashboard"
	"github.com/edgegrid/edgegrid/internal/tui/logsview"
	"github.com/edgegrid/edgegrid/internal/tui/onboarding"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

type mode int

const (
	modeDashboard mode = iota
	modeOnboarding
	modeWelcome
)

// commands is the fixed list the "/" bar autocompletes against. Add new
// ones here as they're wired up.
var commands = []string{"onboard", "logs", "connect", "profile"}

// App is the root bubbletea Model — the only thing cmd/edgegrid ever hands
// to tea.NewProgram.
type App struct {
	mode mode

	ctx     context.Context
	dataDir string

	// connected is whether the dashboard has a real coordinator behind it.
	// isWorker only affects which message is shown while !connected — a
	// worker has no coordinator of its own to default to, which is
	// different from "just hasn't connected to one yet."
	connected bool
	isWorker  bool

	dashboard dashboard.Dashboard
	wizard    onboarding.Wizard
	welcome   welcomeModel

	// runningAgent is the single node agent running in this process, if
	// any — the one source of truth for "is a node already up" that
	// main.go and the onboarding wizard both read/replace instead of each
	// silently spawning their own. nil until one is started (either
	// pre-existing at TUI launch, passed in via New, or newly built by a
	// completed wizard run).
	runningAgent *agent.Agent

	cmdbar      cmdbar.Model
	logs        logsview.Model
	showLogs    bool
	connect     connectModel
	showConnect bool

	// restartProfile is set once "/profile <name>" switches the active
	// profile — data dir is fixed at process startup, so main.go restarts
	// the whole process rather than trying to hot-swap it in place.
	restartProfile string
	restartOnboard bool
	restartNoAgent bool

	previousMode mode

	width, height int
}

// New builds the App starting in dashboard mode. connected reports whether
// c is a real client already pointed at a coordinator (vs. the canned
// Stub) — the dashboard shows a "not connected" state instead of Stub's
// fake data until /connect (or a real client passed in here) changes that.
func New(ctx context.Context, dataDir string, c client.Client, coord string, connected, isWorker bool, runningAgent *agent.Agent) App {
	return App{
		ctx:          ctx,
		dataDir:      dataDir,
		mode:         modeDashboard,
		connected:    connected,
		isWorker:     isWorker,
		dashboard:    dashboard.New(c, coord),
		cmdbar:       cmdbar.New(commands...),
		runningAgent: runningAgent,
	}
}

// StartInOnboarding switches the initial screen to onboarding — used by
// `edgegrid onboard` as a direct entry point into the same unified
// program, not a separate one. Reachable from dashboard mode afterward via
// "/onboard" either way.
func (a App) StartInOnboarding() App {
	a.previousMode = a.mode
	a.mode = modeOnboarding
	a.wizard = onboarding.NewWizard(a.ctx, a.dataDir, a.runningAgent)
	return a
}

// StartInWelcome switches the initial screen to the VSCode-style welcome menu.
func (a App) StartInWelcome() App {
	a.previousMode = a.mode
	a.mode = modeWelcome
	a.welcome = newWelcomeModel()
	return a
}

// WizardResult forwards to the onboarding wizard's Result — for main.go to
// act on after the program exits, regardless of whether onboarding was the
// starting mode or was reached later via "/onboard".
func (a App) WizardResult() (role onboarding.Role, confirmed bool, startedAgent *agent.Agent, cfg *config.Config, err error) {
	return a.wizard.Result()
}

// WantsRestart reports whether "/profile <name>" switched the active
// profile — main.go must exec a fresh process for the switch to take
// effect (see runCommand).
func (a App) WantsRestart() (profileName string, onboard bool, noAgent bool, ok bool) {
	return a.restartProfile, a.restartOnboard, a.restartNoAgent, a.restartProfile != ""
}

func (a App) Init() tea.Cmd {
	var cmds []tea.Cmd
	cmds = append(cmds, a.dashboard.Init(), tickSystemStats())
	if a.mode == modeOnboarding {
		cmds = append(cmds, a.wizard.Init())
	}
	if a.mode == modeWelcome {
		cmds = append(cmds, a.welcome.Init())
	}
	return tea.Batch(cmds...)
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(systemStatsTickMsg); ok {
		return a, tickSystemStats()
	}

	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "ctrl+c" {
		return a, tea.Quit
	}

	if _, ok := msg.(dashboard.RefreshMsg); ok {
		var cmd tea.Cmd
		a.dashboard, cmd = a.dashboard.Update(msg)
		return a, cmd
	}

	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		a.width, a.height = wm.Width, wm.Height
		var cmd1, cmd2, cmd3 tea.Cmd
		if a.cmdbar.Active() {
			a.dashboard, cmd1 = a.dashboard.Update(tea.WindowSizeMsg{Width: wm.Width, Height: wm.Height - 5})
		} else {
			a.dashboard, cmd1 = a.dashboard.Update(wm)
		}
		a.cmdbar, cmd2 = a.cmdbar.Update(wm)
		a.wizard, cmd3 = a.wizard.Update(wm)
		a.connect = a.connect.WithSize(wm.Width, wm.Height)
		a.welcome.width, a.welcome.height = wm.Width, wm.Height
		return a, tea.Batch(cmd1, cmd2, cmd3)
	}

	if a.mode == modeWelcome {
		if _, ok := msg.(welcomeBackMsg); ok {
			a.mode = a.previousMode
			return a, nil
		}
		if _, ok := msg.(welcomeConnectMsg); ok {
			a.showConnect = true
			a.connect = newConnectModel()
			return a, a.connect.Init()
		}
		if _, ok := msg.(welcomeLogsMsg); ok {
			a.showLogs = true
			a.logs = logsview.New(a.dataDir, a.width, max(a.height-3, 3))
			return a, nil
		}
		if sub, ok := msg.(welcomeRestartMsg); ok {
			a.restartProfile = sub.profileName
			a.restartOnboard = sub.onboard
			a.restartNoAgent = sub.noAgent
			return a, tea.Quit
		}
		if act, ok := msg.(welcomeStartActiveMsg); ok {
			if act.profileName == "" {
				if a.dataDir == "./data" || a.dataDir == "data" {
					if isProfileOnboarded("") {
						a.mode = modeDashboard
					} else {
						a.previousMode = modeWelcome
						a.mode = modeOnboarding
						a.wizard = onboarding.NewWizard(a.ctx, a.dataDir, a.runningAgent)
						a.wizard = a.wizard.WithSize(a.width, a.height)
					}
					return a, nil
				}
			} else {
				root, _ := profile.Root()
				targetDir := filepath.Join(root, act.profileName)
				if a.dataDir == targetDir {
					if isProfileOnboarded(act.profileName) {
						a.mode = modeDashboard
					} else {
						a.previousMode = modeWelcome
						a.mode = modeOnboarding
						a.wizard = onboarding.NewWizard(a.ctx, a.dataDir, a.runningAgent)
						a.wizard = a.wizard.WithSize(a.width, a.height)
					}
					return a, nil
				}
			}
			a.restartProfile = act.profileName
			return a, tea.Quit
		}
		var cmd tea.Cmd
		a.welcome, cmd = a.welcome.Update(msg)
		return a, cmd
	}

	// Always let this reach the dashboard, regardless of mode or which
	// overlay (if any) is open — it's a self-rescheduling tick, and unlike
	// one-shot messages, if any of the blocks below ever swallowed it
	// without forwarding, the reschedule would never happen and the
	// Workers tab's live poll would die permanently, not just pause.
	if _, ok := msg.(dashboard.RefreshMsg); ok {
		var cmd tea.Cmd
		a.dashboard, cmd = a.dashboard.Update(msg)
		return a, cmd
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

	if a.showConnect {
		if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
			a.showConnect = false
			return a, nil
		}
		if sub, ok := msg.(connectSubmitMsg); ok {
			c := client.NewHTTP(sub.coord, sub.adminToken)
			a.dashboard = dashboard.New(c, sub.coord)
			a.connected = true
			a.showConnect = false
			return a, nil
		}
		var cmd tea.Cmd
		a.connect, cmd = a.connect.Update(msg)
		return a, cmd
	}

	if sub, ok := msg.(cmdbar.SubmitMsg); ok {
		// restore dashboard height before running
		a.dashboard, _ = a.dashboard.Update(tea.WindowSizeMsg{Width: a.width, Height: a.height})
		return a.runCommand(sub.Command)
	}

	if _, ok := msg.(onboarding.BackToDashboardMsg); ok {
		if !isProfileOnboarded(profile.Active()) || a.previousMode == modeWelcome {
			a.mode = modeWelcome
		} else {
			a.mode = modeDashboard
		}
		return a, nil
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
		typingInWizard := a.mode == modeOnboarding && a.wizard.CapturesTextInput()
		switch key.String() {
		case "ctrl+c":
			// Always an emergency exit, even while typing — unlike "q" and
			// "/", a text field would never want to consume ctrl+c as
			// literal input, so there's no case where suppressing it here
			// helps.
			return a, tea.Quit
		case "q":
			if !typingInWizard {
				return a, tea.Quit
			}
		case "/":
			if typingInWizard {
				break // let it reach the wizard's own text field below, literally
			}
			var cmd tea.Cmd
			a.cmdbar, cmd = a.cmdbar.Activate()
			// resize dashboard height down to leave room for the cmdbar box
			a.dashboard, _ = a.dashboard.Update(tea.WindowSizeMsg{Width: a.width, Height: a.height - 5})
			return a, cmd
		}
	}

	if a.mode == modeOnboarding {
		if _, ok := msg.(onboarding.StartConfirmedMsg); ok {
			// Update the wizard's state first, capturing its command
			var wizardCmd tea.Cmd
			a.wizard, wizardCmd = a.wizard.Update(msg)

			_, confirmed, nodeAgent, _, startErr := a.wizard.Result()
			if confirmed {
				if startErr == nil && nodeAgent != nil {
					// The wizard already closed whatever agent used to be
					// running here (see Wizard.closeExisting) before it
					// built this one — this is where App takes ownership of
					// its replacement.
					a.runningAgent = nodeAgent
					go func() {
						_ = nodeAgent.Start(a.ctx)
					}()
				}

				// Initialize dashboard client pointing to local agent
				coord := "http://127.0.0.1:8080"
				adminToken := nodeident.LoadToken(a.dataDir, "admin.token")
				isWorker := adminToken == "" && nodeident.LoadToken(a.dataDir, "node.token") != ""
				if adminToken != "" {
					c := client.NewHTTP(coord, adminToken)
					a.dashboard = dashboard.New(c, coord)
					a.connected = true
				} else {
					a.dashboard = dashboard.New(client.New(), "")
					a.connected = false
					a.isWorker = isWorker
				}

				a.mode = modeDashboard
				a.dashboard, _ = a.dashboard.Update(tea.WindowSizeMsg{Width: a.width, Height: a.height})
				return a, a.dashboard.Init()
			}

			// If not confirmed (meaning it's the first enter and starting bootstrap),
			// propagate the wizard's startNode cmd so that the starting screen runs!
			return a, wizardCmd
		}
	}

	var cmd tea.Cmd
	switch a.mode {
	case modeDashboard:
		a.dashboard, cmd = a.dashboard.Update(msg)
	case modeOnboarding:
		a.wizard, cmd = a.wizard.Update(msg)
	}
	return a, cmd
}

// runCommand handles a submitted /command.
func (a App) runCommand(command string) (tea.Model, tea.Cmd) {
	command = strings.TrimPrefix(command, "/")

	if name, ok := strings.CutPrefix(command, "profile "); ok {
		if name = strings.TrimSpace(name); name != "" && profile.Use(name) == nil {
			a.restartProfile = name
			return a, tea.Quit
		}
		return a, nil
	}

	switch command {
	case "onboard":
		a.mode = modeOnboarding
		a.wizard = onboarding.NewWizard(a.ctx, a.dataDir, a.runningAgent)
		a.wizard = a.wizard.WithSize(a.width, a.height)
		return a, a.wizard.Init()
	case "logs":
		a.showLogs = true
		a.logs = logsview.New(a.dataDir, a.width, max(a.height-3, 3))
	case "connect":
		a.showConnect = true
		a.connect = newConnectModel()
		return a, a.connect.Init()
	case "profile":
		a.previousMode = a.mode
		a.mode = modeWelcome
		a.welcome = newWelcomeModel()
		a.welcome.fromDashboard = true
		a.welcome.profiles, _ = profile.List()
		a.welcome.profileCursor = 0
		a.welcome.profileOffset = 0
		a.welcome.subMode = 1
		a.welcome.width, a.welcome.height = a.width, a.height
		return a, nil
	}
	return a, nil
}

// notConnectedView is shown instead of dashboard content until a real
// coordinator is connected — replaces silently showing Stub's fake data,
// which gave no indication the dashboard wasn't actually talking to
// anything. isWorker gets a different message: a worker has no
// coordinator of its own to default to, which isn't the same situation
// as a coordinator machine that just hasn't connected yet.
func (a App) notConnectedView() string {
	if a.isWorker {
		return style.Title.Render("Not available") + "\n\n" +
			style.Help.Render("This node is a worker — it doesn't run a coordinator.") + "\n" +
			style.Help.Render("Use /connect to view a coordinator elsewhere on the network.")
	}
	return style.Title.Render("Not connected") + "\n\n" +
		style.Help.Render("No coordinator connected yet.") + "\n" +
		style.Help.Render("Use /connect to connect to one.")
}

func (a App) title() string {
	switch a.mode {
	case modeOnboarding:
		return "EdgeGrid Setup"
	default:
		title := "EdgeGrid Dashboard"
		if c := a.dashboard.Coord(); c != "" {
			title += "  ·  " + c
		}
		return title
	}
}

func (a App) View() string {
	if a.showLogs {
		header := style.HeaderBar.Width(max(a.width, 0)).Render(a.title())
		footer := a.renderSystemFooter()
		bodyHeight := max(a.height-lipgloss.Height(header)-lipgloss.Height(footer), 1)
		placedBody := lipgloss.Place(a.width, bodyHeight, lipgloss.Center, lipgloss.Center, a.logs.View())
		return lipgloss.JoinVertical(lipgloss.Left, header, placedBody, footer)
	}

	if a.showConnect {
		header := style.HeaderBar.Width(max(a.width, 0)).Render(a.title())
		footer := a.renderSystemFooter()
		a.connect = a.connect.WithSize(a.width, a.height)
		bodyHeight := max(a.height-lipgloss.Height(header)-lipgloss.Height(footer), 1)
		placedBody := lipgloss.Place(a.width, bodyHeight, lipgloss.Center, lipgloss.Center, a.connect.View())
		return lipgloss.JoinVertical(lipgloss.Left, header, placedBody, footer)
	}

	if a.mode == modeWelcome {
		header := style.HeaderBar.Width(max(a.width, 0)).Render("EdgeGrid Launcher")
		footer := a.renderSystemFooter()
		bodyHeight := max(a.height-lipgloss.Height(header)-lipgloss.Height(footer), 1)
		placedBody := lipgloss.Place(a.width, bodyHeight, lipgloss.Center, lipgloss.Center, a.welcome.View())
		return lipgloss.JoinVertical(lipgloss.Left, header, placedBody, footer)
	}

	header := style.HeaderBar.Width(max(a.width, 0)).Render(a.title())
	if a.mode == modeOnboarding && a.wizard.StepLabel() != "" {
		header = lipgloss.JoinHorizontal(lipgloss.Top, header, style.StepLabel.Render(a.wizard.StepLabel()))
	}

	var footer string
	if a.cmdbar.Active() {
		footer = a.cmdbar.View()
	} else {
		footer = a.renderSystemFooter()
	}

	var body string
	switch {
	case a.mode == modeOnboarding:
		body = a.wizard.View()
	case !a.connected:
		body = a.notConnectedView()
	default:
		body = a.dashboard.View()
	}

	if a.width <= 0 || a.height <= 0 {
		return header + "\n\n" + body + "\n\n" + footer
	}

	bodyHeight := max(a.height-lipgloss.Height(header)-lipgloss.Height(footer), 1)
	vAlign := lipgloss.Center
	if a.mode == modeDashboard {
		vAlign = lipgloss.Top
	}
	placedBody := lipgloss.Place(a.width, bodyHeight, lipgloss.Center, vAlign, body)

	return lipgloss.JoinVertical(lipgloss.Left, header, placedBody, footer)
}

type systemStatsTickMsg struct{}

func tickSystemStats() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return systemStatsTickMsg{}
	})
}

var (
	prevIdle, prevTotal uint64
)

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
			var total uint64
			var idle uint64
			for i := 1; i < len(fields); i++ {
				val, _ := strconv.ParseUint(fields[i], 10, 64)
				total += val
				if i == 4 {
					idle = val
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
	return 0.15
}

func renderSingleCharMeter(val float64) string {
	// Unicode vertical block elements for single-char resolution: empty to full
	bars := []rune{' ', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	idx := int(val * float64(len(bars)))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(bars) {
		idx = len(bars) - 1
	}

	color := "42" // green
	if val > 0.8 {
		color = "196" // red
	} else if val > 0.5 {
		color = "214" // orange
	}

	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(string(bars[idx]))
}

func (a App) renderSystemFooter() string {
	profileName := profile.Active()
	if profileName == "" {
		profileName = "default"
	}

	timeStr := time.Now().Format("15:04:05")

	cpu := getCPUUsage()
	mem := getMemUsage()

	// Fixed-width %5.1f%% formatting ensures the string length is always exactly 5 chars, eliminating jitter layout shifts
	cpuStr := fmt.Sprintf("CPU: %5.1f%%", cpu*100)
	memStr := fmt.Sprintf("RAM: %5.1f%%", mem*100)

	cpuMeter := renderSingleCharMeter(cpu)
	memMeter := renderSingleCharMeter(mem)

	var helpKeys string
	switch {
	case a.cmdbar.Active():
		helpKeys = "enter run  esc cancel"
	case a.mode == modeWelcome:
		helpKeys = "↑/↓/j/k Nav  enter Select  ctrl+c Quit"
	case a.mode == modeOnboarding:
		helpKeys = "esc Back/Cancel  ctrl+c Quit"
	case !a.connected:
		helpKeys = "/ Command  q Quit"
	default:
		helpKeys = "/ Command  esc Back  q Quit"
	}

	profileStyle := lipgloss.NewStyle().Foreground(style.Accent).Bold(true)
	statsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("86"))
	timeStyle := lipgloss.NewStyle().Foreground(style.Muted)

	leftSection := fmt.Sprintf(" %s %s ", lipgloss.NewStyle().Background(lipgloss.Color("239")).Foreground(lipgloss.Color("255")).Render(" EDGEGRID "), profileStyle.Render(" ⧉ profile:"+profileName))
	middleSection := fmt.Sprintf(" %s %s  %s %s ", cpuMeter, statsStyle.Render(cpuStr), memMeter, statsStyle.Render(memStr))
	rightSection := fmt.Sprintf(" %s  %s ", timeStyle.Render(timeStr), lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(lipgloss.Color("255")).Render(" "+helpKeys+" "))

	totalLen := lipgloss.Width(leftSection) + lipgloss.Width(middleSection) + lipgloss.Width(rightSection)
	gap := a.width - totalLen
	if gap < 2 {
		gap = 2
	}

	return lipgloss.NewStyle().
		Background(lipgloss.Color("235")).
		Width(a.width).
		Render(leftSection + strings.Repeat(" ", gap-2) + middleSection + " " + rightSection)
}
