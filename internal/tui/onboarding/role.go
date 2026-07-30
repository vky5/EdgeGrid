package onboarding

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/tui/style"
	"github.com/edgegrid/edgegrid/internal/worker/hardware"
)

// Role identifies which of the three node shapes agent.NewAgent supports.
type Role int

const (
	RolePrimaryCoordinator Role = iota
	RoleSecondaryCoordinator
	RoleWorker
)

type roleOption struct {
	role  Role
	label string
	desc  string
}

var roleOptions = []roleOption{
	{
		RolePrimaryCoordinator,
		"Start a new cluster",
		"Bootstrap the first coordinator node.\nExposes the control plane ports to allow coordinators and workers to join.",
	},
	{
		RoleSecondaryCoordinator,
		"Join as a coordinator",
		"Add another coordinator node to the cluster.\nSyncs consensus databases to provide state sync and redundant backup.",
	},
	{
		RoleWorker,
		"Join as a worker",
		"Contribute computing hardware to the cluster.\nExposes local CPU and GPU resource slots to execute workflow workloads.",
	},
}

// roleChosenMsg is emitted when the user confirms a role, so wizard.go can
// advance to the next step.
type roleChosenMsg struct {
	role Role
}

// BackToDashboardMsg is emitted when the user wants to escape onboarding completely
type BackToDashboardMsg struct{}

type TickMsg struct{}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*250, func(t time.Time) tea.Msg {
		return TickMsg{}
	})
}

type roleModel struct {
	cursor          int
	config          *config.Config
	editMode        bool
	editIndex       int
	textInput       textinput.Model
	confirmMode     bool
	confirmYes      bool
	tempNatsPort    string
	tempClusterPort string
	tempClusterName string
	tempJoinURL     string
	tempExecutor    string
	tempReqApproval bool
	tempTSHostname  string
	width, height   int
	frame           int
	hwSpec          hardware.Spec
}

func newRoleModel(cfg *config.Config) roleModel {
	return roleModel{
		config: cfg,
		hwSpec: hardware.Detect(),
	}
}

func (m roleModel) WithSize(width, height int) roleModel {
	m.width = width
	m.height = height
	return m
}

func (m roleModel) Init() tea.Cmd {
	return tickCmd()
}

func (m roleModel) currentFields() []string {
	role := roleOptions[m.cursor].role
	switch role {
	case RolePrimaryCoordinator:
		return []string{"NATS Port", "Gossip Port", "Cluster Name", "Tailscale Hostname"}
	case RoleSecondaryCoordinator:
		return []string{"Join URL", "Gossip Port", "Tailscale Hostname"}
	case RoleWorker:
		return []string{"Join URL", "Executor", "Require Approval (true/false)", "Tailscale Hostname"}
	}
	return nil
}

func (m roleModel) enterEditMode() roleModel {
	m.editMode = true
	m.editIndex = 0
	m.confirmMode = false

	m.tempNatsPort = strconv.Itoa(m.config.NATSPort)
	m.tempClusterPort = strconv.Itoa(m.config.ClusterPort)
	m.tempClusterName = m.config.ClusterName
	m.tempJoinURL = m.config.JoinURL
	m.tempExecutor = m.config.Client.Executor
	m.tempReqApproval = m.config.Client.RequireApproval
	m.tempTSHostname = m.config.TailscaleHostname

	m.textInput = textinput.New()
	m.textInput.Focus()
	m.textInput.CharLimit = 128
	m.textInput.Width = 24
	m.textInput.Prompt = "  > "
	m.textInput.PromptStyle = lipgloss.NewStyle().Foreground(style.Accent)
	m.textInput.TextStyle = lipgloss.NewStyle().Foreground(style.Accent).Bold(true)

	m = m.setupInputForField()
	return m
}

func (m roleModel) setupInputForField() roleModel {
	fields := m.currentFields()
	if m.editIndex < 0 || m.editIndex >= len(fields) {
		return m
	}

	fieldName := fields[m.editIndex]
	var val string

	switch fieldName {
	case "NATS Port":
		val = m.tempNatsPort
	case "Gossip Port":
		val = m.tempClusterPort
	case "Cluster Name":
		val = m.tempClusterName
	case "Tailscale Hostname":
		val = m.tempTSHostname
	case "Join URL":
		val = m.tempJoinURL
	case "Executor":
		val = m.tempExecutor
	case "Require Approval (true/false)":
		if m.tempReqApproval {
			val = "true"
		} else {
			val = "false"
		}
	}

	m.textInput.SetValue(val)
	m.textInput.SetCursor(len(val))
	return m
}

func (m roleModel) saveCurrentField() roleModel {
	fields := m.currentFields()
	if m.editIndex < 0 || m.editIndex >= len(fields) {
		return m
	}

	fieldName := fields[m.editIndex]
	val := strings.TrimSpace(m.textInput.Value())

	switch fieldName {
	case "NATS Port":
		m.tempNatsPort = val
	case "Gossip Port":
		m.tempClusterPort = val
	case "Cluster Name":
		m.tempClusterName = val
	case "Tailscale Hostname":
		m.tempTSHostname = val
	case "Join URL":
		m.tempJoinURL = val
	case "Executor":
		m.tempExecutor = val
	case "Require Approval (true/false)":
		m.tempReqApproval = strings.ToLower(val) == "true" || val == "1" || strings.ToLower(val) == "yes"
	}
	return m
}

func (m roleModel) applyTempConfig() roleModel {
	role := roleOptions[m.cursor].role
	switch role {
	case RolePrimaryCoordinator:
		if p, err := strconv.Atoi(m.tempNatsPort); err == nil {
			m.config.NATSPort = p
		}
		if p, err := strconv.Atoi(m.tempClusterPort); err == nil {
			m.config.ClusterPort = p
		}
		m.config.ClusterName = m.tempClusterName
		m.config.TailscaleHostname = m.tempTSHostname
	case RoleSecondaryCoordinator:
		m.config.JoinURL = m.tempJoinURL
		if p, err := strconv.Atoi(m.tempClusterPort); err == nil {
			m.config.ClusterPort = p
		}
		m.config.TailscaleHostname = m.tempTSHostname
	case RoleWorker:
		m.config.JoinURL = m.tempJoinURL
		m.config.Client.Executor = m.tempExecutor
		m.config.Client.RequireApproval = m.tempReqApproval
		m.config.TailscaleHostname = m.tempTSHostname
	}
	return m
}

func (m roleModel) Update(msg tea.Msg) (roleModel, tea.Cmd) {
	switch msg := msg.(type) {
	case TickMsg:
		m.frame++
		return m, tickCmd()
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}

	if m.editMode {
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "esc":
				m.editMode = false
				return m, nil
			case "up":
				m = m.saveCurrentField()
				if m.editIndex > 0 {
					m.editIndex--
				} else {
					m.editIndex = len(m.currentFields()) - 1
				}
				m = m.setupInputForField()
				return m, nil
			case "down":
				m = m.saveCurrentField()
				if m.editIndex < len(m.currentFields())-1 {
					m.editIndex++
				} else {
					m.editIndex = 0
				}
				m = m.setupInputForField()
				return m, nil
			case "enter":
				m = m.saveCurrentField()
				if m.editIndex < len(m.currentFields())-1 {
					m.editIndex++
					m = m.setupInputForField()
					return m, nil
				} else {
					m.editMode = false
					m.confirmMode = true
					m.confirmYes = true
					return m, nil
				}
			}
		}
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}

	if m.confirmMode {
		if msg, ok := msg.(tea.KeyMsg); ok {
			switch msg.String() {
			case "left", "h", "tab", "shift+tab":
				m.confirmYes = !m.confirmYes
				return m, nil
			case "right", "l":
				m.confirmYes = !m.confirmYes
				return m, nil
			case "y":
				m = m.applyTempConfig()
				m.confirmMode = false
				return m, nil
			case "n", "esc":
				m.confirmMode = false
				return m, nil
			case "enter":
				if m.confirmYes {
					m = m.applyTempConfig()
				}
				m.confirmMode = false
				return m, nil
			}
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(roleOptions)-1 {
				m.cursor++
			}
		case "e":
			m = m.enterEditMode()
			return m, nil
		case "esc":
			if !m.editMode && !m.confirmMode {
				return m, func() tea.Msg { return BackToDashboardMsg{} }
			}
		case "enter":
			role := roleOptions[m.cursor].role
			return m, func() tea.Msg {
				return roleChosenMsg{role: role}
			}
		}
	}
	return m, nil
}

var (
	greenColor   = lipgloss.Color("42")
	addedStyle   = lipgloss.NewStyle().Foreground(greenColor).Bold(true)
	removedStyle = lipgloss.NewStyle().Foreground(style.Danger).Strikethrough(true)
)

func (m roleModel) renderArt() string {
	role := roleOptions[m.cursor].role
	hostName, _ := os.Hostname()
	if hostName == "" {
		hostName = "localhost"
	}
	if len(hostName) > 13 {
		hostName = hostName[:13]
	}

	switch role {
	case RolePrimaryCoordinator:
		hostName30 := hostName
		if len(hostName30) > 30 {
			hostName30 = hostName30[:30]
		}
		coordBox := lipgloss.NewStyle().Foreground(greenColor).Bold(true).Render(
			fmt.Sprintf(`            ┌─────────────────────────────────────────┐
            │           PRIMARY COORDINATOR           │
            │                 ( YOU )                 │
            ├─────────────────────────────────────────┤
            │  STATUS : ACTIVE CONSENSUS LEADER       │
            │  PORT   : 4222 / 6222                   │
            │  HOST   : %-30s│
            └─────────────────────────────────────────┘`, hostName30))

		primaryArtLines := []string{
			`                         o
                 ┌───────┴───────┐
                 ▼               ▼`,
			`                         │
                 ┌───────o───────┐
                 ▼               ▼`,
			`                         │
                 ┌───────┴───────┐
                 o               o`,
		}
		lines := lipgloss.NewStyle().Foreground(style.Accent).Render(primaryArtLines[m.frame%len(primaryArtLines)])

		workers := lipgloss.NewStyle().Foreground(style.Help.GetForeground()).Render(
			`           ┌─────────────────────┐     ┌─────────────────────┐
           │     WORKER NODE     │     │     WORKER NODE     │
           │       [W_01]        │     │       [W_02]        │
           ├─────────────────────┤     ├─────────────────────┤
           │ STATUS: ACTIVE      │     │ STATUS: ACTIVE      │
           │ VRAM  : 24GB        │     │ VRAM  : 24GB        │
           └─────────────────────┘     └─────────────────────┘`)

		return coordBox + "\n" + lines + "\n" + workers

	case RoleSecondaryCoordinator:
		c1Box := lipgloss.NewStyle().Foreground(style.Accent).Render(
			` ┌──────────────────────┐
 │ PRIMARY COORD (C1)   │
 │                      │
 ├──────────────────────┤
 │ STATUS: ACTIVE       │
 │ ENDPT : 10.0.0.10    │
 └──────────────────────┘`)

		c2Box := lipgloss.NewStyle().Foreground(greenColor).Bold(true).Render(
			fmt.Sprintf(` ┌──────────────────────┐
 │ SECONDARY COORD (C2) │
 │   (YOU - JOINING)    │
 ├──────────────────────┤
 │ STATUS: SYNCING      │
 │ HOST  : %-13s│
 └──────────────────────┘`, hostName))

		header := lipgloss.JoinHorizontal(lipgloss.Top, c1Box, "     ", c2Box)

		secondaryArtConnectors := []string{
			`             │                            │
             │         ◄────o────►        │
             ▼          REPLICATE         ▼`,
			`             │                            │
             │         ◄─o─────o─►        │
             ▼          REPLICATE         ▼`,
			`             │                            │
             │         ◄─────────►        │
             ▼          REPLICATE         ▼`,
		}
		connectors := lipgloss.NewStyle().Foreground(style.Accent).Render(secondaryArtConnectors[m.frame%len(secondaryArtConnectors)])

		w1Box := lipgloss.NewStyle().Foreground(style.Help.GetForeground()).Render(
			` ┌──────────────────────┐
 │   WORKER POOL (C1)   │
 │  [W_01] [W_02] [W_03]│
 └──────────────────────┘`)

		w2Box := lipgloss.NewStyle().Foreground(style.Help.GetForeground()).Render(
			` ┌──────────────────────┐
 │   WORKER POOL (C2)   │
 │  [W_04] [W_05] [W_06]│
 └──────────────────────┘`)

		footer := lipgloss.JoinHorizontal(lipgloss.Top, w1Box, "     ", w2Box)

		return header + "\n" + connectors + "\n" + footer

	case RoleWorker:
		coordBox := lipgloss.NewStyle().Foreground(style.Accent).Render(
			`            ┌───────────────────────────────────┐
            │      CLUSTER COORDINATOR (C1)     │
            ├───────────────────────────────────┤
            │  STATUS  : ONLINE (LEADER)        │
            │  ENDPOINT: 10.0.0.10:4222         │
            └───────────────────────────────────┘`)

		workerArtConnectors := []string{
			`                         ▲
                         │  (SENDING COMPUTE)
                         │`,
			`                         ▲  o
                         │  (SENDING COMPUTE)
                         │`,
			`                         ▲  o  o
                         │  (SENDING COMPUTE)
                         │`,
		}
		connectors := lipgloss.NewStyle().Foreground(style.Help.GetForeground()).Render(workerArtConnectors[m.frame%len(workerArtConnectors)])

		fanChars := []string{`[  /  ]`, `[  -  ]`, `[  \  ]`, `[  |  ]`}
		fan1 := fanChars[m.frame%4]
		fan2 := fanChars[(m.frame+2)%4]

		gpuStr := "None (CPU Only)"
		hardwareStr := "CPU Only Host"
		vramStr := fmt.Sprintf("%.0f GB RAM / %.0f GB Disk", m.hwSpec.RAMGB, m.hwSpec.DiskFreeGB)
		if m.hwSpec.HasGPU {
			gpuStr = "ACTIVE"
			hardwareStr = m.hwSpec.GPUName
			if len(hardwareStr) > 30 {
				hardwareStr = hardwareStr[:30]
			}
			vramStr = fmt.Sprintf("%.1f GB VRAM / %.0f GB RAM", m.hwSpec.GPUVramGB, m.hwSpec.RAMGB)
		}

		cLine := func(text string) string {
			return "       │ " + padLabel(text, 43) + " │"
		}

		workerRack := lipgloss.NewStyle().Foreground(greenColor).Bold(true).Render(
			`       ┌─────────────────────────────────────────────┐` + "\n" +
			cLine(centerText("WORKER CHASSIS (YOU)", 43)) + "\n" +
			`       ├─────────────────────────────────────────────┤` + "\n" +
			cLine("  FANS    : "+fan1+" "+fan2) + "\n" +
			cLine("  GPU_CORE: "+gpuStr) + "\n" +
			cLine("  HARDWARE: "+hardwareStr) + "\n" +
			cLine("  MEMORY  : "+vramStr) + "\n" +
			cLine("  EXECUTOR: Docker Container Runner") + "\n" +
			`       └─────────────────────────────────────────────┘`)

		return coordBox + "\n" + connectors + "\n" + workerRack
	}
	return ""
}

func padLabel(label string, width int) string {
	if len(label) >= width {
		return label
	}
	return label + strings.Repeat(" ", width-len(label))
}

func centerText(text string, width int) string {
	if len(text) >= width {
		return text
	}
	padding := width - len(text)
	left := padding / 2
	right := padding - left
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
}

func (m roleModel) renderHint(w int) string {
	role := roleOptions[m.cursor].role
	var text string
	switch role {
	case RolePrimaryCoordinator:
		text = "INFO: Bootstraps the new cluster. Coordinates job orchestrations, node metadata, consensus registries, and networks."
	case RoleSecondaryCoordinator:
		text = "INFO: Joins an existing cluster. Replicates databases, consensus logs, and state machines to provide zero-downtime failover."
	case RoleWorker:
		text = "INFO: Connects to coordinate pipelines. Contributes processing units (CPU/GPU cores, VRAM) to execute container workloads."
	}

	return lipgloss.NewStyle().
		Foreground(style.Help.GetForeground()).
		Width(max(w-4, 15)).
		Render(text)
}

func (m roleModel) View() string {
	wTotal := m.width
	if wTotal <= 0 {
		wTotal = 80 // fallback
	}

	wInner := wTotal - 10
	if wInner < 70 {
		wInner = 70
	}

	// Proportional allocation based on the full screen width
	wSelection := int(float64(wInner) * 0.32)
	wSettings := int(float64(wInner) * 0.28)

	// Enforce minimum constraints
	if wSelection < 28 {
		wSelection = 28
	}
	if wSettings < 22 {
		wSettings = 22
	}
	wRightHalf := wInner - wSelection - wSettings - 2
	if wRightHalf < 30 {
		wRightHalf = 30
		wSettings = wInner - wSelection - wRightHalf - 2
	}

	leftPaneStyle := lipgloss.NewStyle().
		Width(wSelection).
		MaxWidth(wSelection).
		Padding(1, 1)


	middlePaneStyle := lipgloss.NewStyle().
		Width(wSettings).
		Padding(1, 1)

	middlePaneActiveStyle := lipgloss.NewStyle().
		Width(wSettings).
		Padding(1, 1)

	// Left Pane Content
	var leftLines []string
	leftLines = append(leftLines, style.Title.Render("What is this node?"), "")
	for i, opt := range roleOptions {
		if i > 0 {
			leftLines = append(leftLines, "", "  "+style.Help.Render(strings.Repeat("┄", max(wSelection-4, 10))), "")
		} else {
			leftLines = append(leftLines, "")
		}
		cursor := "  "
		label := opt.label
		if i == m.cursor {
			cursor = "› "
			label = style.Selected.Render(label)
		}
		leftLines = append(leftLines, cursor+label, "")
	}

	if !m.editMode && !m.confirmMode {
		leftLines = append(leftLines, "", style.Help.Render("Press 'e' to edit settings"))
	} else if m.editMode {
		leftLines = append(leftLines, "", style.Help.Render("Editing settings..."))
	} else if m.confirmMode {
		leftLines = append(leftLines, "", style.Help.Render("Confirming settings..."))
	}

	leftContent := leftPaneStyle.Render(lipgloss.JoinVertical(lipgloss.Left, leftLines...))

	// Middle Pane Content
	var middleLines []string
	role := roleOptions[m.cursor].role
	var roleLabel string
	switch role {
	case RolePrimaryCoordinator:
		roleLabel = "Primary Coordinator"
	case RoleSecondaryCoordinator:
		roleLabel = "Secondary Coordinator"
	case RoleWorker:
		roleLabel = "Worker"
	}

	middleLines = append(middleLines, style.Title.Render(roleLabel+" Settings"), "")

	labelPadLen := 20
	if wSettings > 28 {
		labelPadLen = 22
	}

	if m.editMode {
		fields := m.currentFields()
		middleLines = append(middleLines, style.Help.Render("Press enter to save, esc to cancel"), style.Help.Render("Press ↑/↓ to navigate fields"), "")
		for i, field := range fields {
			var cursor string
			var val string

			switch field {
			case "NATS Port":
				val = m.tempNatsPort
			case "Gossip Port":
				val = m.tempClusterPort
			case "Cluster Name":
				val = m.tempClusterName
			case "Tailscale Hostname":
				val = m.tempTSHostname
			case "Join URL":
				val = m.tempJoinURL
			case "Executor":
				val = m.tempExecutor
			case "Require Approval":
				if m.tempReqApproval {
					val = "true"
				} else {
					val = "false"
				}
			}

			if i > 0 {
				middleLines = append(middleLines, "")
			}

			if i == m.editIndex {
				cursor = "▸ "
				middleLines = append(middleLines, cursor+lipgloss.NewStyle().Bold(true).Render(padLabel(field+":", labelPadLen))+m.textInput.View())
			} else {
				cursor = "  "
				middleLines = append(middleLines, cursor+style.Help.Render(padLabel(field+":", labelPadLen))+val)
			}
		}
	} else if m.confirmMode {
		middleLines = append(middleLines, style.Title.Render("Confirm Changes?"), "")

		fields := m.currentFields()
		for i, field := range fields {
			var orig, temp string
			switch field {
			case "NATS Port":
				orig = strconv.Itoa(m.config.NATSPort)
				temp = m.tempNatsPort
			case "Gossip Port":
				orig = strconv.Itoa(m.config.ClusterPort)
				temp = m.tempClusterPort
			case "Cluster Name":
				orig = m.config.ClusterName
				temp = m.tempClusterName
			case "Tailscale Hostname":
				orig = m.config.TailscaleHostname
				temp = m.tempTSHostname
			case "Join URL":
				orig = m.config.JoinURL
				temp = m.tempJoinURL
			case "Executor":
				orig = m.config.Client.Executor
				temp = m.tempExecutor
			case "Require Approval":
				orig = strconv.FormatBool(m.config.Client.RequireApproval)
				temp = strconv.FormatBool(m.tempReqApproval)
			}

			if i > 0 {
				middleLines = append(middleLines, "")
			}

			if orig != temp {
				middleLines = append(middleLines, "  "+style.Help.Render(padLabel(field+":", labelPadLen))+removedStyle.Render(orig)+" -> "+addedStyle.Render(temp))
			} else {
				middleLines = append(middleLines, "  "+style.Help.Render(padLabel(field+":", labelPadLen))+orig+" (unchanged)")
			}
		}

		middleLines = append(middleLines, "")
		saveBtn := "Save"
		cancelBtn := "Cancel"

		var saveStr, cancelStr string
		if m.confirmYes {
			saveStr = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(style.Accent).Padding(0, 1).Bold(true).Render(saveBtn)
			cancelStr = lipgloss.NewStyle().Foreground(style.Muted).Padding(0, 1).Render(cancelBtn)
		} else {
			saveStr = lipgloss.NewStyle().Foreground(style.Muted).Padding(0, 1).Render(saveBtn)
			cancelStr = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(style.Danger).Padding(0, 1).Bold(true).Render(cancelBtn)
		}
		middleLines = append(middleLines, "  "+saveStr+"   "+cancelStr)
	} else {
		fields := m.currentFields()
		for i, field := range fields {
			var val string
			switch field {
			case "NATS Port":
				val = strconv.Itoa(m.config.NATSPort)
			case "Gossip Port":
				val = strconv.Itoa(m.config.ClusterPort)
			case "Cluster Name":
				val = m.config.ClusterName
			case "Tailscale Hostname":
				val = m.config.TailscaleHostname
			case "Join URL":
				val = m.config.JoinURL
			case "Executor":
				val = m.config.Client.Executor
			case "Require Approval":
				val = strconv.FormatBool(m.config.Client.RequireApproval)
			}

			if i > 0 {
				middleLines = append(middleLines, "")
			}

			middleLines = append(middleLines, "  "+style.Help.Render(padLabel(field+":", labelPadLen))+lipgloss.NewStyle().Bold(true).Render(val))
		}
	}

	var middleContent string
	if m.editMode || m.confirmMode {
		middleContent = middlePaneActiveStyle.Render(lipgloss.JoinVertical(lipgloss.Left, middleLines...))
	} else {
		middleContent = middlePaneStyle.Render(lipgloss.JoinVertical(lipgloss.Left, middleLines...))
	}

	// Dynamic divider height based on the terminal height
	hTarget := m.height - 8
	if hTarget < 12 {
		hTarget = 12
	}
	var divLines []string
	for i := 0; i < hTarget; i++ {
		divLines = append(divLines, "│")
	}
	divider := lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Join(divLines, "\n"))

	// Right Pane content (Centered ASCII Art in top box, absolute bottom position for separator + info hint)
	artText := m.renderArt()
	hintText := m.renderHint(wRightHalf)
	dividerWidth := max(wRightHalf-4, 10)
	if dividerWidth > 34 {
		dividerWidth = 34
	}

	topContent := lipgloss.Place(wRightHalf, hTarget-5, lipgloss.Center, lipgloss.Center, artText)

	bottomBlock := lipgloss.JoinVertical(lipgloss.Center,
		style.Help.Render(strings.Repeat("─", dividerWidth)),
		"",
		hintText,
	)
	bottomContent := lipgloss.Place(wRightHalf, 5, lipgloss.Center, lipgloss.Bottom, bottomBlock)

	artContent := lipgloss.JoinVertical(lipgloss.Center, topContent, bottomContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftContent, divider, middleContent, divider, artContent)
}
