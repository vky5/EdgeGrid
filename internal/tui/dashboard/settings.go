package dashboard

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// tsAPIFields are the three settings persisted straight to their own 0600
// files (nodeident.SaveToken), not settings.json — same convention as
// admin.token/cluster.secret, since these are the credential that lets the
// coordinator mint Tailscale auth keys (see internal/tailscaleapi). Kept as
// a package-level map so load()/persist()/focusField() share one source of
// truth for field name -> file name instead of three separate switches.
var tsAPIFields = map[string]string{
	"Tailscale API Client ID":     "ts_api_client_id",
	"Tailscale API Client Secret": "ts_api_client_secret",
	"Tailscale API Tailnet":       "ts_api_tailnet",
	"Tailscale API Tag":           "ts_api_tag",
}

// SettingsRestartMsg is emitted after a successful save when the user
// confirmed restart — app must quit so main can exec a fresh process
// (ports/executor only apply on agent start).
type SettingsRestartMsg struct{}

type settingsModel struct {
	dataDir   string
	role      string
	isPrimary bool
	width     int
	height    int

	fields []string
	idx    int
	vals   map[string]string
	input  textinput.Model

	// confirmRestart: after save, ask before killing the running coordinator.
	confirmRestart bool
	confirmYes     bool
	status         string
	dirty          bool
}

func newSettingsModel(dataDir string) settingsModel {
	m := settingsModel{
		dataDir: dataDir,
		vals:    map[string]string{},
	}
	m.input = textinput.New()
	m.input.CharLimit = 128
	m.input.Width = 32
	m.input.Prompt = ""
	m.input.PromptStyle = lipgloss.NewStyle().Foreground(style.Accent)
	m.input.TextStyle = lipgloss.NewStyle().Foreground(style.Accent).Bold(true)
	return m.load()
}

func (m settingsModel) load() settingsModel {
	m.role = config.DetectRoleHint(m.dataDir)
	s, _ := config.LoadProfileSettings(m.dataDir)

	natsPort := s.NATSPort
	if natsPort == 0 {
		natsPort = 4222
	}
	clusterPort := s.ClusterPort
	if clusterPort == 0 {
		clusterPort = 6222
	}
	apiPort := strings.TrimPrefix(s.APIPort, ":")
	if apiPort == "" {
		apiPort = "8080"
	}
	exec := s.Executor
	if exec == "" {
		exec = "training"
	}
	req := "false"
	if s.RequireApproval != nil && *s.RequireApproval {
		req = "true"
	}
	clusterName := s.ClusterName
	if clusterName == "" {
		clusterName = "edgegrid"
	}
	host := s.TailscaleHostname
	if host == "" {
		host, _ = os.Hostname()
	}
	join := s.JoinURL

	m.vals = map[string]string{
		"API Port":           apiPort,
		"NATS Port":          strconv.Itoa(natsPort),
		"Cluster Port":       strconv.Itoa(clusterPort),
		"Cluster Name":       clusterName,
		"Executor":           exec,
		"Require Approval":   req,
		"Join URL":           join,
		"Tailscale Hostname": host,
	}

	// isPrimary mirrors the definition used to gate the Tokens tab itself —
	// the coordinator that never joined anyone else, the only one that can
	// hold Tailscale API credentials. config.DetectRoleHint prefers the role
	// persisted at onboarding's role-selection step over inferring from
	// which credential files exist, so this stays accurate even for a
	// secondary coordinator whose admin.token hasn't been generated yet.
	m.isPrimary = config.DetectRoleHint(m.dataDir) == "primary"

	switch m.role {
	case "worker":
		m.fields = []string{"Executor", "Require Approval", "Join URL", "Tailscale Hostname"}
	case "secondary":
		// Join URL matters here specifically — a coordinator-role node
		// re-requests join approval on every startup using it (see
		// wizard.go's coordinatorChosenMsg), so it has to be editable if it
		// was never captured (old profiles) or the coordinator moved.
		m.fields = []string{"API Port", "NATS Port", "Cluster Port", "Cluster Name", "Join URL", "Executor", "Require Approval", "Tailscale Hostname"}
	default:
		// primary / unknown — ports + executor, no Join URL (primary never joins anyone)
		m.fields = []string{"API Port", "NATS Port", "Cluster Port", "Cluster Name", "Executor", "Require Approval", "Tailscale Hostname"}
	}
	if m.isPrimary {
		for _, name := range []string{"Tailscale API Client ID", "Tailscale API Client Secret", "Tailscale API Tailnet", "Tailscale API Tag"} {
			m.vals[name] = nodeident.LoadToken(m.dataDir, tsAPIFields[name])
			m.fields = append(m.fields, name)
		}
	}
	m.idx = 0
	m.dirty = false
	m.confirmRestart = false
	m.status = ""
	return m.focusField()
}

func (m settingsModel) focusField() settingsModel {
	if len(m.fields) == 0 {
		return m
	}
	name := m.fields[m.idx]
	// Executor is a picker — input not used for free text.
	if name == "Executor" || name == "Require Approval" {
		m.input.Blur()
		m.input.SetValue(m.vals[name])
		return m
	}
	if name == "Tailscale API Client Secret" {
		m.input.EchoMode = textinput.EchoPassword
	} else {
		m.input.EchoMode = textinput.EchoNormal
	}
	val := m.vals[name]
	m.input.SetValue(val)
	m.input.SetCursor(len(val))
	_ = m.input.Focus()
	return m
}

func (m settingsModel) saveField() settingsModel {
	if len(m.fields) == 0 {
		return m
	}
	name := m.fields[m.idx]
	if name == "Executor" || name == "Require Approval" {
		return m
	}
	m.vals[name] = strings.TrimSpace(m.input.Value())
	return m
}

// moveField steps ±1 through settings fields (wraps at ends).
func (m settingsModel) moveField(delta int) settingsModel {
	m = m.saveField()
	n := len(m.fields)
	if n == 0 {
		return m
	}
	m.idx = (m.idx + delta%n + n) % n
	return m.focusField()
}

func (m settingsModel) Init() tea.Cmd { return textinput.Blink }

func (m settingsModel) Update(msg tea.Msg) (settingsModel, tea.Cmd) {
	if m.confirmRestart {
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "left", "h", "right", "l", "tab", "shift+tab":
				m.confirmYes = !m.confirmYes
				return m, nil
			case "y":
				m.confirmYes = true
				return m, func() tea.Msg { return SettingsRestartMsg{} }
			case "n", "esc":
				m.confirmRestart = false
				m.status = "saved to settings.json · restart skipped (changes apply on next agent start)"
				return m, nil
			case "enter":
				if m.confirmYes {
					return m, func() tea.Msg { return SettingsRestartMsg{} }
				}
				m.confirmRestart = false
				m.status = "saved to settings.json · restart skipped (changes apply on next agent start)"
				return m, nil
			}
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			name := m.fields[m.idx]
			// Picker lists: move inside until the first item, then leave to previous field.
			// Never wrap to the bottom of the list (that felt like being trapped).
			if name == "Executor" {
				i := config.ExecutorIndex(m.vals["Executor"])
				if i > 0 {
					m.vals["Executor"] = config.KnownExecutors[i-1]
					m.dirty = true
					return m, nil
				}
				return m.moveField(-1), textinput.Blink
			}
			if name == "Require Approval" {
				if !isTruthy(m.vals["Require Approval"]) {
					m.vals["Require Approval"] = "true"
					m.dirty = true
					return m, nil
				}
				return m.moveField(-1), textinput.Blink
			}
			return m.moveField(-1), textinput.Blink
		case "down", "j":
			name := m.fields[m.idx]
			if name == "Executor" {
				i := config.ExecutorIndex(m.vals["Executor"])
				if i < len(config.KnownExecutors)-1 {
					m.vals["Executor"] = config.KnownExecutors[i+1]
					m.dirty = true
					return m, nil
				}
				return m.moveField(+1), textinput.Blink
			}
			if name == "Require Approval" {
				if isTruthy(m.vals["Require Approval"]) {
					m.vals["Require Approval"] = "false"
					m.dirty = true
					return m, nil
				}
				return m.moveField(+1), textinput.Blink
			}
			return m.moveField(+1), textinput.Blink
		case "left", "h", "right", "l":
			// ←/→ only move within a picker (no field leave); no wrap on executor edges.
			name := m.fields[m.idx]
			if name == "Require Approval" {
				if msg.String() == "left" || msg.String() == "h" {
					m.vals["Require Approval"] = "false"
				} else {
					m.vals["Require Approval"] = "true"
				}
				m.dirty = true
				return m, nil
			}
			if name == "Executor" {
				i := config.ExecutorIndex(m.vals["Executor"])
				if msg.String() == "left" || msg.String() == "h" {
					if i > 0 {
						m.vals["Executor"] = config.KnownExecutors[i-1]
						m.dirty = true
					}
				} else if i < len(config.KnownExecutors)-1 {
					m.vals["Executor"] = config.KnownExecutors[i+1]
					m.dirty = true
				}
				return m, nil
			}
			return m, nil
		case "ctrl+s":
			m = m.saveField()
			if err := m.persist(); err != nil {
				m.status = "save failed: " + err.Error()
				return m, nil
			}
			m.dirty = false
			// Running coordinator dashboard: force explicit restart confirm.
			m.confirmRestart = true
			m.confirmYes = false // default to Cancel so Enter is safe
			m.status = ""
			return m, nil
		case "enter":
			// Confirm current picker value and advance — never flip selection.
			name := m.fields[m.idx]
			if name != "Executor" && name != "Require Approval" {
				m = m.saveField()
			}
			if m.idx < len(m.fields)-1 {
				m.idx++
				return m.focusField(), textinput.Blink
			}
			return m, nil
		case " ", "space":
			name := m.fields[m.idx]
			if name == "Require Approval" {
				if isTruthy(m.vals["Require Approval"]) {
					m.vals["Require Approval"] = "false"
				} else {
					m.vals["Require Approval"] = "true"
				}
				m.dirty = true
				return m, nil
			}
			return m, nil
		}
	}

	// Only free-text fields go to the textinput; pickers must not be overwritten.
	if len(m.fields) > 0 {
		name := m.fields[m.idx]
		if name == "Executor" || name == "Require Approval" {
			return m, nil
		}
	}
	var cmd tea.Cmd
	prev := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != prev {
		m.dirty = true
	}
	return m, cmd
}

func isTruthy(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "true" || s == "1" || s == "yes"
}

func (m settingsModel) persist() error {
	get := func(k string) string { return strings.TrimSpace(m.vals[k]) }
	s := config.ProfileSettings{Role: m.role}
	if v := get("NATS Port"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("NATS Port: %w", err)
		}
		s.NATSPort = n
	}
	if v := get("Cluster Port"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("Cluster Port: %w", err)
		}
		s.ClusterPort = n
	}
	if v := get("API Port"); v != "" {
		s.APIPort = v
	}
	if v := get("Cluster Name"); v != "" {
		s.ClusterName = v
	}
	if v := get("Executor"); v != "" {
		if config.NormalizeExecutor(strings.ToLower(v)) == "" {
			return fmt.Errorf("executor must be one of: %s", strings.Join(config.KnownExecutors, ", "))
		}
		s.Executor = strings.ToLower(v)
	}
	if v := get("Require Approval"); v != "" {
		b := strings.EqualFold(v, "true") || v == "1"
		s.RequireApproval = &b
	}
	if v := get("Join URL"); v != "" {
		s.JoinURL = v
	}
	if v := get("Tailscale Hostname"); v != "" {
		s.TailscaleHostname = v
	}
	// Written straight to their own 0600 files, not settings.json — same
	// credential convention as admin.token, and read fresh on every mint/
	// revoke call (tailscaleapi.LoadCredentials), so this takes effect
	// immediately with no coordinator restart needed.
	for name, file := range tsAPIFields {
		if v := get(name); v != "" {
			if err := nodeident.SaveToken(m.dataDir, file, v); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	return config.SaveProfileSettings(m.dataDir, s)
}

func (m settingsModel) View() string {
	w := m.width
	if w <= 0 {
		w = 80
	}

	var b strings.Builder
	b.WriteString(style.Title.Render("SETTINGS") + "\n")
	role := m.role
	if role == "" {
		role = "node"
	}
	b.WriteString(style.Help.Render(fmt.Sprintf("profile data: %s  ·  role: %s", m.dataDir, role)) + "\n\n")

	if m.confirmRestart {
		warn := lipgloss.NewStyle().Foreground(style.Danger).Bold(true)
		b.WriteString(warn.Render("⚠  RESTART REQUIRED") + "\n\n")
		b.WriteString("Settings were saved to settings.json.\n")
		b.WriteString("Applying them restarts this process and the local agent.\n\n")
		b.WriteString(style.Help.Render("You may lose:") + "\n")
		b.WriteString("  · in-progress job log views / unsaved form state in this TUI\n")
		b.WriteString("  · brief downtime for the coordinator HTTP API and NATS\n")
		b.WriteString("  · workers may reconnect; running remote jobs keep going if possible\n\n")

		yes := " Restart now "
		no := " Stay online "
		if m.confirmYes {
			yes = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(style.Danger).Bold(true).Render(yes)
			no = lipgloss.NewStyle().Foreground(style.Muted).Render(no)
		} else {
			yes = lipgloss.NewStyle().Foreground(style.Muted).Render(yes)
			no = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(style.Accent).Bold(true).Render(no)
		}
		b.WriteString(yes + "    " + no + "\n\n")
		b.WriteString(style.Help.Render("←/→ select   enter confirm   esc/n stay"))
		return lipgloss.NewStyle().Width(w).Render(b.String())
	}

	b.WriteString(style.Help.Render("↑/↓ navigate (lists: exit at ends)   enter next   ctrl+s save") + "\n\n")

	for i, field := range m.fields {
		focused := i == m.idx
		label := field
		if focused {
			label = style.Selected.Render("› " + field)
		} else {
			label = style.Help.Render("  " + field)
		}
		b.WriteString(label + "\n")

		switch field {
		case "Executor":
			b.WriteString(renderExecutorList(m.vals["Executor"], focused) + "\n")
		case "Require Approval":
			b.WriteString(renderApprovalList(m.vals["Require Approval"], focused) + "\n")
		default:
			if focused {
				b.WriteString("  " + m.input.View() + "\n\n")
			} else {
				shown := m.vals[field]
				if field == "Tailscale API Client Secret" && shown != "" {
					shown = strings.Repeat("•", len(shown))
				}
				b.WriteString("  " + lipgloss.NewStyle().Bold(true).Render(shown) + "\n\n")
			}
		}
	}

	if m.status != "" {
		if strings.HasPrefix(m.status, "save failed") {
			b.WriteString(style.ErrorText.Render(m.status))
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(style.Accent).Render(m.status))
		}
	} else if m.dirty {
		b.WriteString(style.Help.Render("unsaved changes · ctrl+s to save"))
	} else {
		b.WriteString(style.Help.Render("saved settings load on agent start"))
	}
	// Stretched to the full tab width, like every table-based tab already
	// is (SetWidth(d.width)) — otherwise this is the one tab whose content
	// is narrower than the tab bar, and App.View()'s horizontal centering
	// (invisible when content already fills the width) visibly drags both
	// the body and the tab bar above it toward the middle of the screen.
	return lipgloss.NewStyle().Width(w).Render(b.String())
}

// renderExecutorList is a vertical scrollable list — scales when KnownExecutors grows.
func renderExecutorList(current string, focused bool) string {
	var lines []string
	for _, e := range config.KnownExecutors {
		desc := executorDesc(e)
		selected := e == current
		prefix := "    "
		body := fmt.Sprintf("%-10s  %s", e, desc)
		switch {
		case selected && focused:
			prefix = "  › "
			body = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(style.Accent).Render(" " + body + " ")
		case selected:
			prefix = "  • "
			body = lipgloss.NewStyle().Bold(true).Foreground(style.Accent).Render(body)
		default:
			body = style.Help.Render(body)
		}
		lines = append(lines, prefix+body)
	}
	if focused {
		lines = append(lines, style.Help.Render("    ↑/↓ choose (exits list at ends)   enter next field"))
	}
	return strings.Join(lines, "\n") + "\n"
}

func executorDesc(name string) string {
	switch name {
	case "training":
		return "run real Python scripts; stream stdout as job logs"
	case "mock":
		return "fake ~2s run; does not execute your script (dev/CI)"
	default:
		return ""
	}
}

// renderApprovalList mirrors the executor list: two options, ↑/↓ to choose.
func renderApprovalList(current string, focused bool) string {
	on := isTruthy(current)
	type opt struct {
		val, title, desc string
	}
	opts := []opt{
		{"true", "true", "pause each job until a human approves on the worker"},
		{"false", "false", "start jobs immediately when assigned (no prompt)"},
	}
	var lines []string
	for _, o := range opts {
		selected := (o.val == "true" && on) || (o.val == "false" && !on)
		prefix := "    "
		body := fmt.Sprintf("%-6s  %s", o.title, o.desc)
		switch {
		case selected && focused:
			prefix = "  › "
			body = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(style.Accent).Render(" " + body + " ")
		case selected:
			prefix = "  • "
			body = lipgloss.NewStyle().Bold(true).Foreground(style.Accent).Render(body)
		default:
			body = style.Help.Render(body)
		}
		lines = append(lines, prefix+body)
	}
	if focused {
		lines = append(lines, style.Help.Render("    ↑ true   ↓ false   enter confirm & next field"))
	}
	return strings.Join(lines, "\n") + "\n"
}
