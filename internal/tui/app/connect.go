package app

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// greenColor marks "the local/you side" of the connect art — same visual
// convention onboarding's role/join-status screens use for "JOINING NODE
// (YOU)", kept as its own copy since that one is unexported in a
// different package.
var greenColor = lipgloss.Color("42")

// connectSubmitMsg is emitted when both fields are filled and submitted.
type connectSubmitMsg struct {
	coord      string
	adminToken string
}

// connectTickMsg drives the connect screen's own animation frame — a
// distinct type from onboarding's TickMsg (different package, and even if
// it weren't, mixing tick sources across independently-shown screens is
// exactly the bug class joinStatusModel's joinTickMsg was added to avoid).
type connectTickMsg struct{}

func connectTickCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return connectTickMsg{} })
}

// connectModel is a small two-field form (coordinator address, password) —
// deliberately minimal: one coordinator per session, no multi-cluster
// switching. Reachable via "/connect" from the dashboard.
type connectModel struct {
	addr          textinput.Model
	token         textinput.Model
	focus         int // 0 = addr, 1 = token
	width, height int
	frame         int
}

func newConnectModel() connectModel {
	addr := textinput.New()
	addr.Placeholder = "http://100.x.x.x:8080"
	addr.CharLimit = 256
	addr.Width = 40
	addr.PromptStyle = lipgloss.NewStyle().Foreground(style.Accent)
	addr.TextStyle = lipgloss.NewStyle().Foreground(style.Accent).Bold(true)
	addr.Focus()

	token := textinput.New()
	token.Placeholder = "password"
	token.CharLimit = 128
	token.Width = 40
	token.EchoMode = textinput.EchoPassword
	token.EchoCharacter = '•'
	token.PromptStyle = lipgloss.NewStyle().Foreground(style.Accent)
	token.TextStyle = lipgloss.NewStyle().Foreground(style.Accent).Bold(true)

	return connectModel{addr: addr, token: token}
}

func (m connectModel) WithSize(width, height int) connectModel {
	m.width = width
	m.height = height
	return m
}

func (m connectModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, connectTickCmd())
}

func (m connectModel) Update(msg tea.Msg) (connectModel, tea.Cmd) {
	if _, ok := msg.(connectTickMsg); ok {
		m.frame++
		return m, connectTickCmd()
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "tab", "shift+tab", "up", "down":
			m.focus = 1 - m.focus
			if m.focus == 0 {
				m.token.Blur()
				return m, m.addr.Focus()
			}
			m.addr.Blur()
			return m, m.token.Focus()
		case "enter":
			if m.addr.Value() != "" && m.token.Value() != "" {
				coord, token := m.addr.Value(), m.token.Value()
				return m, func() tea.Msg { return connectSubmitMsg{coord: coord, adminToken: token} }
			}
		}
	}
	var cmd tea.Cmd
	if m.focus == 0 {
		m.addr, cmd = m.addr.Update(msg)
	} else {
		m.token, cmd = m.token.Update(msg)
	}
	return m, cmd
}

func (m connectModel) renderArt() string {
	signals := []string{
		`   o . o . o   `,
		`   . o . o .   `,
		`   . . o . o   `,
		`   o . . o .   `,
	}
	sig := signals[m.frame%len(signals)]

	leftBox := lipgloss.NewStyle().Foreground(greenColor).Bold(true).Render(
		` ┌───────────────────┐
 │ YOUR DASHBOARD    │
 ├───────────────────┤
 │ STATUS: LOCAL     │
 └───────────────────┘`)

	connectors := lipgloss.NewStyle().Foreground(style.Accent).Render(
		"\n      " + sig + "►      \n      LINKING")

	rightBox := lipgloss.NewStyle().Foreground(style.Accent).Render(
		` ┌───────────────────┐
 │ COORDINATOR       │
 ├───────────────────┤
 │ STATUS: REMOTE    │
 └───────────────────┘`)

	return lipgloss.JoinHorizontal(lipgloss.Center, leftBox, connectors, rightBox)
}

func (m connectModel) renderHint(w int) string {
	text := "INFO: The address and password come from whoever operates that coordinator — ask them for both, or check its own startup output if that's you."
	return lipgloss.NewStyle().
		Foreground(style.Help.GetForeground()).
		Width(max(w-4, 15)).
		Render(text)
}

func (m connectModel) View() string {
	wTotal := m.width
	if wTotal <= 0 {
		wTotal = 80
	}
	wInner := max(wTotal-10, 70)

	wLeft := int(float64(wInner) * 0.45)
	wRight := wInner - wLeft - 2

	hTarget := max(m.height-8, 12)
	var divLines []string
	for range hTarget {
		divLines = append(divLines, "│")
	}
	divider := lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Join(divLines, "\n"))

	// Left column
	inputBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Accent).
		Padding(0, 1).
		Width(wLeft - 4)

	var leftLines []string
	leftLines = append(leftLines, style.Title.Render("Connect to coordinator"), "")
	leftLines = append(leftLines, style.Help.Render("Address"))
	leftLines = append(leftLines, inputBox.Render(m.addr.View()), "")
	leftLines = append(leftLines, style.Help.Render("Password"))
	leftLines = append(leftLines, inputBox.Render(m.token.View()))
	leftContent := lipgloss.NewStyle().Width(wLeft).Padding(1, 1).Render(lipgloss.JoinVertical(lipgloss.Left, leftLines...))

	// Right column: art on top, hint pinned to the bottom — same shape as
	// the onboarding coordinator/join-status screens.
	topContent := lipgloss.Place(wRight, hTarget-5, lipgloss.Center, lipgloss.Center, m.renderArt())

	dividerWidth := min(max(wRight-4, 10), 34)
	bottomBlock := lipgloss.JoinVertical(lipgloss.Center,
		style.Help.Render(strings.Repeat("─", dividerWidth)),
		"",
		m.renderHint(wRight),
	)
	bottomContent := lipgloss.Place(wRight, 5, lipgloss.Center, lipgloss.Bottom, bottomBlock)

	rightContent := lipgloss.JoinVertical(lipgloss.Center, topContent, bottomContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftContent, divider, rightContent)
}
