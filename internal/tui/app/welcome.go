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

// WelcomeAction is what the user picked before the welcome screen exited.
// Welcome runs as its own bubbletea program *before* node.LoadConfig, so the
// choice it returns still gets to decide which data dir the node comes up on
// — that's why there's no "restart" action here. The old welcome lived inside
// App, after tsnet was already bound to a data dir, so every profile switch
// had to exec-restart the process (see execRestart in main.go). Choosing
// first removes that round trip for the initial pick; execRestart still
// exists for the "/profile" cmdbar switch mid-session, which genuinely can't
// avoid it.
type WelcomeAction int

const (
	// WelcomeQuit means the user pressed q/ctrl+c — do nothing further.
	WelcomeQuit WelcomeAction = iota
	// WelcomeStart means boot the node and hand over to the dashboard.
	WelcomeStart
	// WelcomeLogs means print this profile's log tail instead of booting.
	WelcomeLogs
)

type welcomeBenchmarkTickMsg struct{}

type welcomeAnimTickMsg struct{}

// settingsStatusExpiredMsg clears the settings save/error line after a few
// seconds. It carries the seq of the message it was scheduled for, so a save
// made while an earlier tick is still in flight doesn't get wiped early by it
// — and pressing ctrl+s twice re-shows the line instead of leaving a stale one
// on screen with no way to tell the second save happened.
type settingsStatusExpiredMsg struct{ seq int }

// settingsStatusTTL is how long a save confirmation or error stays up.
const settingsStatusTTL = 4 * time.Second

func randomProfileName() string {
	b := make([]byte, 2)
	_, _ = crand.Read(b)
	return "cluster-" + hex.EncodeToString(b)
}

// profileDataDir resolves a profile name to its data dir without consulting
// the *active* profile — the settings form edits whichever profile the cursor
// is on, which is usually not the active one. "" is the default ./data dir,
// matching node.ResolveDataDir's last fallback.
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

// welcomeModel is the pre-boot Home/Welcome screen.
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
	statusMsg           string   // error surfaced on the create-profile / submenu screens
	input               textinput.Model
	activeNode          int
	angleA, angleB      float64

	// What the user chose, read by Welcome's caller after the program exits.
	action      WelcomeAction
	profileName string
	authKey     string // tsnet auth key typed on the join screen; "" means interactive login

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
	// settingsStatusSeq rises on every new status line so a pending expiry
	// tick can tell whether it still refers to the message on screen.
	settingsStatusSeq int
}

// RunWelcome shows the landing page and blocks until the user picks
// something. It must run before node.LoadConfig: choosing a profile here sets
// the active profile (or DATA_DIR for the default entry), and LoadConfig
// resolves the data dir exactly once per process.
//
// profileName is "" for the default ./data entry or when the user quit.
// authKey is "" unless the user pasted one on the join screen, in which case
// it must reach tsnet before Up — see node.Config.TailscaleAuthKey.
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

// WelcomeChoice is what the landing page decided.
type WelcomeChoice struct {
	Action      WelcomeAction
	ProfileName string
	AuthKey     string
}

// newWelcomeModel builds the welcome screen. It touches no node state, so it
// is safe to construct before node.LoadConfig.
func newWelcomeModel() welcomeModel {
	ti := textinput.New()
	ti.Placeholder = "cluster-name"
	ti.CharLimit = 64
	ti.Width = 30
	ti.PromptStyle = lipgloss.NewStyle().Foreground(style.Accent)
	ti.TextStyle = lipgloss.NewStyle().Foreground(style.Accent).Bold(true)

	return welcomeModel{input: ti}
}

// Result reports what the user chose. ProfileName is "" when they picked the
// default ./data entry or quit without choosing.
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

	// Global quit. Guarded on the text-entry submodes so typing "q" into a
	// profile name or an API token doesn't kill the program mid-word.
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
					// UseProfile creates the dir, so a brand-new name is
					// usable immediately — there's no onboarding wizard left
					// to run between naming it and booting into it.
					if err := node.UseProfile(val); err != nil {
						m.statusMsg = "could not create profile: " + err.Error()
						return m, nil
					}
					m.profileName = val
					m.action = WelcomeStart
					return m, tea.Quit
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

				// Always the submenu now. The old code branched here on
				// whether the profile had been through the onboarding wizard,
				// sending un-onboarded ones straight into it — that wizard
				// (internal/tui/onboarding) was deleted in 22acea4, so there
				// is no second path left to take.
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
				// Blank is allowed and meaningful: tsnet falls back to
				// interactive login, which is the same path the first node
				// takes. Better than refusing to proceed when someone has a
				// browser open and no key to hand.
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

	// The dispatch above only runs for key presses, so the settings screen
	// needs its non-key messages routed here: the status-expiry tick, and the
	// blink that makes its text cursor visible at all.
	if m.subMode == 6 {
		return m.updateProfileSettings(msg)
	}
	return m, nil
}

// settingsFieldFiles maps each settings-form label to the file it persists
// to inside the profile's data dir. Every field gets its own 0600 file —
// the convention tailscaleapi.LoadCredentials already reads. There is
// deliberately no settings.json: the old ProfileSettings struct carried an
// Apply() that wrote cfg.NATSPort / cfg.Server.Port / cfg.Client.Executor,
// none of which exist on node.Config any more, and a schema that can drift
// out of sync with the struct it configures is worse than no schema.
var settingsFieldFiles = map[string]string{
	"Tailscale API Client ID":     "ts_api_client_id",
	"Tailscale API Client Secret": "ts_api_client_secret",
	"Tailscale API Tailnet":       "ts_api_tailnet",
	"Tailscale API Tag":           "ts_api_tag",
	"API Port":                    "api_port",
	"Require Approval":            "require_approval",
}

// settingsFieldOrder is the form's display order. The ts_api_* fields lead
// because they are the only ones with a live consumer: setting Client ID,
// Client Secret and Tailnet together is what makes
// tailscaleapi.LoadCredentials return non-nil, which is what puts the Tokens
// tab in the dashboard. Nothing else writes those files, so before this form
// existed the only way to enable Tokens was to create them by hand.
var settingsFieldOrder = []string{
	"Tailscale API Client ID",
	"Tailscale API Client Secret",
	"Tailscale API Tailnet",
	"Tailscale API Tag",
	"API Port",
	"Require Approval",
}

// inertSettingsFields save and reload correctly but are read by nothing: the
// coordinator HTTP server "API Port" configured and the join-approval flow
// behind "Require Approval" were both deleted in 22acea4. They are marked as
// such in the UI rather than rendered like working knobs — a form that
// silently accepts a setting nothing honours produces a bug report months
// later with no failing code path to find.
var inertSettingsFields = map[string]bool{
	"API Port":         true,
	"Require Approval": true,
}

// secretSettingsFields are masked when not being edited.
var secretSettingsFields = map[string]bool{
	"Tailscale API Client Secret": true,
}

// isChoiceField reports whether a field is picked from a list rather than
// typed, so key handling routes to the selector instead of the text input.
func isChoiceField(name string) bool { return name == "Require Approval" }

func (m welcomeModel) updateProfileSettings(msg tea.Msg) (welcomeModel, tea.Cmd) {
	if exp, ok := msg.(settingsStatusExpiredMsg); ok {
		// Only the newest scheduled tick may clear the line; older ones belong
		// to messages that have already been replaced.
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

// moveSettingsField commits the field being edited and moves the cursor by
// delta, wrapping at both ends.
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

// setSettingsStatus shows a status line and schedules it to clear itself.
func (m welcomeModel) setSettingsStatus(text string) (welcomeModel, tea.Cmd) {
	m.settingsStatus = text
	m.settingsStatusSeq++
	seq := m.settingsStatusSeq
	return m, tea.Tick(settingsStatusTTL, func(time.Time) tea.Msg {
		return settingsStatusExpiredMsg{seq: seq}
	})
}

// finishSettings writes the form and reports the outcome in the status line.
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
	// The secret is echoed normally while it has focus — you cannot verify a
	// pasted OAuth secret you cannot see, and it is masked again the moment
	// the cursor leaves the field.
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

// persistProfileSettings writes every field to its own file under the
// profile dir. Clearing a field removes its file rather than leaving an empty
// one: LoadToken treats missing and empty the same, but a zero-byte
// ts_api_client_secret on disk reads as a live credential that happens to be
// blank, which is a worse thing to find in a data dir than nothing at all.
// Callers commit the focused field with saveSettingsField first; this
// deliberately does not, so it writes exactly what settingsVals holds rather
// than silently folding in whatever the text input happens to contain.
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

// profileHasJoined reports whether this profile has ever completed a tailnet
// bring-up. tailscale.ip is written only after ts.Up returns a valid address
// (see node.New), which makes it a truthful "this node is already a member"
// marker — unlike tsnet/tailscaled.state, which exists from the first attempt
// whether or not authentication ever succeeded.
func profileHasJoined(name string) bool {
	return node.LoadToken(profileDataDir(name), "tailscale.ip") != ""
}

// profileHasTailscaleAPI reports whether this profile can mint join keys —
// the same three files tailscaleapi.LoadCredentials requires.
func profileHasTailscaleAPI(name string) bool {
	dir := profileDataDir(name)
	return node.LoadToken(dir, "ts_api_client_id") != "" &&
		node.LoadToken(dir, "ts_api_client_secret") != "" &&
		node.LoadToken(dir, "ts_api_tailnet") != ""
}

// networkRoleOptions is the fork shown before a node's first bring-up. The two
// differ in one real, already-implemented way: the first node holds the
// Tailscale API credentials and mints join keys for everyone else, which is
// exactly what puts the Tokens tab on its dashboard. Deliberately not called
// "cluster" or "coordinator" — both name subsystems this branch deleted, and
// would read as a control-plane role that no longer exists.
var networkRoleOptions = []struct {
	name string
	desc string
}{
	{"Start a new network", "This node mints the join keys the others use. Needs Tailscale API credentials."},
	{"Join an existing network", "Paste an auth key minted by the first node."},
}

// mainMenuOptions is the landing menu. "Setup New Node (Onboarding Wizard)"
// and "Connect to Remote Coordinator" used to sit at the top of this list;
// both are gone because the packages behind them (internal/tui/onboarding,
// internal/tui/app/connect.go) were deleted in 22acea4 along with the
// coordinator they talked to. What survives is everything that only needs a
// local data dir.
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

// submenuOptions is what you can do with the profile under the cursor.
// "Open Dashboard (Monitor Only)" is absent for the same reason as the two
// missing main-menu entries: it connected a read-only client to a remote
// coordinator over HTTP, and there is no coordinator to connect to.
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
		// The empty name is the default ./data entry, which UseProfile
		// rejects outright ("profile name required"). Rather than silently
		// leaving whatever profile was already active — which is what the old
		// code did, so picking "default" quietly booted the wrong data dir —
		// say so through DATA_DIR, which node.ResolveDataDir honours ahead of
		// the active profile but still behind an explicit --data-dir flag.
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

		// A profile that already reached the tailnet skips the role screen
		// entirely: neither "new network" nor "join" describes a node that is
		// already a member, and tsnet ignores an auth key once its state store
		// holds a node anyway (see tsnet.Server.AuthKey's doc).
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

// triggerRole acts on the network-role choice. "New network" only warns when
// the API credentials are missing — it does not block, because a node can
// perfectly well come up first and be given credentials afterwards; it just
// cannot mint keys for anyone until it has them.
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

		// Replaces the old "[onboarded]" marker, which keyed off admin.token
		// / node.token — coordinator credentials that nothing issues any more,
		// so every profile would read as not-onboarded forever. These two say
		// something still true: whether the dir has a node identity, and
		// whether Configure Settings has been filled in far enough for the
		// dashboard's Tokens tab to appear.
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

// maskSecret shows only the tail of a stored credential — enough to tell two
// secrets apart when checking which one a profile holds, not enough to be
// worth shoulder-surfing.
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
		
		rows = append(rows, leftPadded + divider + rightPadded)
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}
