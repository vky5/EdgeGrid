package dashboard

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"tailscale.com/client/local"

	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// peersRefreshInterval is how often the Peers tab re-reads Tailscale's
// membership state.
const peersRefreshInterval = 5 * time.Second

// peersModel shows discovery.Snapshot() live — Tailscale's own membership
// view of every EdgeGrid-tagged device. It is not a record of hello
// exchanges this node has completed; there's no store for that yet, so a
// peer listed here as online is a Tailscale fact, not proof this node has
// actually talked to it.
type peersModel struct {
	lc            *local.Client
	peers         []discovery.Peer
	err           error
	width, height int
}

func newPeersModel(lc *local.Client) peersModel {
	m := peersModel{lc: lc}
	return m.refresh()
}

func (m peersModel) WithSize(w, h int) peersModel {
	m.width, m.height = w, h
	return m
}

type peersRefreshMsg struct{}

func peersRefreshCmd() tea.Cmd {
	return tea.Tick(peersRefreshInterval, func(time.Time) tea.Msg { return peersRefreshMsg{} })
}

func (m peersModel) refresh() peersModel {
	if m.lc == nil {
		return m
	}
	m.peers, m.err = discovery.Snapshot(context.Background(), m.lc)
	return m
}

func (m peersModel) Init() tea.Cmd { return peersRefreshCmd() }

func (m peersModel) Update(msg tea.Msg) (peersModel, tea.Cmd) {
	if _, ok := msg.(peersRefreshMsg); ok {
		return m.refresh(), peersRefreshCmd()
	}
	return m, nil
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func (m peersModel) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	height := m.height
	if height <= 0 {
		height = 24
	}

	if m.err != nil {
		content := lipgloss.NewStyle().Foreground(style.Danger).Render("tailscale status: " + m.err.Error())
		return renderPane("PEERS", content, width, height)
	}

	if len(m.peers) == 0 {
		content := lipgloss.NewStyle().Foreground(style.Muted).Render("no other EdgeGrid nodes on this tailnet yet")
		return renderPane("PEERS", content, width, height)
	}

	label := lipgloss.NewStyle().Foreground(style.Muted)
	rows := make([]string, 0, len(m.peers))
	for _, p := range m.peers {
		var statusPill string
		var seen string
		if p.Online {
			statusPill = pill(" ONLINE ", lipgloss.Color("0"), greenColor)
			seen = ""
		} else {
			statusPill = pill(" OFFLINE ", lipgloss.Color("15"), style.Muted)
			seen = label.Render("  last seen " + timeAgo(p.LastSeen))
		}

		name := p.Hostname
		if name == "" {
			name = p.ID
		}
		ip := "—"
		if p.IP.IsValid() {
			ip = p.IP.String()
		}
		rows = append(rows, fmt.Sprintf("%s  %-20s %-16s%s",
			statusPill, name, ip, seen))
	}

	content := strings.Join(rows, "\n")
	return renderPane("PEERS", content, width, height)
}
