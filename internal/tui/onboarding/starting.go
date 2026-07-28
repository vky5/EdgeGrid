package onboarding

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// agentEventMsg carries one update from the background node-startup
// goroutine — either a progress line, the final error, or success.
type agentEventMsg struct {
	line string
	err  error
	done bool
}

type startingModel struct {
	spinner  spinner.Model
	lines    []string
	err      error
	done     bool
	Progress float64
}

func newStartingModel() startingModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return startingModel{
		spinner:  sp,
		Progress: 0.05,
	}
}

func (m startingModel) Init() tea.Cmd { return m.spinner.Tick }

func (m startingModel) Update(msg tea.Msg) (startingModel, tea.Cmd) {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

func (m *startingModel) applyEvent(ev agentEventMsg) {
	if ev.line != "" {
		m.lines = append(m.lines, ev.line)
		if len(m.lines) > 20 {
			m.lines = m.lines[len(m.lines)-20:]
		}

		// Update progress based on key lifecycle milestones
		lineLower := strings.ToLower(ev.line)
		if strings.Contains(lineLower, "initializing") {
			m.Progress = 0.20
		} else if strings.Contains(lineLower, "nats") || strings.Contains(lineLower, "broker") {
			m.Progress = 0.40
		} else if strings.Contains(lineLower, "tailscale") {
			m.Progress = 0.60
		} else if strings.Contains(lineLower, "wireguard") || strings.Contains(lineLower, "overlay") {
			m.Progress = 0.80
		} else if strings.Contains(lineLower, "complete") || strings.Contains(lineLower, "success") {
			m.Progress = 1.0
		} else {
			// Increment slowly for other log inputs, capped at 90%
			if m.Progress < 0.90 {
				m.Progress += 0.03
			}
		}
	}
	if ev.err != nil {
		m.err = ev.err
	}
	if ev.done {
		m.done = true
		m.Progress = 1.0
	}
}

func (m startingModel) View() string {
	if m.err != nil {
		return style.Title.Render("Startup failed") + "\n\n" + style.ErrorText.Render(m.err.Error())
	}
	if m.done {
		return style.Title.Render("Starting services") + "\n\n" + "done — handing off..."
	}

	var loginURL string
	for _, l := range m.lines {
		if strings.Contains(l, "https://login.tailscale.com") {
			idx := strings.Index(l, "https://login.tailscale.com")
			if idx != -1 {
				loginURL = strings.TrimSpace(l[idx:])
			}
		}
	}

	if loginURL != "" {
		return fmt.Sprintf("%s\n\nTo start the coordinator, please log in via Tailscale:\n\n%s\n\n%s  Waiting for authentication...",
			style.Title.Render("Authentication Required"),
			style.Selected.Render(loginURL),
			m.spinner.View())
	}

	var body strings.Builder
	body.WriteString(style.Title.Render("Starting Node"))
	body.WriteString("\n\n")
	body.WriteString(m.spinner.View())
	body.WriteString(" Initializing services...\n\n")

	logCount := len(m.lines)
	if logCount > 0 {
		body.WriteString(style.Help.Render(m.lines[logCount-1]))
		body.WriteString("\n")
	}

	return body.String()
}
