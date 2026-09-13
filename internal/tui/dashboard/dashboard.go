// Package dashboard is the node's live TUI view. There's no coordinator to
// poll and no fleet to admin — this is strictly a single node looking at
// itself: Overview always, and Tokens only when this node has Tailscale API
// credentials configured (see tailscaleapi.LoadCredentials) to mint with.
package dashboard

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"tailscale.com/client/local"

	"github.com/edgegrid/edgegrid/internal/tailscaleapi"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// refreshInterval is how often Overview's local CPU/mem sample ticks.
const refreshInterval = 3 * time.Second

// RefreshMsg ticks the local machine-stats poll. Exported so app.go can let
// it reach Dashboard.Update even while an overlay (logs/cmdbar) would
// otherwise swallow it — a self-rescheduling tick that ever gets swallowed
// without being rescheduled dies permanently.
type RefreshMsg struct{}

func refreshCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return RefreshMsg{} })
}

type tab int

const (
	tabOverview tab = iota
	tabPeers
	tabTokens
)

// chromeLines leaves room for the tab bar inside the body region App already
// sized (App subtracts its own header/footer).
const chromeLines = 3

// Dashboard is the dashboard's content model.
type Dashboard struct {
	dataDir   string
	hasTokens bool // true when tailscaleapi credentials are configured
	tab       tab

	overview overviewModel
	peers    peersModel
	tokens   tokensModel

	width, height int
}

// New builds the dashboard. tsClient is nil when this node has no Tailscale
// API credentials configured — Tokens tab is simply absent then, same as a
// pure worker having no fleet tabs in the old dashboard. lc is nil only if
// tsnet's local client couldn't be obtained — Peers then shows its error
// state instead of crashing.
func New(nodeID, tailscaleIP, dataDir string, tsClient *tailscaleapi.Client, lc *local.Client) Dashboard {
	d := Dashboard{
		dataDir:   dataDir,
		hasTokens: tsClient != nil,
		tab:       tabOverview,
		overview:  newOverviewModel(nodeID, tailscaleIP),
		peers:     newPeersModel(lc),
	}
	if tsClient != nil {
		d.tokens = newTokensModel(tsClient)
	}
	return d
}

func (d *Dashboard) resize() {
	h := max(d.height-chromeLines, 3)
	d.overview.width = d.width
	d.overview.height = h
	d.peers = d.peers.WithSize(d.width, h)
	d.tokens = d.tokens.WithSize(d.width, h)
}

// CapturesTextInput reports whether the current view is holding focus in a
// free-form text field. Neither remaining tab has one, but App checks this
// on every dashboard, so it stays here for that contract.
func (d Dashboard) CapturesTextInput() bool { return false }

// HelpText reports the current footer hint, for app.App's chrome.
func (d Dashboard) HelpText() string {
	switch d.tab {
	case tabTokens:
		return "m mint   c copy+hide   esc hide   r revoke   tab switch   / command   q quit"
	default:
		return "Tab switch tabs   /logs   / command   q quit"
	}
}

func (d Dashboard) getTabNames() []string {
	names := []string{"Overview", "Peers"}
	if d.hasTokens {
		names = append(names, "Tokens")
	}
	return names
}

func (d Dashboard) Init() tea.Cmd {
	return tea.Batch(refreshCmd(), d.peers.Init())
}

func (d Dashboard) Update(msg tea.Msg) (Dashboard, tea.Cmd) {
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		d.width, d.height = wm.Width, wm.Height
		d.resize()
		return d, nil
	}

	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "tab" {
		switch d.tab {
		case tabOverview:
			d.tab = tabPeers
		case tabPeers:
			if d.hasTokens {
				d.tab = tabTokens
			} else {
				d.tab = tabOverview
				d.overview = d.overview.refreshLocal()
			}
		case tabTokens:
			d.tab = tabOverview
			d.overview = d.overview.refreshLocal()
		}
		return d, nil
	}

	if _, ok := msg.(RefreshMsg); ok {
		d.overview = d.overview.refreshLocal()
		return d, refreshCmd()
	}

	// peersRefreshMsg is self-rescheduling like RefreshMsg above, so it has
	// to be handled here regardless of which tab is active — routing it
	// only through the tab-switch below would drop the reschedule whenever
	// Peers isn't the visible tab, killing the ticker for good.
	if _, ok := msg.(peersRefreshMsg); ok {
		var cmd tea.Cmd
		d.peers, cmd = d.peers.Update(msg)
		return d, cmd
	}

	var cmd tea.Cmd
	switch d.tab {
	case tabTokens:
		d.tokens, cmd = d.tokens.Update(msg)
	case tabPeers:
		d.peers, cmd = d.peers.Update(msg)
	default:
		d.overview, cmd = d.overview.Update(msg)
	}
	return d, cmd
}

func (d Dashboard) View() string {
	width := d.width
	if width <= 0 {
		width = 80
	}

	var content string
	switch d.tab {
	case tabTokens:
		content = d.tokens.View()
	case tabPeers:
		content = d.peers.View()
	default:
		content = d.overview.View()
	}

	names := d.getTabNames()
	if len(names) <= 1 {
		// Single tab: no bar, straight to content — same as the old
		// pure-worker view.
		return lipgloss.NewStyle().Width(width).Render(content)
	}

	var tabParts []string
	for _, name := range names {
		var s string
		active := (name == "Overview" && d.tab == tabOverview) ||
			(name == "Peers" && d.tab == tabPeers) ||
			(name == "Tokens" && d.tab == tabTokens)
		if active {
			s = style.TabActive.Render("[ " + strings.ToUpper(name) + " ]")
		} else {
			s = style.TabInactive.Render("  " + strings.ToUpper(name) + "  ")
		}
		tabParts = append(tabParts, s)
	}
	tabRow := lipgloss.JoinHorizontal(lipgloss.Top, tabParts...)
	hint := style.Help.Render("   ( press Tab to switch )")
	bar := lipgloss.JoinHorizontal(lipgloss.Center, tabRow, hint)

	// Pad the bar out to the full width so the joined block is always
	// full-width and left-aligned. app.View centers the body horizontally,
	// which is invisible on Overview because it already fills the width — but
	// a narrower tab used to drag the tab bar into the middle of the screen
	// along with its content. Anchoring the bar here keeps the nav in the same
	// place on every tab regardless of how wide that tab's content is.
	bar = lipgloss.NewStyle().Width(width).Render(bar)

	return lipgloss.JoinVertical(lipgloss.Left, bar, content)
}
