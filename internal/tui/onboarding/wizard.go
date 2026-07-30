// Package onboarding is the setup wizard a node runs through once on first
// start: pick a role, pick a coordinator to join (skipped for a primary
// coordinator, since it has nothing to join), wait for approval, then hand
// off into the normal headless run loop. All three roles fully start a
// real node (see startNode / Result) — a primary coordinator's Ready
// screen requires a confirming enter before startNode runs, since nothing
// has happened yet; a secondary coordinator/worker's join already runs
// startNode as soon as a coordinator address is submitted, since that's
// the point where there's something to actually attempt.
//
// Wizard renders content only — the "/" command bar, logs overlay, and
// global quit key all live one level up in internal/tui/app, shared with
// the dashboard, so there's one implementation of that chrome, not two.
package onboarding

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/agent"
	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

type step int

const (
	stepRole step = iota
	stepCoordinator
	stepJoinStatus
	stepDone
	stepStarting
)

// StepLabels exposes each step's chrome label, keyed by the same step type
// used internally — app.App reads this to render the step indicator
// without needing to duplicate the step names itself.
var StepLabels = map[step]string{
	stepRole:        "Step 1 — Role",
	stepCoordinator: "Step 2 — Coordinator",
	stepJoinStatus:  "Step 3 — Joining",
	stepDone:        "Step 4 — Ready",
	stepStarting:    "Starting",
}

var helpText = map[step]string{
	stepRole:        "↑/↓ choose   enter select   / command   q quit",
	stepCoordinator: "enter confirm   / command   q quit",
	stepJoinStatus:  "/ command   q quit",
	stepDone:        "enter start   / command   q quit",
	stepStarting:    "q quit",
}

var roleLabel = map[Role]string{
	RolePrimaryCoordinator:   "Primary coordinator",
	RoleSecondaryCoordinator: "Secondary coordinator",
	RoleWorker:               "Worker",
}

// startResult is written once by the background startup goroutine, then
// read by the caller after Run() returns — safe without a mutex because
// the goroutine's channel send (agentEventMsg{done: true}) happens-after
// the write and the read happens-after the receive.
type startResult struct {
	agent *agent.Agent
	cfg   *config.Config
	err   error
}

// Wizard is the onboarding flow's content model — see package doc for what
// it deliberately doesn't own.
type Wizard struct {
	ctx     context.Context
	dataDir string
	step    step
	width   int
	height  int

	role        roleModel
	coordinator coordinatorModel
	joinStatus  joinStatusModel
	done        doneModel
	starting    startingModel

	chosenRole Role
	confirmed  bool
	started    bool
	result     *startResult
	eventCh    chan agentEventMsg
	config     *config.Config
	cancelFn   context.CancelFunc

	// existingAgent is any node agent already running in this process
	// (non-nil when /onboard is run against an already-provisioned node,
	// e.g. from the dashboard). It's closed the moment this wizard actually
	// commits to starting a replacement — see closeExisting.
	existingAgent *agent.Agent
}

func NewWizard(ctx context.Context, dataDir string, existingAgent *agent.Agent) Wizard {
	cfg := config.LoadConfig()
	cfg.DataDir = dataDir
	config.ApplyProfileSettings(cfg)
	return Wizard{
		ctx:           ctx,
		dataDir:       dataDir,
		step:          stepRole,
		role:          newRoleModel(cfg),
		config:        cfg,
		existingAgent: existingAgent,
		// coordinator and joinStatus are (re)built with real role/address
		// context once roleChosenMsg / coordinatorChosenMsg actually fire —
		// see Update.
	}
}

// closeExisting stops any node agent already running in this process before
// a new one is started. Re-onboarding an already-provisioned node must
// replace its agent, not run two side by side against the same ports/tsnet
// identity/data dir. Safe to call more than once — nils the field so a
// second call (defensive call sites) is a no-op.
func (w *Wizard) closeExisting() {
	if w.existingAgent != nil {
		w.existingAgent.Close()
		w.existingAgent = nil
	}
}

func (w Wizard) WithSize(width, height int) Wizard {
	w.width = width
	w.height = height
	w.role = w.role.WithSize(width, height)
	w.coordinator = w.coordinator.WithSize(width, height)
	return w
}

// Result reports what happened, for main.go to act on after Run() returns.
// confirmed is false if the user quit before reaching Done. If confirmed
// and role is primary coordinator, agent/cfg/err report how startup went.
func (w Wizard) Result() (role Role, confirmed bool, startedAgent *agent.Agent, cfg *config.Config, err error) {
	if w.result != nil {
		return w.chosenRole, w.confirmed, w.result.agent, w.result.cfg, w.result.err
	}
	return w.chosenRole, w.confirmed, nil, nil, nil
}

// StepLabel and HelpText let app.App build its chrome without duplicating
// per-step knowledge.
func (w Wizard) StepLabel() string { return StepLabels[w.step] }

// CapturesTextInput reports whether the current step has a free-form text
// field focused (right now, only the coordinator-address input) — app.App
// checks this before treating "/" as the global command-bar shortcut, so
// typing a URL like "http://..." doesn't get hijacked partway through.
func (w Wizard) CapturesTextInput() bool { return w.step == stepCoordinator }

func (w Wizard) HelpText() string {
	if w.step == stepDone && w.started {
		return "enter finish   / command   q quit"
	}
	return helpText[w.step]
}

func (w Wizard) Init() tea.Cmd { return w.role.Init() }

// waitForAgentEvent blocks (in its own bubbletea-managed goroutine) until
// the background startup goroutine sends the next progress event — the
// standard bubbletea pattern for streaming updates from external work.
func waitForAgentEvent(ch chan agentEventMsg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// startNode runs the real node startup in the background, for any role —
// agent.NewAgentWithLogging already branches on cfg.EmbedNATS/cfg.JoinURL/
// cfg.Client.Enabled internally to do the right thing (bootstrap, or join
// and wait for approval via requestAndWaitForApproval), so this one
// function covers primary coordinator, secondary coordinator, and worker
// alike — the caller just sets cfg's role flags first. Pushes progress
// (including tsnet's login URL, which otherwise only ever reaches
// log.Printf) into the returned channel.
func startNode(ctx context.Context, cfg *config.Config, result *startResult) chan agentEventMsg {
	ch := make(chan agentEventMsg, 8)
	go func() {
		config.ApplyProfileSettings(cfg) // pick up Configure Settings / prior onboarding saves
		result.cfg = cfg

		onProgress := func(line string) {
			ch <- agentEventMsg{line: line}
		}
		// Same NewAgentWithLogging the plain headless start uses — one
		// "how a node starts" implementation, not a second copy of it here.
		nodeAgent, _, err := agent.NewAgentWithLogging(ctx, cfg, onProgress, true)
		if err != nil {
			result.err = err
			ch <- agentEventMsg{err: err, done: true}
			return
		}
		result.agent = nodeAgent
		ch <- agentEventMsg{line: "node ready", done: true}
	}()
	return ch
}

func (w Wizard) Update(msg tea.Msg) (Wizard, tea.Cmd) {
	// TickMsg (role.go's animation clock, shared with coordinatorModel)
	// deliberately has no special case here — it used to short-circuit to
	// only stepRole/stepCoordinator and return a nil Cmd for every other
	// step, which silently killed the whole tick chain the moment a tick
	// arrived while on stepJoinStatus/stepDone/stepStarting (nothing else
	// ever restarts it except the esc-handler's explicit tickCmd() calls
	// below). The bottom switch already routes TickMsg to whichever
	// screen is actually active — same result for stepRole/stepCoordinator,
	// but harmless instead of fatal for every other step.

	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		w.width = wm.Width
		w.height = wm.Height
		w.role, _ = w.role.Update(wm)
		w.coordinator = w.coordinator.WithSize(wm.Width, wm.Height)
	}

	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
		switch w.step {
		case stepRole:
			if w.cancelFn != nil {
				w.cancelFn()
				w.cancelFn = nil
			}
			return w, func() tea.Msg { return BackToDashboardMsg{} }
		case stepCoordinator:
			w.step = stepRole
			return w, tickCmd()
		case stepJoinStatus:
			if w.cancelFn != nil {
				w.cancelFn()
				w.cancelFn = nil
			}
			w.step = stepCoordinator
			return w, tickCmd()
		case stepStarting:
			if w.cancelFn != nil {
				w.cancelFn()
				w.cancelFn = nil
			}
			w.step = stepRole
			return w, tickCmd()
		case stepDone:
			if w.cancelFn != nil {
				w.cancelFn()
				w.cancelFn = nil
			}
			if w.chosenRole == RolePrimaryCoordinator {
				w.step = stepRole
				return w, tickCmd()
			} else {
				w.step = stepCoordinator
				return w, tickCmd()
			}
		}
	}

	switch msg := msg.(type) {
	case BackToDashboardMsg:
		return w, func() tea.Msg { return msg } // bubble up to app.go
	case agentEventMsg:
		if w.step == stepStarting {
			w.starting.applyEvent(msg)
		}
		if w.step == stepJoinStatus && msg.line != "" {
			w.joinStatus = w.joinStatus.withLogLine(msg.line)
			if idx := strings.Index(msg.line, "https://login.tailscale.com"); idx != -1 {
				url := strings.TrimSpace(msg.line[idx:])
				w.joinStatus = w.joinStatus.withLoginURL(url)
			}
		}
		if !msg.done {
			return w, waitForAgentEvent(w.eventCh)
		}
		if msg.err != nil {
			if w.step == stepJoinStatus {
				w.joinStatus = w.joinStatus.withError(msg.err)
			}
			// stepStarting already shows msg.err via applyEvent above.
			// Either way, this attempt is over — nothing left to wait on.
			return w, nil
		}
		// Real startup finished — show what was actually built (IP, node
		// ID, admin token) instead of immediately quitting, so there's
		// something on screen to read/copy before the TUI exits. Same
		// screen regardless of whether this was the primary-coordinator
		// path (stepStarting) or the join path (stepJoinStatus) below —
		// agent.NewAgentWithLogging already fully built the agent either
		// way, nothing left to distinguish at this point.
		var ip, nodeID, adminToken string
		if w.result != nil && w.result.agent != nil {
			ip = w.result.agent.TailscaleIP()
			nodeID = w.result.agent.NodeID()
			adminToken = w.result.agent.AdminToken()
		}
		w.done = newDoneModel(roleLabel[w.chosenRole], nodeID, ip, adminToken)
		w.started = true
		w.step = stepDone
		return w, nil
	case roleChosenMsg:
		w.chosenRole = msg.role
		if msg.role == RolePrimaryCoordinator {
			// Boot the primary coordinator agent immediately instead of previewing first
			w.closeExisting()
			w.step = stepStarting
			w.starting = newStartingModel()
			w.result = &startResult{}
			ctx, cancel := context.WithCancel(w.ctx)
			w.cancelFn = cancel
			w.eventCh = startNode(ctx, w.config, w.result)
			return w, tea.Batch(w.starting.Init(), waitForAgentEvent(w.eventCh))
		}
		w.coordinator = newCoordinatorModel(roleLabel[msg.role], w.config.JoinURL)
		w.step = stepCoordinator
		return w, nil
	case coordinatorChosenMsg:
		w.config.JoinURL = msg.addr
		switch w.chosenRole {
		case RoleSecondaryCoordinator:
			w.config.Server.Enabled = true
			w.config.Client.Enabled = false
			w.config.EmbedNATS = true
		case RoleWorker:
			w.config.Server.Enabled = false
			w.config.Client.Enabled = true
			w.config.EmbedNATS = false
		}
		w.joinStatus = newJoinStatusModel(roleLabel[w.chosenRole], msg.addr)
		w.step = stepJoinStatus
		w.closeExisting()
		w.result = &startResult{}
		ctx, cancel := context.WithCancel(w.ctx)
		w.cancelFn = cancel
		w.eventCh = startNode(ctx, w.config, w.result)
		return w, tea.Batch(w.joinStatus.Init(), waitForAgentEvent(w.eventCh))
	case StartConfirmedMsg:
		if w.started {
			// Second appearance of stepDone, after real startup already
			// finished — nothing left to do but hand off.
			w.confirmed = true
			return w, tea.Quit
		}
		if w.chosenRole != RolePrimaryCoordinator {
			// Shouldn't happen — secondary/worker now kick off startNode
			// from coordinatorChosenMsg above, before ever reaching a
			// not-yet-started stepDone. Kept as a defensive no-op.
			return w, nil
		}
		w.closeExisting()
		w.step = stepStarting
		w.starting = newStartingModel()
		w.result = &startResult{}
		ctx, cancel := context.WithCancel(w.ctx)
		w.cancelFn = cancel
		w.eventCh = startNode(ctx, w.config, w.result)
		return w, tea.Batch(w.starting.Init(), waitForAgentEvent(w.eventCh))
	}

	var cmd tea.Cmd
	switch w.step {
	case stepRole:
		w.role, cmd = w.role.Update(msg)
	case stepCoordinator:
		w.coordinator, cmd = w.coordinator.Update(msg)
	case stepJoinStatus:
		w.joinStatus, cmd = w.joinStatus.Update(msg)
	case stepDone:
		w.done, cmd = w.done.Update(msg)
	case stepStarting:
		w.starting, cmd = w.starting.Update(msg)
	}
	return w, cmd
}

func (w Wizard) View() string {
	termWidth := w.width
	termHeight := w.height
	if termWidth <= 0 {
		termWidth = 80
	}
	if termHeight <= 0 {
		termHeight = 24
	}

	// 3-Pane Division for setup / onboarding boot phases
	if w.step == stepStarting || w.step == stepJoinStatus || w.step == stepDone {
		leftWidth := int(float64(termWidth-8) * 0.32)
		midWidth := int(float64(termWidth-8) * 0.36)
		rightWidth := termWidth - leftWidth - midWidth - 10
		if leftWidth < 28 {
			leftWidth = 28
		}
		if midWidth < 28 {
			midWidth = 28
		}
		if rightWidth < 20 {
			rightWidth = 20
		}

		// ==================== PANE 1 (Leftmost): Status & Progress ====================
		var leftLines []string
		leftLines = append(leftLines,
			style.Title.Render(" [ DEPLOYMENT PROCESS ] "),
			"",
		)

		greenCheck := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render("✔")
		activeArrow := lipgloss.NewStyle().Foreground(style.Accent).Bold(true).Render("›")
		mutedDot := lipgloss.NewStyle().Foreground(style.Muted).Render("┄")

		// 1. Role Select
		leftLines = append(leftLines, fmt.Sprintf(" %s  Node Role Selection", greenCheck))

		// 2. Settings Config
		if w.chosenRole == RolePrimaryCoordinator {
			leftLines = append(leftLines, fmt.Sprintf(" %s  Configure Settings (Auto)", greenCheck))
		} else {
			leftLines = append(leftLines, fmt.Sprintf(" %s  Configure Join Address", greenCheck))
		}

		// 3. Bootstrap Node Agent
		if w.step == stepDone {
			leftLines = append(leftLines, fmt.Sprintf(" %s  Bootstrap Node Agent", greenCheck))
		} else {
			leftLines = append(leftLines, fmt.Sprintf(" %s  Bootstrap Node Agent", activeArrow))
		}

		// 4. Checking Tailscale auth URLs
		hasSSO := false
		var loginURL string
		if w.step == stepStarting {
			for _, l := range w.starting.lines {
				if strings.Contains(l, "https://login.tailscale.com") {
					hasSSO = true
					idx := strings.Index(l, "https://login.tailscale.com")
					if idx != -1 {
						loginURL = strings.TrimSpace(l[idx:])
					}
				}
			}
		} else if w.step == stepJoinStatus {
			if w.joinStatus.loginURL != "" {
				hasSSO = true
				loginURL = w.joinStatus.loginURL
			}
		}

		if w.step == stepDone {
			leftLines = append(leftLines, fmt.Sprintf(" %s  Network Security (SSO)", greenCheck))
		} else if hasSSO {
			leftLines = append(leftLines, fmt.Sprintf(" %s  SSO Login Required", activeArrow))
		} else {
			leftLines = append(leftLines, fmt.Sprintf(" %s  Network Security (SSO)", mutedDot))
		}

		// 5. Final handoff
		if w.step == stepDone {
			leftLines = append(leftLines, fmt.Sprintf(" %s  Start Cluster Dashboard", activeArrow))
		} else {
			leftLines = append(leftLines, fmt.Sprintf(" %s  Start Cluster Dashboard", mutedDot))
		}

		leftLines = append(leftLines,
			"",
			style.Help.Render(strings.Repeat("┄", leftWidth-4)),
			"",
		)

		// Loading Spinner and Gradient Progress Bar Loader
		var progress float64
		if w.step == stepStarting {
			progress = w.starting.Progress
		} else if w.step == stepJoinStatus {
			progress = 0.65
		} else {
			progress = 1.0
		}

		progressPct := int(progress * 100)
		var progressSpinner string
		if w.step == stepStarting {
			progressSpinner = w.starting.spinner.View()
		} else if w.step == stepJoinStatus {
			progressSpinner = w.joinStatus.spinner.View()
		} else {
			progressSpinner = greenCheck
		}

		leftLines = append(leftLines,
			fmt.Sprintf("%s  Booting node: %d%%", progressSpinner, progressPct),
			renderStartingProgressBar(progress, leftWidth-6),
		)

		leftContent := lipgloss.JoinVertical(lipgloss.Left, leftLines...)

		// ==================== PANE 2 (Middle): Action Required / Login SSO / Credentials ====================
		var midLines []string
		midLines = append(midLines,
			style.Title.Render(" [ IMMEDIATE STEPS ] "),
			"",
		)

		if w.step == stepStarting || w.step == stepJoinStatus {
			if loginURL != "" {
				midLines = append(midLines,
					lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true).Render("AUTHENTICATION REQUIRED:"),
					"",
					style.Help.Render("Please sign up / log in to Tailscale using the link below:"),
					"",
					style.Selected.Render(loginURL),
					"",
					style.Help.Render("Waiting for authorization..."),
				)
			} else {
				midLines = append(midLines,
					style.Help.Render("No immediate action required."),
					"",
					style.Help.Render("Node configuration active."),
					style.Help.Render("Setting up secure overlay networks..."),
				)
			}
		} else {
			// stepDone
			midLines = append(midLines,
				lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render("BOOTSTRAP COMPLETED:"),
				"",
				"Role:     "+roleLabel[w.chosenRole],
			)
			if w.result != nil && w.result.agent != nil {
				if ip := w.result.agent.TailscaleIP(); ip != "" {
					midLines = append(midLines, "IP Addr:  "+ip)
				}
				if nodeID := w.result.agent.NodeID(); nodeID != "" {
					midLines = append(midLines, "Node ID:  "+nodeID)
				}
				if token := w.result.agent.AdminToken(); token != "" {
					midLines = append(midLines,
						"",
						"Admin token:",
						lipgloss.NewStyle().Foreground(style.Accent).Bold(true).Render(token),
					)
				}
			}
			midLines = append(midLines,
				"",
				style.Help.Render("Press Enter to launch dashboard."),
			)
		}
		midContent := lipgloss.JoinVertical(lipgloss.Left, midLines...)

		// ==================== PANE 3 (Rightmost): Live Logs / Rocket ASCII Art ====================
		var rightLines []string
		if w.step == stepDone {
			rightLines = append(rightLines,
				style.Title.Render(" [ ROCKET READY ] "),
				"",
				lipgloss.NewStyle().Foreground(style.Accent).Render(rocketArt),
			)
		} else {
			rightLines = append(rightLines,
				style.Title.Render(" [ LIVE OUTPUT LOGS ] "),
				"",
			)

			// Show last 7 lines of logs
			var logsList []string
			if w.step == stepStarting {
				logsList = w.starting.lines
			} else if w.step == stepJoinStatus {
				logsList = w.joinStatus.lines
			}

			startIdx := len(logsList) - 8
			if startIdx < 0 {
				startIdx = 0
			}
			for i := startIdx; i < len(logsList); i++ {
				line := logsList[i]
				if len(line) > rightWidth-2 {
					line = line[:rightWidth-5] + "..."
				}
				rightLines = append(rightLines, style.Help.Render(line))
			}
		}
		rightContent := lipgloss.JoinVertical(lipgloss.Left, rightLines...)

		// Align columns inside styled borders
		leftBox := lipgloss.NewStyle().
			Width(leftWidth).
			Padding(1, 1).
			Render(leftContent)

		midBox := lipgloss.NewStyle().
			Width(midWidth).
			Padding(1, 1).
			Render(midContent)

		rightBox := lipgloss.NewStyle().
			Width(rightWidth).
			Padding(1, 1).
			Render(rightContent)

		dividerLines := make([]string, max(termHeight-8, 10))
		for i := 0; i < len(dividerLines); i++ {
			dividerLines[i] = "│"
		}
		divider := lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Join(dividerLines, "\n"))

		body := lipgloss.JoinHorizontal(lipgloss.Top, leftBox, divider, midBox, divider, rightBox)

		outerStyle := lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(style.Accent).
			Padding(1, 1).
			Width(termWidth - 6).
			Height(termHeight - 4)

		return lipgloss.Place(termWidth, termHeight, lipgloss.Center, lipgloss.Center, outerStyle.Render(body))
	}

	cardWidth := termWidth
	cardHeight := termHeight

	w.role = w.role.WithSize(cardWidth, cardHeight)
	w.coordinator = w.coordinator.WithSize(cardWidth, cardHeight)
	w.joinStatus = w.joinStatus.WithSize(cardWidth, cardHeight)

	var body string
	switch w.step {
	case stepRole:
		body = w.role.View()
	case stepCoordinator:
		body = w.coordinator.View()
	}

	outerStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(style.Accent).
		Padding(1, 2).
		Width(cardWidth - 6).
		Height(cardHeight - 4)

	card := outerStyle.Render(body)

	return lipgloss.Place(termWidth, termHeight, lipgloss.Center, lipgloss.Center, card)
}

func renderStartingProgressBar(progress float64, width int) string {
	if width < 5 {
		width = 5
	}
	filled := int(progress * float64(width))
	if filled > width {
		filled = width
	}
	empty := width - filled

	var sb strings.Builder
	sb.WriteString("[")

	// Smooth horizontal gradient for the filled block segment
	for i := 0; i < filled; i++ {
		t := 0.0
		if width > 1 {
			t = float64(i) / float64(width-1)
		}
		var r, g, b float64
		if t < 0.5 {
			t2 := t * 2.0
			r = 0.0 + (139.0-0.0)*t2
			g = 242.0 + (92.0-242.0)*t2
			b = 254.0 + (246.0-254.0)*t2
		} else {
			t2 := (t - 0.5) * 2.0
			r = 139.0 + (244.0-139.0)*t2
			g = 92.0 + (63.0-92.0)*t2
			b = 246.0 + (94.0-246.0)*t2
		}
		hexStr := fmt.Sprintf("#%02x%02x%02x", int(r), int(g), int(b))
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(hexStr)).Render("■"))
	}

	if empty > 0 {
		emptyStr := lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Repeat("░", empty))
		sb.WriteString(emptyStr)
	}

	sb.WriteString("]")
	return sb.String()
}
