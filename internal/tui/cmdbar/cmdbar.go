// Package cmdbar is the command line that "/" opens at the bottom of the TUI, e.g. /logs.
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

// sent when the user presses enter on a command that isn't empty
// e.g. typing /profile primary sends "profile primary"
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

// a command bar that suggests from these command names, given without the "/"
// e.g. New("logs", "profile")
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

// opens the bar; the screen calls it when "/" is pressed and the bar is closed
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

// handles keys only while the bar is open; the screen handles "/" itself
// and must not also act on the keys the bar is using
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

// the input line and matching commands when open, or a dim hint when closed
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
