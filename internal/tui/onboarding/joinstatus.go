package onboarding

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// joinApprovedMsg is emitted once the join request is approved. Nothing
// wires this to the real requestAndWaitForApproval poll loop yet — that's
// the transport decision still pending, same as internal/tui/client.
type joinApprovedMsg struct {
	nodeID string
	ip     string
}

// joinTickMsg drives the elapsed-time counter — a distinct type from
// role.go's TickMsg so it isn't intercepted by wizard.go's
// always-route-TickMsg-to-role special case.
type joinTickMsg struct{}

func joinTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return joinTickMsg{} })
}

type joinStatusModel struct {
	roleLabel     string
	coordAddr     string
	spinner       spinner.Model
	elapsed       int
	width, height int
	frame         int
	loginURL      string
	err           error
	lines         []string
}

func newJoinStatusModel(roleLabel, coordAddr string) joinStatusModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return joinStatusModel{roleLabel: roleLabel, coordAddr: coordAddr, spinner: sp}
}

func (m joinStatusModel) WithSize(width, height int) joinStatusModel {
	m.width = width
	m.height = height
	return m
}

func (m joinStatusModel) withLoginURL(url string) joinStatusModel {
	m.loginURL = url
	return m
}

func (m joinStatusModel) withLogLine(line string) joinStatusModel {
	if line != "" {
		m.lines = append(m.lines, line)
		if len(m.lines) > 20 {
			m.lines = m.lines[len(m.lines)-20:]
		}
	}
	return m
}

// withError marks the join attempt as failed — requestAndWaitForApproval
// returned an error (rejected, or a network/HTTP failure talking to the
// coordinator). Terminal: nothing more will arrive on the event channel.
func (m joinStatusModel) withError(err error) joinStatusModel {
	m.err = err
	return m
}

func (m joinStatusModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, joinTickCmd())
}

func (m joinStatusModel) Update(msg tea.Msg) (joinStatusModel, tea.Cmd) {
	if _, ok := msg.(joinTickMsg); ok {
		m.elapsed++
		m.frame++
		return m, joinTickCmd()
	}
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

func (m joinStatusModel) renderArt(w int) string {
	signals := []string{
		`   o . o . o   `,
		`   . o . o .   `,
		`   . . o . o   `,
		`   o . . o .   `,
	}
	sig := signals[m.frame%len(signals)]
	lockChar := "🔒"
	if m.frame%2 == 0 {
		lockChar = "🔓"
	}

	leftBox := lipgloss.NewStyle().Foreground(greenColor).Bold(true).Render(
		` ┌───────────────────┐
 │ JOINING NODE (YOU)│
 ├───────────────────┤
 │ STATUS: PENDING   │
 └───────────────────┘`)

	connectors := lipgloss.NewStyle().Foreground(style.Accent).Render(
		fmt.Sprintf("\n      %s%s      \n        PENDING", sig, lockChar))

	addr11 := m.coordAddr
	if len(addr11) > 11 {
		addr11 = addr11[:11]
	}

	rightBox := lipgloss.NewStyle().Foreground(style.Accent).Render(
		fmt.Sprintf(` ┌───────────────────┐
 │COORDINATOR TARGET │
 ├───────────────────┤
 │ ADDR: %-11s │
 └───────────────────┘`, addr11))

	return lipgloss.JoinHorizontal(lipgloss.Center, leftBox, connectors, rightBox)
}

func (m joinStatusModel) renderHint(w int) string {
	if m.loginURL != "" {
		return lipgloss.NewStyle().
			Foreground(style.Danger).
			Bold(true).
			Width(max(w-4, 15)).
			Render(fmt.Sprintf("TAILSCALE LOGIN REQUIRED:\nOpen link in browser to authenticate:\n\n%s", m.loginURL))
	}
	text := "INFO: Awaiting administrator authorization. Please open the primary coordinator's CLI dashboard to approve this node join request."
	return lipgloss.NewStyle().
		Foreground(style.Help.GetForeground()).
		Width(max(w-4, 15)).
		Render(text)
}

func (m joinStatusModel) View() string {
	if m.err != nil {
		return style.Title.Render("Join failed") + "\n\n" + style.ErrorText.Render(m.err.Error()) +
			"\n\n" + style.Help.Render("esc back   q quit")
	}

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
	leftLines = append(leftLines, style.Title.Render("Joining cluster"), "")
	leftLines = append(leftLines, contextLine("role", m.roleLabel))
	leftLines = append(leftLines, contextLine("coordinator", m.coordAddr), "")

	leftLines = append(leftLines, style.Help.Render(strings.Repeat("┄", max(wLeft-4, 10))), "")

	leftLines = append(leftLines, style.Selected.Render("✓")+" connected to coordinator")
	leftLines = append(leftLines, style.Selected.Render("✓")+" join request submitted")
	leftLines = append(leftLines, m.spinner.View()+" waiting for admin approval..."+style.Help.Render(fmt.Sprintf("  (elapsed %ds)", m.elapsed)))

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

func contextLine(label, value string) string {
	pad := max(13-len(label), 1)
	return style.Help.Render(label) + strings.Repeat(" ", pad) + value
}
