package app

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/node"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// one update from the background start-up: a progress line, or the final result
type bootEvent struct {
	line     string
	nodeUp   *node.Node
	closeLog func() error
	err      error
	done     bool
}

// starts the node behind a progress screen and returns once it is up or has failed
// tsnet can wait forever for a browser login, and with logs going to a file
// the login link was hidden, so without this screen the terminal looked hung
func RunBoot(ctx context.Context, cfg *node.Config) (*node.Node, func() error, error) {
	events := make(chan bootEvent, 64)

	go func() {
		// never closed: tsnet logs one more line ("AuthLoop: state is Running; done") after we're done,
		// and a send on a closed channel crashes even inside a select with a default
		// so the bootEvent{done: true} below ends the screen, not a close
		onProgress := func(line string) {
			// never wait here, or a slow or finished screen would stall tsnet
			select {
			case events <- bootEvent{line: line}:
			default:
			}
		}
		n, closeLog, err := node.NewWithLogging(ctx, cfg, onProgress, true)
		events <- bootEvent{nodeUp: n, closeLog: closeLog, err: err, done: true}
	}()

	final, err := tea.NewProgram(newBootingModel(events), tea.WithAltScreen()).Run()
	if err != nil {
		return nil, nil, err
	}
	m, ok := final.(bootingModel)
	if !ok {
		return nil, nil, nil
	}
	return m.nodeUp, m.closeLog, m.err
}

type bootingModel struct {
	spinner       spinner.Model
	events        <-chan bootEvent
	lines         []string
	loginURL      string
	width, height int

	nodeUp   *node.Node
	closeLog func() error
	err      error
}

func newBootingModel(events <-chan bootEvent) bootingModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(style.Accent)
	return bootingModel{spinner: sp, events: events}
}

func waitForBootEvent(ch <-chan bootEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return bootEvent{done: true}
		}
		return ev
	}
}

func (m bootingModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, waitForBootEvent(m.events))
}

func (m bootingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" || msg.String() == "q" {
			// the start-up goroutine stops on its own once the context is cancelled
			return m, tea.Quit
		}
		return m, nil
	case bootEvent:
		if msg.done {
			m.nodeUp, m.closeLog, m.err = msg.nodeUp, msg.closeLog, msg.err
			return m, tea.Quit
		}
		if msg.line != "" {
			m.lines = append(m.lines, msg.line)
			if len(m.lines) > 12 {
				m.lines = m.lines[len(m.lines)-12:]
			}
			if u := extractLoginURL(msg.line); u != "" {
				m.loginURL = u
			}
		}
		return m, waitForBootEvent(m.events)
	}
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

// the Tailscale login link in a tsnet log line, or ""
// e.g. "... or go to: https://login.tailscale.com/a/701180b014f14" gives the link
// tsnet prints it again every five seconds until login, so the last one seen is current
func extractLoginURL(line string) string {
	const marker = "https://login.tailscale.com"
	i := strings.Index(line, marker)
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(strings.Fields(line[i:])[0])
}

func (m bootingModel) View() string {
	if m.err != nil {
		return style.Title.Render("Startup failed") + "\n\n" + style.ErrorText.Render(m.err.Error())
	}

	var b strings.Builder
	if m.loginURL != "" {
		b.WriteString(style.Title.Render("AUTHENTICATION REQUIRED"))
		b.WriteString("\n\nThis node isn't on the tailnet yet. Open this to let it in:\n\n  ")
		b.WriteString(lipgloss.NewStyle().Foreground(style.Accent).Bold(true).Render(m.loginURL))
		b.WriteString("\n\n" + m.spinner.View() + " waiting for authentication...")
		b.WriteString("\n\n" + style.Help.Render("Next time, paste an auth key on the join screen to skip this."))
	} else {
		b.WriteString(style.Title.Render("STARTING NODE"))
		b.WriteString("\n\n" + m.spinner.View() + " bringing up tailnet...")
	}

	if len(m.lines) > 0 {
		b.WriteString("\n\n" + style.Help.Render(m.lines[len(m.lines)-1]))
	}
	b.WriteString("\n\n" + style.Help.Render("q / ctrl+c: cancel"))

	body := b.String()
	if m.width <= 0 || m.height <= 0 {
		return body
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
}
