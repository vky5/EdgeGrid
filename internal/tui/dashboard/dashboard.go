// Package dashboard is the node's live screen about itself.
// Overview is always there; Tokens only when this profile has a Tailscale API login to make keys with
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

// how often Overview reads this machine's CPU and memory, every 3s
const refreshInterval = 3 * time.Second

// the tick that reads this machine's stats again; app.go passes it through even under an overlay
// a tick that is swallowed is never scheduled again, so the stats would stop for good
type RefreshMsg struct{}

func refreshCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return RefreshMsg{} })
}

type tab int

const (
	tabOverview tab = iota
	tabPeers
	tabHistory
	tabTokens
)

// lines kept for the tab bar inside the space App gives the dashboard
const chromeLines = 3

// the dashboard: its tabs and which one is showing
type Dashboard struct {
	dataDir   string
	hasTokens bool // true when there's a Tailscale API login
	tab       tab

	overview overviewModel
	peers    peersModel
	history  historyModel
	tokens   tokensModel

	width, height int
}

// builds the dashboard; with tsClient nil there's no Tokens tab
// lc is nil only if the Tailscale client couldn't be had, and Peers then shows an error instead of crashing
func New(nodeID, tailscaleIP, dataDir string, tsClient *tailscaleapi.Client, lc *local.Client, send SendFunc, transfers TransfersFunc) Dashboard {
	d := Dashboard{
		dataDir:   dataDir,
		hasTokens: tsClient != nil,
		tab:       tabOverview,
		overview:  newOverviewModel(nodeID, tailscaleIP),
		peers:     newPeersModel(lc, send, transfers),
		history:   newHistoryModel(HistoryFuncs{}),
	}
	if tsClient != nil {
		d.tokens = newTokensModel(tsClient)
	}
	return d
}

// connects the Peers tab to who may send this node files
// kept out of New so New doesn't gain an argument per feature
func (d Dashboard) WithTrust(t TrustFuncs) Dashboard {
	d.peers.trust = t
	d.peers = d.peers.refresh()
	return d
}

// connects the Peers tab to who may send this node tasks, and to sending tasks
func (d Dashboard) WithTasks(t TaskFuncs) Dashboard {
	d.peers.tasks = t
	d.peers = d.peers.refresh()
	return d
}

// connects the History tab to this node's transfer database
func (d Dashboard) WithHistory(h HistoryFuncs) Dashboard {
	d.history.funcs = h
	d.history = d.history.refresh()
	return d
}

func (d *Dashboard) resize() {
	h := max(d.height-chromeLines, 3)
	d.overview.width = d.width
	d.overview.height = h
	d.peers = d.peers.WithSize(d.width, h)
	d.history = d.history.WithSize(d.width, h)
	d.tokens = d.tokens.WithSize(d.width, h)
}

// whether a text field has the keyboard, e.g. Peers while a file path is typed
// so App doesn't treat the keys as its own shortcuts
func (d Dashboard) CapturesTextInput() bool {
	return d.tab == tabPeers && d.peers.capturesTextInput()
}

// the help line App shows in the footer
func (d Dashboard) HelpText() string {
	switch d.tab {
	case tabTokens:
		return "m mint   c copy+hide   esc hide   r revoke   tab switch   / command   q quit"
	case tabPeers:
		return d.peers.helpText()
	default:
		return "Tab switch tabs   /logs   / command   q quit"
	}
}

func (d Dashboard) getTabNames() []string {
	names := []string{"Overview", "Peers", "History"}
	if d.hasTokens {
		names = append(names, "Tokens")
	}
	return names
}

func (d Dashboard) Init() tea.Cmd {
	return tea.Batch(refreshCmd(), d.peers.Init(), d.history.Init())
}

func (d Dashboard) Update(msg tea.Msg) (Dashboard, tea.Cmd) {
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		d.width, d.height = wm.Width, wm.Height
		d.resize()
		return d, nil
	}

	// not while typing a path, or tab would switch away and lose what was typed
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "tab" && !d.CapturesTextInput() {
		switch d.tab {
		case tabOverview:
			d.tab = tabPeers
		case tabPeers:
			d.tab = tabHistory
		case tabHistory:
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

	// handled whatever tab is showing: the Peers and History ticks would otherwise stop for good on another tab,
	// and manifestBuiltMsg and blobSentMsg arrive only once, so switching tabs mid-hash
	// or mid-transfer would throw the result away
	switch msg.(type) {
	case peersRefreshMsg, manifestBuiltMsg, blobSentMsg, transferTickMsg:
		var cmd tea.Cmd
		d.peers, cmd = d.peers.Update(msg)
		return d, cmd
	case historyRefreshMsg:
		var cmd tea.Cmd
		d.history, cmd = d.history.Update(msg)
		return d, cmd
	}

	var cmd tea.Cmd
	switch d.tab {
	case tabTokens:
		d.tokens, cmd = d.tokens.Update(msg)
	case tabPeers:
		d.peers, cmd = d.peers.Update(msg)
	case tabHistory:
		d.history, cmd = d.history.Update(msg)
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
	case tabHistory:
		content = d.history.View()
	default:
		content = d.overview.View()
	}

	names := d.getTabNames()
	if len(names) <= 1 {
		// only one tab, so no tab bar
		return lipgloss.NewStyle().Width(width).Render(content)
	}

	var tabParts []string
	for _, name := range names {
		var s string
		active := (name == "Overview" && d.tab == tabOverview) ||
			(name == "Peers" && d.tab == tabPeers) ||
			(name == "History" && d.tab == tabHistory) ||
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

	// pad the tab bar to full width so it stays put when switching tabs
	// app.View centres the body, so a narrow tab would pull the bar to the middle
	bar = lipgloss.NewStyle().Width(width).Render(bar)

	return lipgloss.JoinVertical(lipgloss.Left, bar, content)
}
