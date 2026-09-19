package dashboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"tailscale.com/client/local"

	"github.com/edgegrid/edgegrid/internal/blob"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// peersRefreshInterval is how often the Peers tab re-reads Tailscale's
// membership state.
const peersRefreshInterval = 5 * time.Second

// sendMediaType is what a file picked in the TUI is labelled as. blob
// never interprets it — it travels with the manifest so a receiver can
// decide what the bytes are for. "Some opaque file a human picked" has no
// better label than the generic one.
const sendMediaType = "application/octet-stream"

// peersMode is where the Peers tab is in the send flow. Browsing is the
// resting state; everything else is one step of "send a file to this peer".
type peersMode int

const (
	peersBrowsing peersMode = iota
	peersPickFile
	peersBuilding
	peersReady
	peersSending
)

// SendFunc transfers path to peer. node.Node.SendBlob satisfies it —
// declared here so the TUI doesn't import node.
type SendFunc func(ctx context.Context, peer discovery.Peer, path string) error

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

	mode   peersMode
	cursor int

	// target is captured by value, not as an index into peers. The 5s
	// refresh can reorder or shrink the list mid-flow, and an index would
	// then point at a different machine than the one that was picked.
	target   discovery.Peer
	input    textinput.Model
	path     string
	manifest *blob.Manifest
	flowErr  error
	send     SendFunc
	sendNote string
}

func newPeersModel(lc *local.Client, send SendFunc) peersModel {
	m := peersModel{lc: lc, send: send}
	return m.refresh()
}

func (m peersModel) WithSize(w, h int) peersModel {
	m.width, m.height = w, h
	return m
}

type peersRefreshMsg struct{}

// manifestBuiltMsg carries the result of hashing a file off the UI
// goroutine. BuildManifest reads and hashes the whole file, which is
// seconds to minutes for a large one — long enough to freeze the TUI if it
// ran inline.
type manifestBuiltMsg struct {
	path     string
	manifest *blob.Manifest
	err      error
}

// blobSentMsg carries the outcome of a transfer. The transfer runs off the
// UI goroutine for the same reason hashing does — it can take minutes.
type blobSentMsg struct {
	peer discovery.Peer
	size int64
	err  error
}

func peersRefreshCmd() tea.Cmd {
	return tea.Tick(peersRefreshInterval, func(time.Time) tea.Msg { return peersRefreshMsg{} })
}

func sendBlobCmd(send SendFunc, peer discovery.Peer, path string, size int64) tea.Cmd {
	return func() tea.Msg {
		err := send(context.Background(), peer, path)
		return blobSentMsg{peer: peer, size: size, err: err}
	}
}

func buildManifestCmd(path string) tea.Cmd {
	return func() tea.Msg {
		mf, err := blob.BuildManifest(path, sendMediaType, 0)
		return manifestBuiltMsg{path: path, manifest: mf, err: err}
	}
}

func (m peersModel) refresh() peersModel {
	if m.lc == nil {
		return m
	}
	m.peers, m.err = discovery.Snapshot(context.Background(), m.lc)
	if m.cursor >= len(m.peers) {
		m.cursor = max(len(m.peers)-1, 0)
	}
	return m
}

func (m peersModel) Init() tea.Cmd { return peersRefreshCmd() }

// capturesTextInput reports whether a keystroke belongs to the file-path
// field rather than to the dashboard's own shortcuts.
func (m peersModel) capturesTextInput() bool { return m.mode == peersPickFile }

// helpText is the footer hint for whichever step of the flow is showing.
func (m peersModel) helpText() string {
	switch m.mode {
	case peersPickFile:
		return "enter hash the file   esc cancel"
	case peersBuilding:
		return "hashing…   esc cancel"
	case peersReady:
		return "enter send   esc cancel"
	case peersSending:
		return "sending…"
	default:
		return "↑/↓ select   s send a file   Tab switch tabs   /logs   q quit"
	}
}

func (m peersModel) Update(msg tea.Msg) (peersModel, tea.Cmd) {
	switch msg := msg.(type) {
	case peersRefreshMsg:
		return m.refresh(), peersRefreshCmd()

	case blobSentMsg:
		m.mode = peersBrowsing
		m.manifest = nil
		m.path = ""
		if msg.err != nil {
			m.flowErr = msg.err
			m.sendNote = ""
			return m, nil
		}
		m.flowErr = nil
		m.sendNote = fmt.Sprintf("sent %s to %s", humanBytes(msg.size), peerLabel(msg.peer))
		return m, nil

	case manifestBuiltMsg:
		// A cancel during hashing leaves the flow; a late result must not
		// yank the tab back into the confirm step.
		if m.mode != peersBuilding || msg.path != m.path {
			return m, nil
		}
		if msg.err != nil {
			m.flowErr = msg.err
			m.mode = peersPickFile
			m.input.Focus()
			return m, textinput.Blink
		}
		m.manifest = msg.manifest
		m.mode = peersReady
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case peersPickFile:
			return m.updatePickFile(msg)
		case peersBuilding:
			if msg.Type == tea.KeyEsc {
				return m.cancelFlow(), nil
			}
			return m, nil
		case peersSending:
			return m, nil
		case peersReady:
			return m.updateReady(msg)
		default:
			return m.updateBrowsing(msg)
		}
	}
	return m, nil
}

func (m peersModel) updateBrowsing(key tea.KeyMsg) (peersModel, tea.Cmd) {
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.peers)-1 {
			m.cursor++
		}
	case "s", "enter":
		if len(m.peers) == 0 || m.cursor >= len(m.peers) {
			return m, nil
		}
		p := m.peers[m.cursor]
		if !p.Online {
			m.flowErr = fmt.Errorf("%s is offline", peerLabel(p))
			return m, nil
		}
		m.target = p
		m.flowErr = nil
		m.sendNote = ""
		m.manifest = nil
		m.path = ""
		m.mode = peersPickFile
		m.input = newPathInput()
		return m, textinput.Blink
	}
	return m, nil
}

func (m peersModel) updatePickFile(key tea.KeyMsg) (peersModel, tea.Cmd) {
	switch key.Type {
	case tea.KeyEsc:
		return m.cancelFlow(), nil
	case tea.KeyEnter:
		path := expandPath(strings.TrimSpace(m.input.Value()))
		if path == "" {
			return m, nil
		}
		m.path = path
		m.flowErr = nil
		m.mode = peersBuilding
		m.input.Blur()
		return m, buildManifestCmd(path)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	return m, cmd
}

func (m peersModel) updateReady(key tea.KeyMsg) (peersModel, tea.Cmd) {
	switch key.Type {
	case tea.KeyEsc:
		return m.cancelFlow(), nil
	case tea.KeyEnter:
		if m.send == nil || m.manifest == nil {
			m.flowErr = fmt.Errorf("no transport wired up")
			return m, nil
		}
		m.flowErr = nil
		m.mode = peersSending
		return m, sendBlobCmd(m.send, m.target, m.path, m.manifest.Size)
	}
	return m, nil
}

func (m peersModel) cancelFlow() peersModel {
	m.mode = peersBrowsing
	m.input.Blur()
	m.manifest = nil
	m.flowErr = nil
	m.path = ""
	return m
}

func newPathInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "~/path/to/file"
	ti.CharLimit = 512
	ti.Width = 40
	ti.Prompt = ""
	ti.PromptStyle = lipgloss.NewStyle().Foreground(style.Accent)
	ti.TextStyle = lipgloss.NewStyle().Foreground(style.Accent).Bold(true)
	ti.Focus()
	return ti
}

// expandPath resolves a leading ~ so a typed path behaves the way it does
// in a shell. Anything else is left alone.
//
// It also undoes what a terminal does to a dragged-in file: dropping a file
// onto a terminal pastes its path as text, and terminals disagree about
// spaces — some wrap the whole path in quotes, others backslash-escape.
// Neither form opens as-is.
func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if len(p) >= 2 {
		if (p[0] == '\'' && p[len(p)-1] == '\'') || (p[0] == '"' && p[len(p)-1] == '"') {
			p = p[1 : len(p)-1]
		}
	}
	p = strings.ReplaceAll(p, `\ `, " ")

	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

func peerLabel(p discovery.Peer) string {
	if p.Hostname != "" {
		return p.Hostname
	}
	return p.ID
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

// humanBytes is for display only — the manifest always carries exact byte
// counts.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
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

	if m.mode != peersBrowsing {
		return renderPane("SEND", m.sendView(), width, height)
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
	rows := make([]string, 0, len(m.peers)+2)
	for i, p := range m.peers {
		var statusPill string
		var seen string
		if p.Online {
			statusPill = pill(" ONLINE ", lipgloss.Color("0"), greenColor)
			seen = ""
		} else {
			statusPill = pill(" OFFLINE ", lipgloss.Color("15"), style.Muted)
			seen = label.Render("  last seen " + timeAgo(p.LastSeen))
		}

		ip := "—"
		if p.IP.IsValid() {
			ip = p.IP.String()
		}
		row := fmt.Sprintf("%s  %-20s %-16s%s", statusPill, peerLabel(p), ip, seen)
		if i == m.cursor {
			rows = append(rows, style.Selected.Render("▸ ")+row)
		} else {
			rows = append(rows, "  "+row)
		}
	}

	if m.flowErr != nil {
		rows = append(rows, "", lipgloss.NewStyle().Foreground(style.Danger).Render(m.flowErr.Error()))
	} else if m.sendNote != "" {
		rows = append(rows, "", lipgloss.NewStyle().Foreground(greenColor).Render(m.sendNote))
	}

	return renderPane("PEERS", strings.Join(rows, "\n"), width, height)
}

// sendView renders the send flow: which peer, which file, and — once it's
// hashed — what the manifest actually says. The chunk breakdown is shown
// deliberately: it's the part of the design worth being able to see.
func (m peersModel) sendView() string {
	muted := lipgloss.NewStyle().Foreground(style.Muted)
	accent := lipgloss.NewStyle().Foreground(style.Accent).Bold(true)

	lines := []string{
		muted.Render("to  ") + accent.Render(peerLabel(m.target)),
	}

	switch m.mode {
	case peersPickFile:
		lines = append(lines, muted.Render("file")+" "+m.input.View())
	case peersSending:
		lines = append(lines,
			muted.Render("file")+" "+m.path,
			"",
			muted.Render("sending… manifest first, then every chunk"),
		)
	case peersBuilding:
		lines = append(lines,
			muted.Render("file")+" "+m.path,
			"",
			muted.Render("hashing… reading the file once, hashing every chunk"),
		)
	case peersReady:
		lines = append(lines, muted.Render("file")+" "+m.path)
		if m.manifest != nil {
			lines = append(lines, "", m.manifestView(muted, accent))
		}
	}

	if m.flowErr != nil {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(style.Danger).Render(m.flowErr.Error()))
	}
	return strings.Join(lines, "\n")
}

func (m peersModel) manifestView(muted, accent lipgloss.Style) string {
	mf := m.manifest
	out := []string{
		fmt.Sprintf("%s %s  %s %d",
			muted.Render("size"), accent.Render(humanBytes(mf.Size)),
			muted.Render("chunks"), len(mf.Chunks)),
		muted.Render("sha256 ") + shortHash(mf.SHA256),
	}

	// A few chunk hashes, not all of them — enough to make the structure
	// concrete without turning the pane into a wall of hex.
	const preview = 3
	for i, c := range mf.Chunks {
		if i >= preview {
			out = append(out, muted.Render(fmt.Sprintf("  … %d more", len(mf.Chunks)-preview)))
			break
		}
		out = append(out, muted.Render(fmt.Sprintf("  [%d] @%-10d %-9s ", c.Index, c.Offset, humanBytes(int64(c.Length))))+shortHash(c.SHA256))
	}
	return strings.Join(out, "\n")
}

func shortHash(h string) string {
	if len(h) <= 16 {
		return h
	}
	return h[:16] + "…"
}
