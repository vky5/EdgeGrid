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
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/node"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// what the user picked on the welcome screen
// welcome runs before node.LoadConfig, so the pick still decides the data folder and needs no restart;
// only "/profile" later in the dashboard has to restart the process
type WelcomeAction int

const (
	// the user pressed q or ctrl+c; do nothing more
	WelcomeQuit WelcomeAction = iota
	// start the node and open the dashboard
	WelcomeStart
	// print the end of this profile's log instead of starting
	WelcomeLogs
)

type welcomeBenchmarkTickMsg struct{}

type welcomeAnimTickMsg struct{}

// clears the settings save message after a few seconds
// seq is the message it was set for, so an old timer doesn't wipe a newer message
type settingsStatusExpiredMsg struct{ seq int }

// how long a save message or error stays on screen, 4s
const settingsStatusTTL = 4 * time.Second

// a made-up name for a new profile, e.g. cluster-3f9a
func randomProfileName() string {
	b := make([]byte, 2)
	_, _ = crand.Read(b)
	return "cluster-" + hex.EncodeToString(b)
}

// the data folder for a profile name, e.g. "primary" gives ~/.edgegrid/primary and "" gives ./data
// it ignores the active profile, since the settings form edits the one under the cursor
func profileDataDir(name string) string {
	if name == "" {
		return "./data"
	}
	root, err := node.ProfileRoot()
	if err != nil {
		return "./data"
	}
	return filepath.Join(root, name)
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

	// colour fades row by row: cyan (0, 242, 254), purple (139, 92, 246), pink (244, 63, 94)
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

	// one character and one style per screen cell
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
	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true) // the highlighted node

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

	// dots on the globe: 11 rings of 24, 264 points
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

	// 26 dots along each link, so it reads as a solid line
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

	// globe size, shrunk to 85% to leave a margin
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

		// terminal cells are about 2.45x taller than wide, so stretch x to keep the globe round
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

// the welcome screen shown before the node starts
type welcomeModel struct {
	width, height int
	selectedIdx   int // 0 = Choose previous profile, 1 = Diagnostics, 2 = Logs
	// subMode: 0 main, 1 profiles, 2 new name, 3 profile submenu, 4 benchmark,
	// 5 delete confirm, 6 profile settings editor, 7 network role, 8 auth key
	subMode             int
	profiles            []string // loaded list of profiles
	profileCursor       int      // cursor for profile list
	profileOffset       int      // scrolling viewport offset for profile list
	selectedProfileName string   // selected profile for submenu option
	submenuIdx          int      // cursor for submenu options
	statusMsg           string   // error shown on the new-profile and submenu screens
	input               textinput.Model
	activeNode          int
	angleA, angleB      float64

	// what the user chose, read after the screen exits
	action      WelcomeAction
	profileName string
	authKey     string // join key typed on the join screen, e.g. tskey-auth-...; "" means log in through the browser

	// Network-role screen (subMode 7) and auth-key entry (subMode 8).
	roleIdx int

	// Benchmark fields
	benchmarkProgress float64
	benchmarkActive   bool
	benchmarkLogs     []string

	// Profile settings editor (subMode 6)
	settingsFields []string
	settingsIdx    int
	settingsVals   map[string]string
	settingsStatus string
	settingsDir    string
	// goes up with each new save message, so a timer can tell if its message is still showing
	settingsStatusSeq int
}

// shows the welcome screen until the user picks; must run before node.LoadConfig,
// since picking a profile sets the data folder and LoadConfig reads it only once
// AuthKey is "" unless pasted on the join screen, and must reach tsnet before it starts
func RunWelcome() (choice WelcomeChoice, err error) {
	final, err := tea.NewProgram(newWelcomeModel(), tea.WithAltScreen()).Run()
	if err != nil {
		return WelcomeChoice{}, err
	}
	w, ok := final.(welcomeModel)
	if !ok {
		return WelcomeChoice{}, nil
	}
	return w.Result(), nil
}

// what the welcome screen decided
type WelcomeChoice struct {
	Action      WelcomeAction
	ProfileName string
	AuthKey     string
}

// builds the welcome screen; it touches no node state, so it's safe before node.LoadConfig
func newWelcomeModel() welcomeModel {
	ti := textinput.New()
	ti.Placeholder = "cluster-name"
	ti.CharLimit = 64
	ti.Width = 30
	ti.PromptStyle = lipgloss.NewStyle().Foreground(style.Accent)
	ti.TextStyle = lipgloss.NewStyle().Foreground(style.Accent).Bold(true)

	return welcomeModel{input: ti}
}

// what the user chose; ProfileName is "" for the default ./data or a quit
func (m welcomeModel) Result() WelcomeChoice {
	return WelcomeChoice{Action: m.action, ProfileName: m.profileName, AuthKey: m.authKey}
}

func (m welcomeModel) Init() tea.Cmd {
	return tickWelcomeAnim()
}

func (m welcomeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	nm, cmd := m.update(msg)
	return nm, cmd
}

func (m welcomeModel) update(msg tea.Msg) (welcomeModel, tea.Cmd) {
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = wm.Width, wm.Height
		return m, nil
	}

	// quit from anywhere, except while typing, so a "q" in a name or token doesn't quit
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c":
			m.action = WelcomeQuit
			return m, tea.Quit
		case "q":
			if m.subMode != 2 && m.subMode != 6 && m.subMode != 8 {
				m.action = WelcomeQuit
				return m, tea.Quit
			}
		}
	}

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
				m.subMode = 1
				return m, nil
			case "enter":
				val := strings.TrimSpace(m.input.Value())
				if val != "" {
					// a new profile goes to the same submenu, not straight to starting,
					// so it can pick "join an existing network" and paste a key before tsnet falls back to browser login
					if err := node.UseProfile(val); err != nil {
						m.statusMsg = "could not create profile: " + err.Error()
						return m, nil
					}
					m.profileName = val
					m.selectedProfileName = val
					m.statusMsg = ""
					m.subMode = 3
					m.submenuIdx = 0
					return m, nil
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
			case "up", "k":
				m.selectedIdx = (m.selectedIdx - 1 + len(mainMenuOptions)) % len(mainMenuOptions)
			case "down", "j":
				m.selectedIdx = (m.selectedIdx + 1) % len(mainMenuOptions)
			case "enter":
				return m, m.triggerSelection()
			}
		} else if m.subMode == 1 {
			switch key.String() {
			case "esc", "backspace":
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

				m.selectedProfileName = selected
				m.statusMsg = ""
				m.subMode = 3
				m.submenuIdx = 0
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
			case "up", "k":
				m.submenuIdx = (m.submenuIdx - 1 + len(submenuOptions)) % len(submenuOptions)
			case "down", "j":
				m.submenuIdx = (m.submenuIdx + 1) % len(submenuOptions)
			case "enter":
				return m, m.triggerSubmenu()
			}
		} else if m.subMode == 5 {
			switch key.String() {
			case "y", "Y", "enter":
				if err := node.DeleteProfile(m.selectedProfileName); err != nil {
					m.statusMsg = "delete failed: " + err.Error()
				}
				m.profiles, _ = node.ListProfiles()
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
		} else if m.subMode == 6 {
			return m.updateProfileSettings(msg)
		} else if m.subMode == 7 {
			switch key.String() {
			case "esc":
				m.subMode = 3
			case "1":
				m.roleIdx = 0
				return m, m.triggerRole()
			case "2":
				m.roleIdx = 1
				return m, m.triggerRole()
			case "up", "k":
				m.roleIdx = (m.roleIdx - 1 + len(networkRoleOptions)) % len(networkRoleOptions)
			case "down", "j":
				m.roleIdx = (m.roleIdx + 1) % len(networkRoleOptions)
			case "enter":
				return m, m.triggerRole()
			}
		} else if m.subMode == 8 {
			switch key.String() {
			case "esc":
				m.subMode = 7
				return m, nil
			case "enter":
				// blank is allowed: tsnet falls back to browser login, like the first node does
				m.authKey = strings.TrimSpace(m.input.Value())
				m.action = WelcomeStart
				return m, tea.Quit
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		} else if m.subMode == 9 {
			switch key.String() {
			case "esc":
				m.subMode = 7
			case "s":
				m.openProfileSettings()
				return m, textinput.Blink
			case "enter":
				m.action = WelcomeStart
				return m, tea.Quit
			}
		}
	}

	// the code above only handles keys, so the settings screen gets its other messages here,
	// e.g. the save-message timer and the cursor blink
	if m.subMode == 6 {
		return m.updateProfileSettings(msg)
	}
	return m, nil
}

// each settings label and the file it's saved in, e.g. "Tailscale API Client ID" in ts_api_client_id
// one 0600 file per field, the way tailscaleapi.LoadCredentials reads them
var settingsFieldFiles = map[string]string{
	"Tailscale API Client ID":     "ts_api_client_id",
	"Tailscale API Client Secret": "ts_api_client_secret",
	"Tailscale API Tailnet":       "ts_api_tailnet",
	"Tailscale API Tag":           "ts_api_tag",
	"API Port":                    "api_port",
	"Require Approval":            "require_approval",
}

// the order fields show in the form
// the Tailscale API fields come first: they're the only ones read, and Client ID, Secret and Tailnet together add the Tokens tab
var settingsFieldOrder = []string{
	"Tailscale API Client ID",
	"Tailscale API Client Secret",
	"Tailscale API Tailnet",
	"Tailscale API Tag",
	"API Port",
	"Require Approval",
}

// fields that are saved but nothing reads yet, so the form marks them
// or someone would set them and wonder why nothing changed
var inertSettingsFields = map[string]bool{
	"API Port":         true,
	"Require Approval": true,
}

// fields hidden with dots unless being edited
var secretSettingsFields = map[string]bool{
	"Tailscale API Client Secret": true,
}

// whether a field is picked from a list instead of typed, e.g. "Require Approval"
func isChoiceField(name string) bool { return name == "Require Approval" }

func (m welcomeModel) updateProfileSettings(msg tea.Msg) (welcomeModel, tea.Cmd) {
	if exp, ok := msg.(settingsStatusExpiredMsg); ok {
		// only the newest timer clears the message; older ones belong to replaced messages
		if exp.seq == m.settingsStatusSeq {
			m.settingsStatus = ""
		}
		return m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		if len(m.settingsFields) > 0 && isChoiceField(m.settingsFields[m.settingsIdx]) {
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	field := ""
	if len(m.settingsFields) > 0 {
		field = m.settingsFields[m.settingsIdx]
	}
	switch key.String() {
	case "esc":
		m.subMode = 3
		m.settingsStatus = ""
		return m, nil
	case "up", "k":
		if field == "Require Approval" && !strings.EqualFold(m.settingsVals[field], "true") {
			m.settingsVals[field] = "true"
			return m, nil
		}
		return m.moveSettingsField(-1)
	case "down", "j":
		if field == "Require Approval" && strings.EqualFold(m.settingsVals[field], "true") {
			m.settingsVals[field] = "false"
			return m, nil
		}
		return m.moveSettingsField(1)
	case "left", "h":
		if field == "Require Approval" {
			m.settingsVals[field] = "false"
		}
		return m, nil
	case "right", "l":
		if field == "Require Approval" {
			m.settingsVals[field] = "true"
		}
		return m, nil
	case " ", "space":
		if field == "Require Approval" {
			m.settingsVals[field] = toggleWelcomeBool(m.settingsVals[field])
			return m, nil
		}
	case "ctrl+s":
		m = m.saveSettingsField()
		return m.finishSettings()
	case "enter":
		if !isChoiceField(field) {
			m = m.saveSettingsField()
		}
		if m.settingsIdx < len(m.settingsFields)-1 {
			m.settingsIdx++
			m = m.focusSettingsField()
			return m, textinput.Blink
		}
		return m.finishSettings()
	}
	if isChoiceField(field) {
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// saves the field being edited and moves the cursor, wrapping at both ends
// e.g. delta -1 on the first field goes to the last one
func (m welcomeModel) moveSettingsField(delta int) (welcomeModel, tea.Cmd) {
	m = m.saveSettingsField()
	n := len(m.settingsFields)
	if n == 0 {
		return m, nil
	}
	m.settingsIdx = (m.settingsIdx + delta%n + n) % n
	m = m.focusSettingsField()
	return m, textinput.Blink
}

// shows a status line that clears itself after 4s
func (m welcomeModel) setSettingsStatus(text string) (welcomeModel, tea.Cmd) {
	m.settingsStatus = text
	m.settingsStatusSeq++
	seq := m.settingsStatusSeq
	return m, tea.Tick(settingsStatusTTL, func(time.Time) tea.Msg {
		return settingsStatusExpiredMsg{seq: seq}
	})
}

// writes the form and says how it went in the status line
func (m welcomeModel) finishSettings() (welcomeModel, tea.Cmd) {
	if err := m.persistProfileSettings(); err != nil {
		return m.setSettingsStatus("save failed: " + err.Error())
	}
	return m.setSettingsStatus("saved to " + m.settingsDir)
}

func toggleWelcomeBool(s string) string {
	if strings.EqualFold(s, "true") || s == "1" {
		return "false"
	}
	return "true"
}

func (m welcomeModel) saveSettingsField() welcomeModel {
	if m.settingsVals == nil || len(m.settingsFields) == 0 {
		return m
	}
	if m.settingsIdx < 0 || m.settingsIdx >= len(m.settingsFields) {
		return m
	}
	name := m.settingsFields[m.settingsIdx]
	if isChoiceField(name) {
		return m
	}
	m.settingsVals[name] = strings.TrimSpace(m.input.Value())
	return m
}

func (m welcomeModel) focusSettingsField() welcomeModel {
	if len(m.settingsFields) == 0 {
		return m
	}
	name := m.settingsFields[m.settingsIdx]
	if isChoiceField(name) {
		m.input.Blur()
		return m
	}
	// the secret shows in full while being edited, so a pasted one can be checked; it's hidden again on leaving the field
	val := m.settingsVals[name]
	m.input.SetValue(val)
	m.input.SetCursor(len(val))
	m.input.Focus()
	return m
}

func (m *welcomeModel) openProfileSettings() {
	dir := profileDataDir(m.selectedProfileName)
	m.settingsDir = dir
	m.settingsFields = settingsFieldOrder
	m.settingsVals = make(map[string]string, len(settingsFieldOrder))
	for _, name := range settingsFieldOrder {
		m.settingsVals[name] = node.LoadToken(dir, settingsFieldFiles[name])
	}
	if m.settingsVals["Require Approval"] == "" {
		m.settingsVals["Require Approval"] = "false"
	}
	m.settingsIdx = 0
	m.settingsStatus = ""
	m.input = textinput.New()
	m.input.CharLimit = 256
	m.input.Width = 34
	m.input.Prompt = ""
	m.input.PromptStyle = lipgloss.NewStyle().Foreground(style.Accent)
	m.input.TextStyle = lipgloss.NewStyle().Foreground(style.Accent).Bold(true)
	*m = m.focusSettingsField()
	m.subMode = 6
}

// saves each field to its own file, and deletes the file when a field is cleared
// an empty secret file looks like a real but blank login, worse than no file
// callers save the focused field first; this writes only what settingsVals holds
func (m welcomeModel) persistProfileSettings() error {
	if v := strings.TrimSpace(m.settingsVals["API Port"]); v != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(v, ":"))
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("API Port must be 1-65535")
		}
	}

	for _, name := range m.settingsFields {
		file := settingsFieldFiles[name]
		v := strings.TrimSpace(m.settingsVals[name])
		if v == "" {
			if err := os.Remove(filepath.Join(m.settingsDir, file)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("%s: %w", name, err)
			}
			continue
		}
		if err := node.SaveToken(m.settingsDir, file, v); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// whether this profile has joined the tailnet before, e.g. ~/.edgegrid/primary/tailscale.ip exists
func profileHasJoined(name string) bool {
	return node.HasJoined(profileDataDir(name))
}

// whether this profile can make join keys: the same three files tailscaleapi.LoadCredentials needs
func profileHasTailscaleAPI(name string) bool {
	dir := profileDataDir(name)
	return node.LoadToken(dir, "ts_api_client_id") != "" &&
		node.LoadToken(dir, "ts_api_client_secret") != "" &&
		node.LoadToken(dir, "ts_api_tailnet") != ""
}

// the choice shown before a node's first start: new network or join one
// the only difference is the first node holds the Tailscale API login and makes join keys for the rest
var networkRoleOptions = []struct {
	name string
	desc string
}{
	{"Start a new network", "This node mints the join keys the others use. Needs Tailscale API credentials."},
	{"Join an existing network", "Paste an auth key minted by the first node."},
}

// the main menu on the welcome screen
var mainMenuOptions = []struct {
	name string
	desc string
}{
	{"Choose Profile", "Pick a data dir, then boot this node into it"},
	{"Run Hardware Diagnostics", "Benchmark local CPU, memory, and FLOPS"},
	{"View System Logs", "Tail this profile's log file"},
}

func (m *welcomeModel) triggerSelection() tea.Cmd {
	switch m.selectedIdx {
	case 0:
		m.profiles, _ = node.ListProfiles()
		m.profileCursor = 0
		m.profileOffset = 0
		m.statusMsg = ""
		m.subMode = 1
		return nil
	case 1:
		m.benchmarkProgress = 0.0
		m.benchmarkActive = true
		m.benchmarkLogs = []string{"> Initializing EdgeGrid diagnostics..."}
		m.subMode = 4
		return tickBenchmark()
	case 2:
		m.action = WelcomeLogs
		return tea.Quit
	}
	return nil
}

// what you can do with the profile under the cursor
var submenuOptions = []struct {
	name string
	desc string
}{
	{"Start Node & Open Dashboard", "Bring this node onto the tailnet and load the dashboard"},
	{"Configure Settings", "Tailscale API credentials and profile knobs"},
	{"Back to Profiles List", "Return to the profiles selector"},
}

func (m *welcomeModel) triggerSubmenu() tea.Cmd {
	switch m.submenuIdx {
	case 0:
		// "" is the default ./data, which UseProfile refuses, so set DATA_DIR instead
		// or the last active profile would start; --data-dir still wins over DATA_DIR
		if m.selectedProfileName == "" {
			if err := os.Setenv("DATA_DIR", "./data"); err != nil {
				m.statusMsg = "could not select default data dir: " + err.Error()
				return nil
			}
		} else if err := node.UseProfile(m.selectedProfileName); err != nil {
			m.statusMsg = "could not switch profile: " + err.Error()
			return nil
		}
		m.profileName = m.selectedProfileName

		// a profile that already joined skips the role screen:
		// neither choice fits it, and tsnet ignores a join key once it has joined
		if profileHasJoined(m.selectedProfileName) {
			m.action = WelcomeStart
			return tea.Quit
		}
		m.roleIdx = 0
		m.statusMsg = ""
		m.subMode = 7
		return nil
	case 1:
		m.openProfileSettings()
		return textinput.Blink
	case 2:
		m.subMode = 1
	}
	return nil
}

// acts on the new-or-join choice; "new network" without an API login only warns,
// since the login can be added later, it just can't make keys until then
func (m *welcomeModel) triggerRole() tea.Cmd {
	if m.roleIdx == 0 {
		if !profileHasTailscaleAPI(m.selectedProfileName) {
			m.subMode = 9
			return nil
		}
		m.action = WelcomeStart
		return tea.Quit
	}

	m.input = textinput.New()
	m.input.Placeholder = "tskey-auth-..."
	m.input.EchoMode = textinput.EchoPassword
	m.input.CharLimit = 256
	m.input.Width = 44
	m.input.Prompt = ""
	m.input.PromptStyle = lipgloss.NewStyle().Foreground(style.Accent)
	m.input.TextStyle = lipgloss.NewStyle().Foreground(style.Accent).Bold(true)
	m.input.Focus()
	m.subMode = 8
	return textinput.Blink
}

func renderMainMenu(m welcomeModel, width int) string {
	var lines []string
	lines = append(lines,
		style.Title.Render("SELECT RUN MODE:"),
		"",
	)

	for i, opt := range mainMenuOptions {
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

		name := item
		if i == 0 {
			name = ""
		}
		dirPath := profileDataDir(name)

		// show whether the profile has a node ID and a Tailscale API login (which adds the Tokens tab)
		var tags []string
		if node.LoadToken(dirPath, "node.id") != "" {
			tags = append(tags, lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("[node id]"))
		} else {
			tags = append(tags, style.Help.Render("[new]"))
		}
		if node.LoadToken(dirPath, "ts_api_client_id") != "" &&
			node.LoadToken(dirPath, "ts_api_client_secret") != "" &&
			node.LoadToken(dirPath, "ts_api_tailnet") != "" {
			tags = append(tags, lipgloss.NewStyle().Foreground(style.Accent).Render("[ts api]"))
		}

		lines = append(lines,
			prefix+title,
			fmt.Sprintf("    %s  %s", style.Help.Render(dirPath), strings.Join(tags, " ")),
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
	if m.statusMsg != "" {
		lines = append(lines, "", style.ErrorText.Render(m.statusMsg))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func renderProfileSettings(m welcomeModel, width int) string {
	name := m.selectedProfileName
	if name == "" {
		name = "default"
	}
	var lines []string
	lines = append(lines,
		style.Title.Render("PROFILE SETTINGS: "+name),
		style.Help.Render(m.settingsDir+"  ·  one 0600 file per field"),
		"",
		style.Help.Render("↑/↓ field   ←/→ Approval   enter next   ctrl+s save   esc back"),
		"",
	)
	for i, field := range m.settingsFields {
		focused := i == m.settingsIdx
		label := field
		if inertSettingsFields[field] {
			label += "  (not wired)"
		}
		if focused {
			lines = append(lines, style.Selected.Render("› "+label))
		} else {
			lines = append(lines, style.Help.Render("  "+label))
		}

		if field == "Require Approval" {
			on := strings.EqualFold(m.settingsVals[field], "true") || m.settingsVals[field] == "1"
			for _, o := range []struct{ val, title, desc string }{
				{"true", "true", "stored, but nothing reads it yet"},
				{"false", "false", "stored, but nothing reads it yet"},
			} {
				sel := (o.val == "true") == on
				prefix := "    "
				body := fmt.Sprintf("%-6s  %s", o.title, o.desc)
				switch {
				case sel && focused:
					prefix = "  › "
					body = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(style.Accent).Render(" " + body + " ")
				case sel:
					prefix = "  • "
					body = lipgloss.NewStyle().Bold(true).Foreground(style.Accent).Render(body)
				default:
					body = style.Help.Render(body)
				}
				lines = append(lines, prefix+body)
			}
			if focused {
				lines = append(lines, style.Help.Render("    ↑ true   ↓ false   enter next field"))
			}
			lines = append(lines, "")
			continue
		}

		switch {
		case focused:
			lines = append(lines, "  "+m.input.View(), "")
		case m.settingsVals[field] == "":
			lines = append(lines, "  "+style.Help.Render("(unset)"), "")
		case secretSettingsFields[field]:
			lines = append(lines, "  "+lipgloss.NewStyle().Bold(true).Render(maskSecret(m.settingsVals[field])), "")
		default:
			lines = append(lines, "  "+lipgloss.NewStyle().Bold(true).Render(m.settingsVals[field]), "")
		}
	}
	if m.settingsStatus != "" {
		st := lipgloss.NewStyle().Foreground(style.Accent)
		if strings.HasPrefix(m.settingsStatus, "save failed") {
			st = style.ErrorText
		}
		lines = append(lines, st.Render(m.settingsStatus))
	} else {
		lines = append(lines, style.Help.Render("Client ID + Secret + Tailnet together enable the dashboard's Tokens tab."))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// a secret as dots plus its last 4 characters, e.g. ••••••••a1b2
// enough to tell two apart, not enough to be worth reading over a shoulder
func maskSecret(s string) string {
	if len(s) <= 4 {
		return strings.Repeat("•", len(s))
	}
	return strings.Repeat("•", 8) + s[len(s)-4:]
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
	if m.statusMsg != "" {
		lines = append(lines, "", style.ErrorText.Render(m.statusMsg))
	}
	lines = append(lines, "", style.Help.Render(" esc: Back"))
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func renderNetworkRole(m welcomeModel, width int) string {
	name := m.selectedProfileName
	if name == "" {
		name = "default"
	}
	lines := []string{
		style.Title.Render("SET UP: " + name),
		style.Help.Render("this node has not joined a tailnet yet"),
	}
	for i, opt := range networkRoleOptions {
		if i > 0 {
			lines = append(lines, "", "  "+style.Help.Render(strings.Repeat("┄", max(width-8, 8))), "")
		} else {
			lines = append(lines, "")
		}
		prefix, title := style.Help.Render("  "), opt.name
		if m.roleIdx == i {
			prefix, title = style.Selected.Render("› "), style.Selected.Render(opt.name)
		}
		lines = append(lines, prefix+title, "    "+style.Help.Render(opt.desc))
	}
	lines = append(lines, "", style.Help.Render(" enter: continue   esc: back"))
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func renderAuthKeyEntry(m welcomeModel, width int) string {
	lines := []string{
		style.Title.Render("JOIN AN EXISTING NETWORK"),
		"",
		"Paste a Tailscale auth key minted by the first node:",
		"",
		"  " + m.input.View(),
		"",
		style.Help.Render("The first node mints these from its dashboard's Tokens tab (m)."),
		style.Help.Render("Leave blank to log in through the browser instead."),
		"",
		style.Help.Render(" enter: start node   esc: back"),
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func renderNeedsSettings(m welcomeModel, width int) string {
	lines := []string{
		style.Title.Render("CONFIGURE SETTINGS FIRST"),
		"",
		"Starting a new network means this node mints the join keys",
		"the other nodes use — and that needs Tailscale API credentials,",
		"which this profile does not have yet.",
		"",
		style.Help.Render("Configure Settings holds the four values; see the OAuth client"),
		style.Help.Render("in the Tailscale admin console for where they come from."),
		"",
		style.Help.Render("You can also start now and add them later — the node comes up"),
		style.Help.Render("either way, it just cannot mint keys until it has them."),
		"",
		style.Help.Render(" s: open settings   enter: start anyway   esc: back"),
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
	case 6:
		rightContent = renderProfileSettings(m, colWidth)
	case 7:
		rightContent = renderNetworkRole(m, colWidth)
	case 8:
		rightContent = renderAuthKeyEntry(m, colWidth)
	case 9:
		rightContent = renderNeedsSettings(m, colWidth)
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

		rows = append(rows, leftPadded+divider+rightPadded)
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}
