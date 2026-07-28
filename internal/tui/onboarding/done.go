package onboarding

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/tui/style"
)

type doneModel struct {
	roleLabel  string
	nodeID     string
	ip         string
	adminToken string
}

func newDoneModel(roleLabel, nodeID, ip, adminToken string) doneModel {
	return doneModel{roleLabel: roleLabel, nodeID: nodeID, ip: ip, adminToken: adminToken}
}

// StartConfirmedMsg is emitted when the user presses enter to leave the
// wizard and actually start the node.
type StartConfirmedMsg struct{}

func (m doneModel) Init() tea.Cmd { return nil }

func (m doneModel) Update(msg tea.Msg) (doneModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "enter" {
		return m, func() tea.Msg { return StartConfirmedMsg{} }
	}
	return m, nil
}

const rocketArt = `
     /\
    /  \
   /    \
  |  ▐▌  |
  |  ▐▌  |
 /|  ▐▌  |\
/ |  ▐▌  | \
| |  ▐▌  | |
| |  ▐▌  | |
\/|======|\/
  |  ||  |
  |  ||  |
  /======\
  \vvvvvv/
   \vvvv/
    \vv/
     \/`

func (m doneModel) View() string {
	// 1. Left pane: summary card
	var details []string
	details = append(details, "Role:     "+m.roleLabel)
	if m.nodeID != "" {
		details = append(details, "Node ID:  "+m.nodeID)
	}
	if m.ip != "" {
		details = append(details, "IP Addr:  "+m.ip)
	}
	if m.adminToken != "" {
		details = append(details, "", "Admin token (save this):", m.adminToken)
	}

	cardBody := style.Title.Render("EDGEGRID READY") + "\n\n" + strings.Join(details, "\n")

	// Widened when showing a token — a 64-char hex string doesn't wrap
	// cleanly (no spaces to break on), so it needs room instead.
	cardWidth := 34
	if m.adminToken != "" {
		cardWidth = 70
	}

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("42")). // Emerald green border for success!
		Padding(1, 2).
		Width(cardWidth)

	leftContent := cardStyle.Render(cardBody)

	// 2. Right pane: Rocket ASCII Art
	artStyle := lipgloss.NewStyle().
		Foreground(style.Accent). // Blue accent rocket!
		Padding(0, 4)

	rightContent := artStyle.Render(rocketArt)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftContent, rightContent)
}
