// Package style holds lipgloss styles shared across every TUI screen, so
// onboarding and dashboard look like one product instead of two.
package style

import "github.com/charmbracelet/lipgloss"

var (
	Accent = lipgloss.Color("39")  // blue
	Muted  = lipgloss.Color("240") // gray
	Danger = lipgloss.Color("196") // red

	Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(Accent).
		Padding(0, 1)

	Selected = lipgloss.NewStyle().
			Bold(true).
			Foreground(Accent)

	Help = lipgloss.NewStyle().
		Foreground(Muted)

	Border = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Muted).
		Padding(0, 1)

	TabActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(Accent).
			Padding(0, 2)

	TabInactive = lipgloss.NewStyle().
			Bold(true).
			Foreground(Muted).
			Padding(0, 2)

	ErrorText = lipgloss.NewStyle().
			Foreground(Danger)

	HeaderBar = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("0")).
			Background(Accent).
			Padding(0, 1)

	FooterBar = lipgloss.NewStyle().
			Foreground(Muted).
			Padding(0, 1)

	StepLabel = lipgloss.NewStyle().
			Foreground(Muted).
			Padding(0, 1)
)

// Screen lays out a full-terminal app frame: a header bar spanning the full
// width, the given body centered in the remaining vertical space, and a
// footer hint bar — the same chrome used by every full-screen program in
// this TUI so onboarding and the dashboard read as one product.
func Screen(width, height int, title, step, body, help string) string {
	if width <= 0 || height <= 0 {
		// No WindowSizeMsg yet (first frame) — render unplaced so there's
		// still something on screen instead of a blank frame.
		return HeaderBar.Render(title) + "\n\n" + body + "\n\n" + FooterBar.Render(help)
	}

	header := HeaderBar.Width(width).Render(title)
	if step != "" {
		header = lipgloss.JoinHorizontal(lipgloss.Top, header, StepLabel.Render(step))
	}
	footer := FooterBar.Width(width).Render(help)

	bodyHeight := max(height-lipgloss.Height(header)-lipgloss.Height(footer), 1)
	centeredBody := lipgloss.Place(width, bodyHeight, lipgloss.Center, lipgloss.Center, body)

	return lipgloss.JoinVertical(lipgloss.Left, header, centeredBody, footer)
}
