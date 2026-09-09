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

// bootEvent is one update from the background bring-up goroutine: a progress
// line, or the terminal result.
type bootEvent struct {
	line     string
	nodeUp   *node.Node
	closeLog func() error
	err      error
	done     bool
}

// RunBoot brings the node up while showing a progress screen, and returns once
// tsnet is either up or has failed.
//
// The screen exists because of what tsnet does when a data dir is not yet
// authenticated: ts.Up blocks, possibly forever, while emitting a login URL
// through UserLogf every five seconds. With logs routed to a file for the
// dashboard's sake, that URL was invisible and the terminal simply sat blank —
// the node looked hung when it was only waiting to be told who it was.
func RunBoot(ctx context.Context, cfg *node.Config) (*node.Node, func() error, error) {
	events := make(chan bootEvent, 64)

	go func() {
		defer close(events)
		onProgress := func(line string) {
			// Non-blocking: tsnet calls this from its own goroutines and must
			// never be stalled by a slow or finished UI.
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
			// Leaves the goroutine to unwind on the cancelled context.
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

// extractLoginURL pulls the interactive-login URL out of a tsnet log line.
// tsnet reprints it every five seconds until authentication completes, so the
// last one seen is always current.
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
