// Package dashboard is the ongoing admin/monitor TUI — what the deleted
// Next.js web app used to do, talking to a running coordinator through
// internal/tui/client.
//
// Dashboard renders content only (tab bar + tables) — the "/" command bar,
// logs overlay, and global quit key all live one level up in
// internal/tui/app, shared with onboarding, so there's one implementation
// of that chrome, not two.
//
// Both workers and coordinators get Overview (local machine + job tasks).
// Pure workers only get Overview — they solve jobs over NATS and cannot
// submit or admin the grid. Coordinators get Overview + Jobs + Workers + Admin.
package dashboard

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/tui/client"
	"github.com/edgegrid/edgegrid/internal/tui/style"
	"github.com/edgegrid/edgegrid/internal/worker"
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
	tabOverview tab = iota
	tabJobs
	tabWorkers
	tabAdmin
	tabSettings
)

// jobsView is which of jobsList/jobDetail/submitJob the Jobs tab shows.
type jobsView int

const (
	jobsViewList jobsView = iota
	jobsViewDetail
	jobsViewSubmit
)

// chromeLines leaves room for the tab bar inside the body region App already
// sized (App subtracts its own header/footer). Too small and Jobs/Workers
// tables overflow and look “broken”; 3 ≈ tab row + padding.
const chromeLines = 3

// Dashboard is the dashboard's content model — see package doc for what it
// deliberately doesn't own.
type Dashboard struct {
	client   client.Client
	coord    string
	dataDir  string
	isWorker bool
	tab      tab

	jobsView  jobsView
	jobsList  jobsListModel
	jobDetail jobDetailModel
	submitJob submitJobModel
	overview  overviewModel
	settings  settingsModel

	workersList workersListModel
	admin       adminModel

	width, height int
}

func New(c client.Client, coord string, isWorker bool, nodeID string, natsURL string, dataDir string) Dashboard {
	d := Dashboard{
		client:      c,
		coord:       coord,
		dataDir:     dataDir,
		isWorker:    isWorker,
		tab:         tabJobs, // coordinator lands on Jobs (primary ops surface)
		jobsView:    jobsViewList,
		jobsList:    newJobsListModel(c),
		workersList: newWorkersListModel(c),
		admin:       newAdminModel(c),
		overview:    newOverviewModel(nodeID, natsURL, isWorker),
		submitJob:   newSubmitJobModel(c, clientIsHTTP(c)),
		settings:    newSettingsModel(dataDir),
	}
	if isWorker {
		d.tab = tabOverview // workers only have Overview
	}
	return d
}

// clientIsHTTP is true only for a real coordinator HTTP client — not Stub.
func clientIsHTTP(c client.Client) bool {
	_, ok := c.(*client.HTTP)
	return ok
}

func (d *Dashboard) Init() tea.Cmd {
	if d.isWorker {
		// Overview only — no jobs/workers admin poll.
		return nil
	}
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
	d.overview.width = d.width
	d.overview.height = h
	d.submitJob = d.submitJob.WithSize(d.width, h)
	d.settings.width = d.width
	d.settings.height = h
}

func (d *Dashboard) newSubmitJob() submitJobModel {
	h := max(d.height-chromeLines, 3)
	return newSubmitJobModel(d.client, clientIsHTTP(d.client)).WithSize(d.width, h)
}

// WithWorkerRuntime pushes in-process agent status into Overview for both
// pure workers and coordinators that run a worker loop.
func (d *Dashboard) WithWorkerRuntime(
	agentUp, busy bool,
	jobIDs []string,
	doneOK, doneFail int,
	recent []worker.FinishedJob,
) {
	d.overview = d.overview.WithAgentRuntime(agentUp, busy, jobIDs, doneOK, doneFail, recent)
	d.overview = d.overview.refreshLocal()
}

// Coord reports the connected coordinator address, for app.App's header.
func (d Dashboard) Coord() string { return d.coord }

// CapturesTextInput reports whether the current view is holding focus in a
// free-form text field. App uses this to avoid treating "/" as the command
// bar and "q" as quit while the user is typing (e.g. file paths on submit).
func (d Dashboard) CapturesTextInput() bool {
	if d.isWorker {
		return false
	}
	if d.tab == tabJobs && d.jobsView == jobsViewSubmit {
		return d.submitJob.CapturesTextInput()
	}
	// Settings text fields + restart confirm must not trigger / or q.
	if d.tab == tabSettings {
		return true
	}
	return false
}

// HelpText reports the current footer hint, for app.App's chrome.
func (d Dashboard) HelpText() string {
	if d.isWorker {
		return "/logs   / command   q quit"
	}
	switch d.tab {
	case tabOverview:
		return "n submit job   Tab switch tabs   /logs   / command   q quit"
	case tabJobs:
		switch d.jobsView {
		case jobsViewList:
			return "pgup/pgdn scroll logs   n new job   x cancel   tab switch   / command   q quit"
		case jobsViewDetail:
			return "esc back"
		case jobsViewSubmit:
			return "Tab navigate   ctrl+o load file   ctrl+s submit   esc cancel"
		}
	case tabWorkers:
		return "↑/↓ select   tab switch   / command   q quit"
	case tabAdmin:
		return "a approve   r reject   tab switch   / command   q quit"
	case tabSettings:
		return "↑/↓ fields   ←/→ executor   ctrl+s save   tab switch   q quit"
	}
	return ""
}

func (d Dashboard) getTabNames() []string {
	if d.isWorker {
		return []string{"Overview"}
	}
	return []string{"Overview", "Jobs", "Workers", "Admin", "Settings"}
}

func (d Dashboard) tabFromName(name string) tab {
	switch strings.ToLower(name) {
	case "overview":
		return tabOverview
	case "jobs":
		return tabJobs
	case "workers":
		return tabWorkers
	case "admin":
		return tabAdmin
	case "settings":
		return tabSettings
	}
	return tabOverview
}

func (d Dashboard) Update(msg tea.Msg) (Dashboard, tea.Cmd) {
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		d.width, d.height = wm.Width, wm.Height
		d.resizeTables()
		return d, nil
	}

	// Pure workers: Overview only — no submit, no admin tabs.
	if d.isWorker {
		return d, nil
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		if key.String() == "n" && d.tab == tabOverview {
			d.tab = tabJobs
			d.jobsView = jobsViewSubmit
			d.submitJob = d.newSubmitJob()
			return d, d.submitJob.Init()
		}
	}

	// Tab switches top-level dashboard tabs, except on the submit form where
	// Tab / Shift+Tab cycle fields (and ctrl+o path entry). Block while the
	// settings restart confirm dialog is open.
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "tab" {
		onSubmit := d.tab == tabJobs && d.jobsView == jobsViewSubmit
		settingsConfirm := d.tab == tabSettings && d.settings.confirmRestart
		if !onSubmit && !settingsConfirm {
			names := d.getTabNames()
			if len(names) <= 1 {
				return d, nil
			}
			var currIdx int
			for i, name := range names {
				if d.tabFromName(name) == d.tab {
					currIdx = i
					break
				}
			}
			nextIdx := (currIdx + 1) % len(names)
			d.tab = d.tabFromName(names[nextIdx])
			if d.tab == tabOverview {
				d.overview = d.overview.refreshLocal()
			}
			if d.tab == tabWorkers {
				return d, d.workersList.Init()
			}
			if d.tab == tabJobs {
				d.jobsView = jobsViewList
				return d, d.jobsList.Init()
			}
			if d.tab == tabAdmin {
				d.admin = d.admin.refresh()
			}
			if d.tab == tabSettings {
				d.settings = d.settings.load()
				return d, d.settings.Init()
			}
			return d, nil
		}
	}

	switch msg := msg.(type) {
	case jobDetailMsg:
		d.jobDetail = newJobDetailModel(d.client, msg.jobID)
		d.jobsView = jobsViewDetail
		return d, d.jobDetail.Init()
	case newJobMsg:
		d.submitJob = d.newSubmitJob()
		d.jobsView = jobsViewSubmit
		return d, d.submitJob.Init()
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
		d.overview = d.overview.refreshLocal()
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
	case tabSettings:
		d.settings, cmd = d.settings.Update(msg)
	}
	return d, cmd
}

func (d Dashboard) View() string {
	var tabParts []string
	names := d.getTabNames()
	for _, name := range names {
		var s string
		if d.tabFromName(name) == d.tab {
			s = style.TabActive.Render("[ " + strings.ToUpper(name) + " ]")
		} else {
			s = style.TabInactive.Render("  " + strings.ToUpper(name) + "  ")
		}
		tabParts = append(tabParts, s)
	}
	tabRow := lipgloss.JoinHorizontal(lipgloss.Top, tabParts...)
	var hint string
	if len(names) > 1 {
		hint = style.Help.Render("   ( press Tab to switch )")
	}
	bar := lipgloss.JoinHorizontal(lipgloss.Center, tabRow, hint)

	var content string
	switch d.tab {
	case tabOverview:
		content = d.overview.View()
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
	case tabSettings:
		content = d.settings.View()
	}

	return lipgloss.JoinVertical(lipgloss.Left, bar, content)
}
