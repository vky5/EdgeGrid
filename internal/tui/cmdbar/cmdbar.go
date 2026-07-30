// Package cmdbar is the "/"-triggered command line shown at the bottom of
// every TUI screen — one component shared by onboarding and the dashboard,
// so "/logs" (and whatever commands follow it) behaves identically no
// matter which screen it's opened from.
package cmdbar

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/tui/style"
)

var (
	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(style.Accent).
			Padding(0, 1)
)

// SubmitMsg is emitted when the user presses enter with a non-empty command.
type SubmitMsg struct {
	Command string
}

type Model struct {
	input         textinput.Model
	active        bool
	commands      []string
	suggestions   []string
	selectedIndex int
	width         int
}

// New builds a command bar that autocompletes against the given command
// names (without the leading "/") — e.g. New("onboard", "logs").
func New(commands ...string) Model {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 128
	ti.Width = 40
	ti.ShowSuggestions = false // Disable native suggestion inline hints
	return Model{
		input:         ti,
		commands:      commands,
		suggestions:   commands,
		selectedIndex: 0,
	}
}

func (m Model) Active() bool { return m.active }

// Activate focuses the bar. Call when the owning screen sees a "/" key
// press and the bar isn't already active.
func (m Model) Activate() (Model, tea.Cmd) {
	m.active = true
	m.input.SetValue("")
	m.selectedIndex = 0
	m = m.updateSuggestions()
	return m, m.input.Focus()
}

func (m Model) updateSuggestions() Model {
	typed := m.input.Value()
	m.suggestions = nil
	for _, cmd := range m.commands {
		if strings.HasPrefix(strings.ToLower(cmd), strings.ToLower(typed)) {
			m.suggestions = append(m.suggestions, cmd)
		}
	}
	// Clamp/reset selectedIndex
	if m.selectedIndex >= len(m.suggestions) {
		m.selectedIndex = len(m.suggestions) - 1
	}
	if len(m.suggestions) == 0 {
		m.selectedIndex = -1
	}
	return m
}

// Update should only be called while Active() is true — the owning screen
// is responsible for routing "/" itself (see Activate) and for not
// forwarding other keys to its own screen logic while this is active.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = wm.Width
		return m, nil
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			m.active = false
			m.input.Blur()
			return m, nil
		case "backspace":
			if m.input.Value() == "" {
				m.active = false
				m.input.Blur()
				return m, nil
			}
		case "up", "shift+tab":
			if len(m.suggestions) > 0 {
				if m.selectedIndex <= 0 {
					m.selectedIndex = len(m.suggestions) - 1
				} else {
					m.selectedIndex--
				}
			}
			return m, nil
		case "down":
			if len(m.suggestions) > 0 {
				if m.selectedIndex >= len(m.suggestions)-1 {
					m.selectedIndex = 0
				} else {
					m.selectedIndex++
				}
			}
			return m, nil
		case "tab":
			if len(m.suggestions) > 0 && m.selectedIndex >= 0 && m.selectedIndex < len(m.suggestions) {
				m.input.SetValue(m.suggestions[m.selectedIndex])
				m.input.SetCursor(len(m.input.Value()))
				m = m.updateSuggestions()
			}
			return m, nil
		case "enter":
			var cmd string
			if m.selectedIndex >= 0 && m.selectedIndex < len(m.suggestions) {
				cmd = m.suggestions[m.selectedIndex]
			} else {
				cmd = strings.TrimSpace(m.input.Value())
			}
			m.active = false
			m.input.Blur()
			if cmd == "" {
				return m, nil
			}
			return m, func() tea.Msg { return SubmitMsg{Command: cmd} }
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m = m.updateSuggestions()
	return m, cmd
}

// View renders the active input line plus matching command suggestions, or
// a dim hint when inactive.
func (m Model) View() string {
	if !m.active {
		return style.Help.Render("/ command")
	}

	var lines []string
	lines = append(lines, m.input.View())

	if len(m.suggestions) > 0 {
		lines = append(lines, "") // spacer line
		for i, sug := range m.suggestions {
			if i == m.selectedIndex {
				lines = append(lines, style.Selected.Render("▸ "+sug))
			} else {
				lines = append(lines, style.Help.Render("  "+sug))
			}
		}
	}

	body := strings.Join(lines, "\n")
	w := 50
	if m.width > 6 {
		w = m.width - 4
	}
	return boxStyle.Width(w).Render(body)
}
