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
	tokens   tokensModel

	width, height int
}

// New builds the dashboard. tsClient is nil when this node has no Tailscale
// API credentials configured — Tokens tab is simply absent then, same as a
// pure worker having no fleet tabs in the old dashboard.
func New(nodeID, tailscaleIP, dataDir string, tsClient *tailscaleapi.Client) Dashboard {
	d := Dashboard{
		dataDir:   dataDir,
		hasTokens: tsClient != nil,
		tab:       tabOverview,
		overview:  newOverviewModel(nodeID, tailscaleIP),
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
	d.tokens = d.tokens.WithHeight(h)
}

// CapturesTextInput reports whether the current view is holding focus in a
// free-form text field. Neither remaining tab has one, but App checks this
// on every dashboard, so it stays here for that contract.
func (d Dashboard) CapturesTextInput() bool { return false }

// HelpText reports the current footer hint, for app.App's chrome.
func (d Dashboard) HelpText() string {
	switch d.tab {
	case tabTokens:
		return "m mint   c copy   r revoke   tab switch   / command   q quit"
	default:
		if d.hasTokens {
			return "Tab switch tabs   /logs   / command   q quit"
		}
		return "/logs   / command   q quit"
	}
}

func (d Dashboard) getTabNames() []string {
	if d.hasTokens {
		return []string{"Overview", "Tokens"}
	}
	return []string{"Overview"}
}

func (d Dashboard) Init() tea.Cmd {
	return refreshCmd()
}

func (d Dashboard) Update(msg tea.Msg) (Dashboard, tea.Cmd) {
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		d.width, d.height = wm.Width, wm.Height
		d.resize()
		return d, nil
	}

	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "tab" && d.hasTokens {
		if d.tab == tabOverview {
			d.tab = tabTokens
		} else {
			d.tab = tabOverview
			d.overview = d.overview.refreshLocal()
		}
		return d, nil
	}

	if _, ok := msg.(RefreshMsg); ok {
		d.overview = d.overview.refreshLocal()
		return d, refreshCmd()
	}

	var cmd tea.Cmd
	switch d.tab {
	case tabTokens:
		d.tokens, cmd = d.tokens.Update(msg)
	default:
		d.overview, cmd = d.overview.Update(msg)
	}
	return d, cmd
}

func (d Dashboard) View() string {
	names := d.getTabNames()
	if len(names) <= 1 {
		// Single tab: no bar, straight to content — same as the old
		// pure-worker view.
		switch d.tab {
		case tabTokens:
			return d.tokens.View()
		default:
			return d.overview.View()
		}
	}

	var tabParts []string
	for _, name := range names {
		var s string
		if (name == "Overview" && d.tab == tabOverview) || (name == "Tokens" && d.tab == tabTokens) {
			s = style.TabActive.Render("[ " + strings.ToUpper(name) + " ]")
		} else {
			s = style.TabInactive.Render("  " + strings.ToUpper(name) + "  ")
		}
		tabParts = append(tabParts, s)
	}
	tabRow := lipgloss.JoinHorizontal(lipgloss.Top, tabParts...)
	hint := style.Help.Render("   ( press Tab to switch )")
	bar := lipgloss.JoinHorizontal(lipgloss.Center, tabRow, hint)

	var content string
	switch d.tab {
	case tabTokens:
		content = d.tokens.View()
	default:
		content = d.overview.View()
	}

	return lipgloss.JoinVertical(lipgloss.Left, bar, content)
}
