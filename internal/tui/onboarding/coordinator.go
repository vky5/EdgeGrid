package onboarding

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// coordinatorChosenMsg is emitted when the user confirms a join address.
type coordinatorChosenMsg struct {
	addr string
}

type coordinatorModel struct {
	roleLabel     string
	input         textinput.Model
	width, height int
	frame         int
}

func newCoordinatorModel(roleLabel, defaultAddr string) coordinatorModel {
	ti := textinput.New()
	ti.Placeholder = "100.x.x.x:8080"
	ti.SetValue(defaultAddr)
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 48
	ti.PromptStyle = lipgloss.NewStyle().Foreground(style.Accent)
	ti.TextStyle = lipgloss.NewStyle().Foreground(style.Accent).Bold(true)
	return coordinatorModel{roleLabel: roleLabel, input: ti}
}

func (m coordinatorModel) WithSize(width, height int) coordinatorModel {
	m.width = width
	m.height = height
	return m
}

func (m coordinatorModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, tickCmd())
}

func (m coordinatorModel) Update(msg tea.Msg) (coordinatorModel, tea.Cmd) {
	switch msg := msg.(type) {
	case TickMsg:
		m.frame++
		return m, tickCmd()
	case tea.KeyMsg:
		if msg.String() == "enter" && m.input.Value() != "" {
			addr := m.input.Value()
			return m, func() tea.Msg { return coordinatorChosenMsg{addr: addr} }
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m coordinatorModel) renderArt(w int) string {
	signals := []string{
		`   o . o . o   `,
		`   . o . o .   `,
		`   . . o . o   `,
		`   o . . o .   `,
	}
	sig := signals[m.frame%len(signals)]

	leftBox := lipgloss.NewStyle().Foreground(greenColor).Bold(true).Render(
		` ┌───────────────────┐
 │ JOINING NODE (YOU)│
 ├───────────────────┤
 │ STATUS: READY     │
 └───────────────────┘`)

	// Nothing is actually connecting yet at this screen — the address
	// hasn't been submitted — so this stays neutral until enter is pressed.
	connectors := lipgloss.NewStyle().Foreground(style.Muted).Render(
		fmt.Sprintf("\n      %s╌      \n      AWAITING INPUT", sig))

	rightBox := lipgloss.NewStyle().Foreground(style.Muted).Render(
		` ┌───────────────────┐
 │COORDINATOR TARGET │
 ├───────────────────┤
 │ STATUS: UNKNOWN   │
 └───────────────────┘`)

	return lipgloss.JoinHorizontal(lipgloss.Center, leftBox, connectors, rightBox)
}

func (m coordinatorModel) renderHint(w int) string {
	text := "INFO: Enter the HTTP address of an active primary coordinator. The joining node will request token validation and secure network credentials."
	return lipgloss.NewStyle().
		Foreground(style.Help.GetForeground()).
		Width(max(w-4, 15)).
		Render(text)
}

func (m coordinatorModel) View() string {
	wTotal := m.width
	if wTotal <= 0 {
		wTotal = 80 // fallback
	}

	wInner := wTotal - 10
	if wInner < 70 {
		wInner = 70
	}

	wLeft := int(float64(wInner) * 0.45)
	wRight := wInner - wLeft - 2

	// Vertical line divider height
	hTarget := m.height - 8
	if hTarget < 12 {
		hTarget = 12
	}
	var divLines []string
	for i := 0; i < hTarget; i++ {
		divLines = append(divLines, "│")
	}
	divider := lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Join(divLines, "\n"))

	// Left Column Content
	var leftLines []string
	leftLines = append(leftLines, style.Help.Render("Join as: ")+style.Selected.Render(m.roleLabel), "")
	leftLines = append(leftLines, style.Title.Render("Coordinator address"), "")

	inputBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Accent).
		Padding(0, 1).
		Width(wLeft - 4)
	leftLines = append(leftLines, inputBox.Render(m.input.View()), "")

	leftLines = append(leftLines, style.Help.Render(strings.Repeat("┄", max(wLeft-4, 10))), "")
	leftLines = append(leftLines, style.Help.Render("Examples"), "")

	alignExample := func(value, desc string) string {
		pad := max(wLeft-len(value)-len(desc)-4, 2)
		return "  " + style.Selected.Render(value) + strings.Repeat(" ", pad) + style.Help.Render(desc)
	}
	leftLines = append(leftLines, alignExample("http://blacktree.in:8080", "public/LAN address"))
	leftLines = append(leftLines, alignExample("100.125.85.92:8080", "tailnet address"))

	leftContent := lipgloss.NewStyle().Width(wLeft).Padding(1, 1).Render(lipgloss.JoinVertical(lipgloss.Left, leftLines...))

	// Right Column Content
	artText := m.renderArt(wRight)
	hintText := m.renderHint(wRight)

	dividerWidth := max(wRight-4, 10)
	if dividerWidth > 34 {
		dividerWidth = 34
	}

	topContent := lipgloss.Place(wRight, hTarget-5, lipgloss.Center, lipgloss.Center, artText)

	bottomBlock := lipgloss.JoinVertical(lipgloss.Center,
		style.Help.Render(strings.Repeat("─", dividerWidth)),
		"",
		hintText,
	)
	bottomContent := lipgloss.Place(wRight, 5, lipgloss.Center, lipgloss.Bottom, bottomBlock)

	artContent := lipgloss.JoinVertical(lipgloss.Center, topContent, bottomContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftContent, divider, artContent)
}
