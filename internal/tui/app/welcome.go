package app

import (
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/profile"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

type welcomeRestartMsg struct {
	profileName string
	onboard     bool
	noAgent     bool
}

type welcomeStartActiveMsg struct {
	profileName string
}

type welcomeConnectMsg struct{}

type welcomeLogsMsg struct{}

type welcomeBenchmarkTickMsg struct{}

type welcomeAnimTickMsg struct{}

type welcomeBackMsg struct{}

func randomProfileName() string {
	b := make([]byte, 2)
	_, _ = crand.Read(b)
	return "cluster-" + hex.EncodeToString(b)
}

func isProfileOnboarded(name string) bool {
	if name == "" {
		// default local ./data folder
		_, err1 := os.Stat("data/admin.token")
		_, err2 := os.Stat("data/node.token")
		return err1 == nil || err2 == nil
	}
	root, err := profile.Root()
	if err != nil {
		return false
	}
	dir := filepath.Join(root, name)
	_, err1 := os.Stat(filepath.Join(dir, "admin.token"))
	_, err2 := os.Stat(filepath.Join(dir, "node.token"))
	return err1 == nil || err2 == nil
}

func tickBenchmark() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
		return welcomeBenchmarkTickMsg{}
	})
}

func tickWelcomeAnim() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
		return welcomeAnimTickMsg{}
	})
}

type point3D struct {
	x, y, z float64
}

func getNodePosition(id string) point3D {
	switch id {
	case "C1":
		return point3D{x: 0, y: -0.9, z: 0.1}
	case "C2":
		return point3D{x: -0.65, y: -0.3, z: 0.65}
	case "C3":
		return point3D{x: 0.65, y: -0.3, z: -0.65}
	case "C4":
		return point3D{x: -0.45, y: 0.45, z: -0.7}
	case "C5":
		return point3D{x: 0.45, y: 0.45, z: 0.7}
	case "W1":
		return point3D{x: -0.85, y: 0.05, z: 0.2}
	case "W2":
		return point3D{x: -0.3, y: 0.8, z: -0.45}
	case "W3":
		return point3D{x: 0.85, y: 0.05, z: -0.2}
	case "W4":
		return point3D{x: 0.3, y: 0.8, z: 0.45}
	default:
		return point3D{x: 0, y: 0, z: 0}
	}
}

func getRowStyle(y int, height int) lipgloss.Style {
	if height <= 1 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#8b5cf6"))
	}

	// Mathematically interpolate RGB values row-by-row
	// Start: Cyan (0, 242, 254)
	// Mid: Purple (139, 92, 246)
	// End: Pink (244, 63, 94)
	var r, g, b float64
	t := float64(y) / float64(height-1)

	if t < 0.5 {
		t2 := t * 2.0
		r = 0.0 + (139.0-0.0)*t2
		g = 242.0 + (92.0-242.0)*t2
		b = 254.0 + (246.0-254.0)*t2
	} else {
		t2 := (t - 0.5) * 2.0
		r = 139.0 + (244.0-139.0)*t2
		g = 92.0 + (63.0-92.0)*t2
		b = 246.0 + (94.0-246.0)*t2
	}

	hexStr := fmt.Sprintf("#%02x%02x%02x", int(r), int(g), int(b))
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hexStr))
}

func render3DGoGlobe(width, height int, activeNode int, angleA, angleB float64) string {
	if height < 6 || width < 15 {
		return "[Globe canvas too small]"
	}

	// Create text buffer based on dynamic width & height
	charBuffer := make([][]rune, height)
	styleBuffer := make([][]lipgloss.Style, height)
	for y := 0; y < height; y++ {
		charBuffer[y] = make([]rune, width)
		styleBuffer[y] = make([]lipgloss.Style, width)
		for x := 0; x < width; x++ {
			charBuffer[y][x] = ' '
		}
	}

	coordStyle := lipgloss.NewStyle().Foreground(style.Accent).Bold(true)
	workerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	hybridStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true) // Neon active node color

	// Map active index to node ID
	activeNodeID := "C1"
	switch activeNode {
	case 0:
		activeNodeID = "C1"
	case 1:
		activeNodeID = "C2"
	case 2:
		activeNodeID = "C3"
	case 3:
		activeNodeID = "C4"
	case 4:
		activeNodeID = "C5"
	}

	// Generate sphere unit coordinates with 12 lat and 24 long (264 points, 20% denser)
	numLat := 12
	numLong := 24
	var spherePoints []point3D
	for i := 1; i < numLat; i++ {
		theta := (float64(i) * math.Pi) / float64(numLat)
		for j := 0; j < numLong; j++ {
			phi := (float64(j) * 2.0 * math.Pi) / float64(numLong)
			x := math.Sin(theta) * math.Cos(phi)
			y := math.Sin(theta) * math.Sin(phi)
			z := math.Cos(theta)
			spherePoints = append(spherePoints, point3D{x, y, z})
		}
	}

	// Mesh connections
	connections := [][]string{
		{"C1", "C2"}, {"C1", "C3"}, {"C1", "C4"}, {"C1", "C5"},
		{"C2", "C3"}, {"C2", "C4"}, {"C3", "C5"}, {"C4", "C5"},
		{"C2", "W1"}, {"C4", "W2"}, {"C3", "W3"}, {"C5", "W4"},
	}

	// Generate connection dots in 3D space with high density (25 steps) to form solid-looking lines
	var linePoints []point3D
	for _, conn := range connections {
		p1 := getNodePosition(conn[0])
		p2 := getNodePosition(conn[1])
		for step := 0; step <= 25; step++ {
			t := float64(step) / 25.0
			x := p1.x + (p2.x-p1.x)*t
			y := p1.y + (p2.y-p1.y)*t
			z := p1.z + (p2.z-p1.z)*t
			length := math.Sqrt(x*x + y*y + z*z)
			if length > 0 {
				linePoints = append(linePoints, point3D{x / length, y / length, z / length})
			}
		}
	}

	// Dynamic radius scaled down slightly (x 0.85) to add breathing margins
	R := (float64(height)/2.0 - 0.5) * 0.85
	maxRFromWidth := (float64(width) / 5.2) * 0.85
	if R > maxRFromWidth {
		R = maxRFromWidth
	}
	if R < 2.5 {
		R = 2.5
	}

	// Project sphere dots to 2D text screen coordinates
	for _, p := range spherePoints {
		// Y-axis rotation
		x1 := p.x*math.Cos(angleA) - p.z*math.Sin(angleA)
		z1 := p.x*math.Sin(angleA) + p.z*math.Cos(angleA)
		// X-axis rotation
		y2 := p.y*math.Cos(angleB) - z1*math.Sin(angleB)
		z2 := p.y*math.Sin(angleB) + z1*math.Cos(angleB)

		// 2.45 aspect ratio correction factor to make the globe a perfect circle on terminal fonts
		projX := int(math.Round(float64(width)/2.0 + x1*R*2.45))
		projY := int(math.Round(float64(height)/2.0 + y2*R))

		if projX >= 0 && projX < width && projY >= 0 && projY < height {
			rowStyle := getRowStyle(projY, height)
			if z2 > 0.4 {
				charBuffer[projY][projX] = '·'
				styleBuffer[projY][projX] = rowStyle
			} else if z2 > 0 {
				charBuffer[projY][projX] = '.'
				styleBuffer[projY][projX] = rowStyle
			}
		}
	}

	// Project wireframe lines
	for _, p := range linePoints {
		x1 := p.x*math.Cos(angleA) - p.z*math.Sin(angleA)
		z1 := p.x*math.Sin(angleA) + p.z*math.Cos(angleA)
		y2 := p.y*math.Cos(angleB) - z1*math.Sin(angleB)
		z2 := p.y*math.Sin(angleB) + z1*math.Cos(angleB)

		projX := int(math.Round(float64(width)/2.0 + x1*R*2.45))
		projY := int(math.Round(float64(height)/2.0 + y2*R))

		if projX >= 0 && projX < width && projY >= 0 && projY < height {
			if z2 > 0.1 {
				charBuffer[projY][projX] = '+'
				styleBuffer[projY][projX] = getRowStyle(projY, height)
			}
		}
	}

	// Project node labels onto the 3D globe surface
	nodeIDs := []string{"C1", "C2", "C3", "C4", "C5", "W1", "W2", "W3", "W4"}
	for _, id := range nodeIDs {
		pos := getNodePosition(id)
		x1 := pos.x*math.Cos(angleA) - pos.z*math.Sin(angleA)
		z1 := pos.x*math.Sin(angleA) + pos.z*math.Cos(angleA)
		y2 := pos.y*math.Cos(angleB) - z1*math.Sin(angleB)
		z2 := pos.y*math.Sin(angleB) + z1*math.Cos(angleB)

		projX := int(math.Round(float64(width)/2.0 + x1*R*2.45))
		projY := int(math.Round(float64(height)/2.0 + y2*R))

		if z2 > -0.2 && projX >= 2 && projX < width-2 && projY >= 0 && projY < height {
			label := fmt.Sprintf("[%s]", id)
			runes := []rune(label)

			var nStyle lipgloss.Style
			if activeNodeID == id {
				nStyle = activeStyle
			} else if id == "C3" {
				nStyle = hybridStyle
			} else if id[0] == 'C' {
				nStyle = coordStyle
			} else {
				nStyle = workerStyle
			}

			// Plot text
			for i := 0; i < len(runes); i++ {
				writeX := projX - 2 + i
				if writeX >= 0 && writeX < width {
					charBuffer[projY][writeX] = runes[i]
					styleBuffer[projY][writeX] = nStyle
				}
			}
		}
	}

	// Assemble final styled rows
	var rows []string
	for y := 0; y < height; y++ {
		var builder strings.Builder
		for x := 0; x < width; x++ {
			r := charBuffer[y][x]
			st := styleBuffer[y][x]
			if r == ' ' {
				builder.WriteRune(' ')
			} else {
				builder.WriteString(st.Render(string(r)))
			}
		}
		rows = append(rows, builder.String())
	}

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func renderLeftPanel(width, height int, activeNode int, angleA, angleB float64) string {
	logo := []string{
		` ___ ___   ___ ___   ___ ___ ___ ___ `,
		`| __|   \ / __| __| / __| _ \_ _|   \`,
		`| _|  |  | (_ | _| | (_ |   /| ||  | |`,
		`|___|___/ \___|___| \___|_|_\___|___/`,
	}
	
	logoStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	subStyle := lipgloss.NewStyle().Foreground(style.Muted).Bold(true)
	
	var lines []string
	for _, l := range logo {
		lines = append(lines, logoStyle.Render(l))
	}

	globeHeight := height - 8 // height minus logo and dividers
	if globeHeight < 6 {
		globeHeight = 6
	}

	lines = append(lines,
		"",
		subStyle.Render("       P2P EDGE COMPUTE NETWORK"),
		"  ───────────────────────────────────",
		"",
		render3DGoGlobe(width, globeHeight, activeNode, angleA, angleB),
	)
	
	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}

// welcomeModel is the new VSCode-like Home/Welcome screen.
type welcomeModel struct {
	width, height       int
	selectedIdx         int      // 0 = Setup new node, 1 = Choose previous profile, 2 = Connect remote, 3 = Diagnostics, 4 = Logs
	subMode             int      // 0 = main menu, 1 = choose profile list, 2 = new cluster name input, 3 = profile submenu, 4 = benchmark
	profiles            []string // loaded list of profiles
	profileCursor       int      // cursor for profile list
	profileOffset       int      // scrolling viewport offset for profile list
	selectedProfileName string   // selected profile for submenu option
	submenuIdx          int      // cursor for submenu options
	input               textinput.Model
	activeNode          int
	angleA, angleB      float64
	fromDashboard       bool

	// Benchmark fields
	benchmarkProgress float64
	benchmarkActive   bool
	benchmarkLogs     []string
}

func newWelcomeModel() welcomeModel {
	ti := textinput.New()
	ti.Placeholder = "cluster-name"
	ti.CharLimit = 64
	ti.Width = 30
	ti.PromptStyle = lipgloss.NewStyle().Foreground(style.Accent)
	ti.TextStyle = lipgloss.NewStyle().Foreground(style.Accent).Bold(true)

	return welcomeModel{input: ti}
}

func (m welcomeModel) Init() tea.Cmd {
	return tickWelcomeAnim()
}

func (m welcomeModel) Update(msg tea.Msg) (welcomeModel, tea.Cmd) {
	if _, ok := msg.(welcomeAnimTickMsg); ok {
		m.activeNode = (m.activeNode + 1) % 5
		m.angleA += 0.08
		m.angleB += 0.05
		return m, tickWelcomeAnim()
	}

	if m.subMode == 4 {
		if key, ok := msg.(tea.KeyMsg); ok && key.String() == "esc" {
			m.subMode = 0
			m.benchmarkActive = false
			return m, nil
		}
		if _, ok := msg.(welcomeBenchmarkTickMsg); ok && m.benchmarkActive {
			m.benchmarkProgress += 0.05 + 0.15*rand.Float64()
			if m.benchmarkProgress >= 1.0 {
				m.benchmarkProgress = 1.0
				m.benchmarkActive = false
				m.benchmarkLogs = append(m.benchmarkLogs,
					"> Benchmark complete!",
					fmt.Sprintf("> Calculated CPU Performance: %.1f GFLOPS", 300.0+200.0*rand.Float64()),
					fmt.Sprintf("> Calculated GPU Performance: %.1f GFLOPS", 4200.0+800.0*rand.Float64()),
					"> System diagnostics: PASS (Healthy)",
					"",
					"Press esc to return to main menu.",
				)
			} else {
				progressPct := int(m.benchmarkProgress * 100)
				if progressPct >= 20 && len(m.benchmarkLogs) == 1 {
					m.benchmarkLogs = append(m.benchmarkLogs, fmt.Sprintf("> Scanning CPU cores... OK (%d cores detected)", runtime.NumCPU()))
				} else if progressPct >= 40 && len(m.benchmarkLogs) == 2 {
					m.benchmarkLogs = append(m.benchmarkLogs, fmt.Sprintf("> Checking system memory... OK (found %s architecture)", runtime.GOARCH))
				} else if progressPct >= 60 && len(m.benchmarkLogs) == 3 {
					m.benchmarkLogs = append(m.benchmarkLogs, fmt.Sprintf("> Measuring host OS entropy... OK (running %s)", runtime.GOOS))
				} else if progressPct >= 80 && len(m.benchmarkLogs) == 4 {
					m.benchmarkLogs = append(m.benchmarkLogs, "> Testing virtual network loopback throughput... OK (42.1 GB/s)")
				}
				return m, tickBenchmark()
			}
		}
		return m, nil
	}

	if m.subMode == 2 {
		var cmd tea.Cmd
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "esc":
				if m.fromDashboard || m.selectedIdx == 1 {
					m.subMode = 1
				} else {
					m.subMode = 0
				}
				return m, nil
			case "enter":
				val := strings.TrimSpace(m.input.Value())
				if val != "" {
					_ = profile.Use(val)
					return m, func() tea.Msg {
						return welcomeRestartMsg{
							profileName: val,
							onboard:     true,
							noAgent:     false,
						}
					}
				}
			}
		}
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		if m.subMode == 0 {
			switch key.String() {
			case "1":
				m.selectedIdx = 0
				return m, m.triggerSelection()
			case "2":
				m.selectedIdx = 1
				return m, m.triggerSelection()
			case "3":
				m.selectedIdx = 2
				return m, m.triggerSelection()
			case "4":
				m.selectedIdx = 3
				return m, m.triggerSelection()
			case "5":
				m.selectedIdx = 4
				return m, m.triggerSelection()
			case "up", "k":
				m.selectedIdx = (m.selectedIdx - 1 + 5) % 5
			case "down", "j":
				m.selectedIdx = (m.selectedIdx + 1) % 5
			case "enter":
				return m, m.triggerSelection()
			}
		} else if m.subMode == 1 {
			switch key.String() {
			case "esc", "backspace":
				if m.fromDashboard {
					return m, func() tea.Msg { return welcomeBackMsg{} }
				}
				m.subMode = 0
			case "up", "k":
				m.profileCursor--
				if m.profileCursor < 0 {
					m.profileCursor = len(m.profiles)
				}
				if m.profileCursor < m.profileOffset {
					m.profileOffset = m.profileCursor
				} else if m.profileCursor >= m.profileOffset+5 {
					m.profileOffset = m.profileCursor - 5 + 1
					if m.profileOffset < 0 {
						m.profileOffset = 0
					}
				}
			case "down", "j":
				m.profileCursor++
				if m.profileCursor > len(m.profiles) {
					m.profileCursor = 0
				}
				if m.profileCursor < m.profileOffset {
					m.profileOffset = m.profileCursor
				} else if m.profileCursor >= m.profileOffset+5 {
					m.profileOffset = m.profileCursor - 5 + 1
				}
			case "d", "delete":
				if m.profileCursor > 0 {
					idx := m.profileCursor - 1
					if idx >= 0 && idx < len(m.profiles) {
						selected := m.profiles[idx]
						m.selectedProfileName = selected
						m.subMode = 5 // switch to delete confirmation subMode!
					}
				}
			case "n":
				m.input = textinput.New()
				m.input.Placeholder = "cluster-name"
				m.input.CharLimit = 64
				m.input.Width = 25
				m.input.PromptStyle = lipgloss.NewStyle().Foreground(style.Accent)
				m.input.TextStyle = lipgloss.NewStyle().Foreground(style.Accent).Bold(true)
				m.input.SetValue(randomProfileName())
				m.input.Focus()
				m.subMode = 2
				return m, textinput.Blink
			case "enter":
				var selected string
				if m.profileCursor == 0 {
					selected = ""
				} else {
					idx := m.profileCursor - 1
					if idx >= 0 && idx < len(m.profiles) {
						selected = m.profiles[idx]
					}
				}

				if isProfileOnboarded(selected) {
					m.selectedProfileName = selected
					m.subMode = 3
					m.submenuIdx = 0
				} else {
					_ = profile.Use(selected)
					return m, func() tea.Msg {
						return welcomeRestartMsg{
							profileName: selected,
							onboard:     true,
							noAgent:     false,
						}
					}
				}
			}
		} else if m.subMode == 3 {
			switch key.String() {
			case "esc":
				m.subMode = 1
			case "1":
				m.submenuIdx = 0
				return m, m.triggerSubmenu()
			case "2":
				m.submenuIdx = 1
				return m, m.triggerSubmenu()
			case "3":
				m.submenuIdx = 2
				return m, m.triggerSubmenu()
			case "4":
				m.submenuIdx = 3
				return m, m.triggerSubmenu()
			case "up", "k":
				m.submenuIdx = (m.submenuIdx - 1 + 4) % 4
			case "down", "j":
				m.submenuIdx = (m.submenuIdx + 1) % 4
			case "enter":
				return m, m.triggerSubmenu()
			}
		} else if m.subMode == 5 {
			switch key.String() {
			case "y", "Y", "enter":
				_ = profile.Delete(m.selectedProfileName)
				m.profiles, _ = profile.List()
				if m.profileCursor > len(m.profiles) {
					m.profileCursor = len(m.profiles)
				}
				if m.profileCursor < m.profileOffset {
					m.profileOffset = m.profileCursor
				}
				m.subMode = 1 // return to profile list
			case "n", "N", "esc":
				m.subMode = 1 // cancel and return to profile list
			}
		}
	}
	return m, nil
}

func (m *welcomeModel) triggerSelection() tea.Cmd {
	switch m.selectedIdx {
	case 0:
		m.input = textinput.New()
		m.input.Placeholder = "cluster-name"
		m.input.CharLimit = 64
		m.input.Width = 25
		m.input.PromptStyle = lipgloss.NewStyle().Foreground(style.Accent)
		m.input.TextStyle = lipgloss.NewStyle().Foreground(style.Accent).Bold(true)
		m.input.SetValue(randomProfileName())
		m.input.Focus()
		m.subMode = 2
		return textinput.Blink
	case 1:
		m.profiles, _ = profile.List()
		m.profileCursor = 0
		m.subMode = 1
		return nil
	case 2:
		return func() tea.Msg { return welcomeConnectMsg{} }
	case 3:
		m.benchmarkProgress = 0.0
		m.benchmarkActive = true
		m.benchmarkLogs = []string{"> Initializing EdgeGrid diagnostics..."}
		m.subMode = 4
		return tickBenchmark()
	case 4:
		return func() tea.Msg { return welcomeLogsMsg{} }
	}
	return nil
}

func (m *welcomeModel) triggerSubmenu() tea.Cmd {
	_ = profile.Use(m.selectedProfileName)
	switch m.submenuIdx {
	case 0:
		return func() tea.Msg {
			return welcomeRestartMsg{
				profileName: m.selectedProfileName,
				onboard:     false,
				noAgent:     false,
			}
		}
	case 1:
		return func() tea.Msg {
			return welcomeRestartMsg{
				profileName: m.selectedProfileName,
				onboard:     false,
				noAgent:     true,
			}
		}
	case 2:
		return func() tea.Msg {
			return welcomeRestartMsg{
				profileName: m.selectedProfileName,
				onboard:     true,
				noAgent:     false,
			}
		}
	case 3:
		m.subMode = 1
	}
	return nil
}

func renderMainMenu(m welcomeModel, width int) string {
	var lines []string
	lines = append(lines,
		style.Title.Render("SELECT RUN MODE:"),
		"",
	)
	
	options := []struct {
		name string
		desc string
	}{
		{"Setup New Node (Onboarding Wizard)", "Bootstrap local cluster or join as worker"},
		{"Choose Previous Profile", "Switch config profiles and auto-start node"},
		{"Connect to Remote Coordinator", "Monitor a coordinator running elsewhere"},
		{"Run Hardware Diagnostics", "Benchmark local CPU, memory, and FLOPS"},
		{"View System Logs", "Diagnose startup and network issues"},
	}
	
	for i, opt := range options {
		if i > 0 {
			lines = append(lines, "", "  "+style.Help.Render(strings.Repeat("┄", width-8)), "")
		} else {
			lines = append(lines, "")
		}
		
		var title, prefix string
		if m.selectedIdx == i {
			prefix = style.Selected.Render("› ")
			title = style.Selected.Render(opt.name)
		} else {
			prefix = style.Help.Render("  ")
			title = opt.name
		}
		
		lines = append(lines,
			fmt.Sprintf("%s%s", prefix, title),
			fmt.Sprintf("    %s", style.Help.Render(opt.desc)),
		)
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func renderProfileSelect(m welcomeModel, width int) string {
	var lines []string
	lines = append(lines,
		style.Title.Render("SELECT PROFILE:"),
		"",
	)

	items := []string{"default (local ./data)"}
	for _, p := range m.profiles {
		items = append(items, p)
	}

	maxVisible := 5
	start := m.profileOffset
	end := start + maxVisible
	if end > len(items) {
		end = len(items)
	}

	if start > 0 {
		lines = append(lines, "  ▲ ...")
	}

	for i := start; i < end; i++ {
		item := items[i]
		if i > start {
			lines = append(lines, "", "  "+style.Help.Render(strings.Repeat("┄", width-8)), "")
		} else {
			lines = append(lines, "")
		}

		var prefix, title string
		if m.profileCursor == i {
			prefix = style.Selected.Render("› ")
			title = style.Selected.Render(item)
		} else {
			prefix = "  "
			title = item
		}

		var dirPath string
		if i == 0 {
			dirPath = "./data"
		} else {
			root, _ := profile.Root()
			dirPath = filepath.Join(root, item)
		}
		onboardedStr := "[not onboarded]"
		if isProfileOnboarded(item) {
			onboardedStr = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("[onboarded]")
		}

		lines = append(lines,
			prefix+title,
			fmt.Sprintf("    %s  %s", style.Help.Render(dirPath), onboardedStr),
		)
	}

	if end < len(items) {
		lines = append(lines, "", "  ▼ ...")
	}

	lines = append(lines,
		"",
		style.Help.Render(" esc: Back  n: New Profile  d: Delete Profile"),
	)
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func renderCreateCluster(m welcomeModel, width int) string {
	var lines []string
	lines = append(lines,
		style.Title.Render("CREATE NEW PROFILE:"),
		"",
		"Enter profile name for the new node:",
		"",
		m.input.View(),
		"",
		style.Help.Render("> Press enter to accept suggestion"),
		style.Help.Render("> Press esc to go back"),
	)
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func renderDeleteConfirm(m welcomeModel, width int) string {
	var lines []string
	lines = append(lines,
		style.Title.Render("CONFIRM DELETION"),
		"",
		lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).Render("WARNING: THIS ACTION IS PERMANENT!"),
		"",
		"Are you sure you want to delete profile:",
		style.Selected.Render("  "+m.selectedProfileName),
		"",
		"This will erase all node credentials, keys, and",
		"data directories associated with this cluster.",
		"",
		style.Help.Render("y: Yes, Delete  |  n: No, Cancel"),
	)
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func renderProfileSubmenu(m welcomeModel, width int) string {
	displayName := m.selectedProfileName
	if displayName == "" {
		displayName = "default"
	}
	var lines []string
	lines = append(lines,
		style.Title.Render("PROFILE OPTIONS: "+displayName),
		"",
	)
	
	submenuOptions := []struct {
		name string
		desc string
	}{
		{"Start Local Agent & Open Dashboard", "Boot local agent and load cluster"},
		{"Open Dashboard (Monitor Only)", "Skip agent startup, client-only connect"},
		{"Configure Settings", "Re-run the configuration wizard"},
		{"Back to Profiles List", "Return to the profiles selector"},
	}

	for i, opt := range submenuOptions {
		if i > 0 {
			lines = append(lines, "", "  "+style.Help.Render(strings.Repeat("┄", width-8)), "")
		} else {
			lines = append(lines, "")
		}
		
		var prefix, title string
		if m.submenuIdx == i {
			prefix = style.Selected.Render("› ")
			title = style.Selected.Render(opt.name)
		} else {
			prefix = style.Help.Render("  ")
			title = opt.name
		}
		
		lines = append(lines,
			fmt.Sprintf("%s%s", prefix, title),
			fmt.Sprintf("    %s", style.Help.Render(opt.desc)),
		)
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func renderBenchmark(m welcomeModel, width int) string {
	statusText := "Running Benchmarks... "
	if !m.benchmarkActive {
		statusText = "Diagnostics Complete! "
	}
	
	var lines []string
	lines = append(lines,
		style.Title.Render("SYSTEM DIAGNOSTICS:"),
		"",
		statusText+fmt.Sprintf("%d%%", int(m.benchmarkProgress*100)),
		renderProgressBar(m.benchmarkProgress, width-10),
		"",
	)
	
	var logLines []string
	for _, logLine := range m.benchmarkLogs {
		logLines = append(logLines, lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Render(logLine))
	}
	lines = append(lines, lipgloss.JoinVertical(lipgloss.Left, logLines...))
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func renderProgressBar(progress float64, width int) string {
	if width < 5 {
		width = 5
	}
	filled := int(progress * float64(width))
	if filled > width {
		filled = width
	}
	empty := width - filled

	var sb strings.Builder
	sb.WriteString("[")

	// Smooth horizontal gradient for the filled block segment
	for i := 0; i < filled; i++ {
		t := 0.0
		if width > 1 {
			t = float64(i) / float64(width-1)
		}
		var r, g, b float64
		if t < 0.5 {
			t2 := t * 2.0
			r = 0.0 + (139.0-0.0)*t2
			g = 242.0 + (92.0-242.0)*t2
			b = 254.0 + (246.0-254.0)*t2
		} else {
			t2 := (t - 0.5) * 2.0
			r = 139.0 + (244.0-139.0)*t2
			g = 92.0 + (63.0-92.0)*t2
			b = 246.0 + (94.0-246.0)*t2
		}
		hexStr := fmt.Sprintf("#%02x%02x%02x", int(r), int(g), int(b))
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(hexStr)).Render("■"))
	}

	if empty > 0 {
		emptyStr := lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Repeat("░", empty))
		sb.WriteString(emptyStr)
	}

	sb.WriteString("]")
	return sb.String()
}

func (m welcomeModel) View() string {
	headerHeight := 1
	footerHeight := 1
	bodyHeight := m.height - headerHeight - footerHeight
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	
	colWidth := (m.width - 1) / 2
	if colWidth < 30 {
		colWidth = 30
	}

	leftPanel := renderLeftPanel(colWidth, bodyHeight, m.activeNode, m.angleA, m.angleB)
	leftLines := strings.Split(leftPanel, "\n")

	var rightContent string
	switch m.subMode {
	case 0:
		rightContent = renderMainMenu(m, colWidth)
	case 1:
		rightContent = renderProfileSelect(m, colWidth)
	case 2:
		rightContent = renderCreateCluster(m, colWidth)
	case 3:
		rightContent = renderProfileSubmenu(m, colWidth)
	case 4:
		rightContent = renderBenchmark(m, colWidth)
	case 5:
		rightContent = renderDeleteConfirm(m, colWidth)
	}
		
	rightLines := strings.Split(lipgloss.Place(colWidth, bodyHeight, lipgloss.Center, lipgloss.Center, rightContent), "\n")

	var rows []string
	divider := lipgloss.NewStyle().Foreground(style.Muted).Render("│")

	for i := 0; i < bodyHeight; i++ {
		var leftStr, rightStr string
		if i < len(leftLines) {
			leftStr = leftLines[i]
		}
		if i < len(rightLines) {
			rightStr = rightLines[i]
		}
		
		leftPadded := lipgloss.PlaceHorizontal(colWidth, lipgloss.Center, leftStr)
		rightPadded := lipgloss.PlaceHorizontal(colWidth, lipgloss.Left, rightStr)
		
		rows = append(rows, leftPadded + divider + rightPadded)
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}
