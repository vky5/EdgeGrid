// Package dashboard is the ongoing admin/monitor TUI — what the deleted
// Next.js web app used to do, talking to a running coordinator through
// internal/tui/client.
//
// Dashboard renders content only (tab bar + tables) — the "/" command bar,
// logs overlay, and global quit key all live one level up in
// internal/tui/app, shared with onboarding, so there's one implementation
// of that chrome, not two.
package dashboard

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/tui/client"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// refreshInterval is how often the Workers tab re-polls the coordinator —
// the list/stats aren't push-updated, so this is what makes them "live".
const refreshInterval = 3 * time.Second

// RefreshMsg ticks the Workers tab's live poll. Exported so app.go can let
// it reach Dashboard.Update even while an overlay (logs/connect/cmdbar) or
// onboarding mode would otherwise swallow it — a self-rescheduling tick
// that ever gets swallowed without being rescheduled dies permanently.
type RefreshMsg struct{}

func refreshCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return RefreshMsg{} })
}

type tab int

const (
	tabJobs tab = iota
	tabWorkers
	tabAdmin
)

var tabNames = []string{"Jobs", "Workers", "Admin"}

// jobsView is which of jobsList/jobDetail/submitJob the Jobs tab shows.
type jobsView int

const (
	jobsViewList jobsView = iota
	jobsViewDetail
	jobsViewSubmit
)

// chromeLines leaves room for the tab bar (this package) plus the header/
// footer bars app.App renders around it.
const chromeLines = 4

// Dashboard is the dashboard's content model — see package doc for what it
// deliberately doesn't own.
type Dashboard struct {
	client client.Client
	coord  string
	tab    tab

	jobsView  jobsView
	jobsList  jobsListModel
	jobDetail jobDetailModel
	submitJob submitJobModel

	workersList workersListModel
	admin       adminModel

	width, height int
}

func New(c client.Client, coord string) Dashboard {
	return Dashboard{
		client:      c,
		coord:       coord,
		tab:         tabJobs,
		jobsView:    jobsViewList,
		jobsList:    newJobsListModel(c),
		workersList: newWorkersListModel(c),
		admin:       newAdminModel(c),
	}
}

func (d *Dashboard) Init() tea.Cmd {
	return tea.Batch(
		refreshCmd(),
		d.jobsList.Init(),
		d.workersList.Init(),
	)
}

func (d *Dashboard) resizeTables() {
	h := max(d.height-chromeLines, 3)
	d.jobsList = d.jobsList.WithSize(d.width, h)
	d.workersList = d.workersList.WithSize(d.width, h)
	d.admin.table.SetHeight(h)
	d.admin.table.SetWidth(d.width)
}

// Coord reports the connected coordinator address, for app.App's header.
func (d Dashboard) Coord() string { return d.coord }

// HelpText reports the current footer hint, for app.App's chrome.
func (d Dashboard) HelpText() string {
	switch d.tab {
	case tabJobs:
		switch d.jobsView {
		case jobsViewList:
			return "pgup/pgdn scroll logs   n new job   x cancel   tab switch   / command   q quit"
		case jobsViewDetail:
			return "esc back"
		case jobsViewSubmit:
			return "ctrl+s submit   esc cancel"
		}
	case tabWorkers:
		return "↑/↓ select   tab switch   / command   q quit"
	case tabAdmin:
		return "a approve   r reject   tab switch   / command   q quit"
	}
	return ""
}

func (d Dashboard) Update(msg tea.Msg) (Dashboard, tea.Cmd) {
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		d.width, d.height = wm.Width, wm.Height
		d.resizeTables()
		return d, nil
	}

	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "tab" {
		d.tab = (d.tab + 1) % tab(len(tabNames))
		if d.tab == tabWorkers {
			return d, d.workersList.Init()
		}
		if d.tab == tabJobs {
			return d, d.jobsList.Init()
		}
		if d.tab == tabAdmin {
			d.admin = d.admin.refresh()
		}
		return d, nil
	}

	switch msg := msg.(type) {
	case jobDetailMsg:
		d.jobDetail = newJobDetailModel(d.client, msg.jobID)
		d.jobsView = jobsViewDetail
		return d, d.jobDetail.Init()
	case newJobMsg:
		d.submitJob = newSubmitJobModel(d.client)
		d.jobsView = jobsViewSubmit
		return d, nil
	case backToJobsMsg:
		d.jobsView = jobsViewList
		d.jobsList = newJobsListModel(d.client)
		d.resizeTables()
		return d, nil
	case jobSubmittedMsg:
		d.jobsView = jobsViewList
		d.jobsList = newJobsListModel(d.client)
		d.resizeTables()
		return d, nil
	case RefreshMsg:
		d.workersList = d.workersList.refresh()
		d.jobsList = d.jobsList.refresh()
		d.admin = d.admin.refresh()
		return d, refreshCmd()
	}

	var cmd tea.Cmd
	switch d.tab {
	case tabJobs:
		switch d.jobsView {
		case jobsViewList:
			d.jobsList, cmd = d.jobsList.Update(msg)
		case jobsViewDetail:
			d.jobDetail, cmd = d.jobDetail.Update(msg)
		case jobsViewSubmit:
			d.submitJob, cmd = d.submitJob.Update(msg)
		}
	case tabWorkers:
		d.workersList, cmd = d.workersList.Update(msg)
	case tabAdmin:
		d.admin, cmd = d.admin.Update(msg)
	}
	return d, cmd
}

func (d Dashboard) View() string {
	var tabParts []string
	for i, name := range tabNames {
		var s string
		if tab(i) == d.tab {
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
	case tabJobs:
		switch d.jobsView {
		case jobsViewList:
			content = d.jobsList.View()
		case jobsViewDetail:
			content = d.jobDetail.View()
		case jobsViewSubmit:
			content = d.submitJob.View()
		}
	case tabWorkers:
		content = d.workersList.View()
	case tabAdmin:
		content = d.admin.View()
	}

	return lipgloss.JoinVertical(lipgloss.Left, bar, content)
}
