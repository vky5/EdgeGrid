package dashboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"tailscale.com/client/local"

	"github.com/edgegrid/edgegrid/internal/blob"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// how often the Peers tab asks Tailscale for the peer list again, every 5s
const peersRefreshInterval = 5 * time.Second

// the type sent with a file picked in the TUI; blob never reads it, the receiver can
const sendMediaType = "application/octet-stream"

// which step the Peers tab is on, e.g. browsing, picking a file, sending
type peersMode int

const (
	peersBrowsing peersMode = iota
	peersPickFile
	peersBuilding
	peersReady
	peersSending
	peersPickKind
	peersDispatching
)

// sends a file to a peer, e.g. node.Node.SendBlob; declared here so the TUI doesn't import node
// m is the manifest already built for the preview, so the file isn't hashed twice
type SendFunc func(ctx context.Context, peer discovery.Peer, path string, m *blob.Manifest) error

// reads and changes which peers may send this node files, e.g. node.Node.TrustedPeers and SetTrust
// keyed by Tailscale's ID for the peer, the same as discovery.Peer.ID
type TrustFuncs struct {
	List func() map[string]bool // StableID -> allowed; peers with no entry are absent
	Set  func(stableID, hostname string, allow bool) error
}

// lets the Peers tab allow peers to send tasks, send one and wait for its result
// same keys as TrustFuncs but a separate list; without Await the tab stops at yes or no
type TaskFuncs struct {
	ListTrust func() map[string]bool
	SetTrust  func(stableID, hostname string, allow bool) error
	Dispatch  func(ctx context.Context, peer discovery.Peer, kind string) (taskID string, err error)
	Await     func(ctx context.Context, peer discovery.Peer, taskID string) (task.ResultResponse, error)
}

// one transfer happening now, either way; a copy of node.Transfer
type Transfer struct {
	Direction string  // "sending" or "receiving"
	Peer      string  // e.g. alpha5-297b when sending, the peer's node ID when receiving
	Frac      float64 // how far along, e.g. 0.5 is halfway
	BytesDone int64
	Total     int64
	Verifying bool // all bytes are in; the receiver is saving and checking the file
	Done      bool
	Err       error
	Started   time.Time // for the live speed; zero means unknown
}

// lists what this node is sending and receiving right now
// the tab asks for it on a tick, so a transfer never waits on the screen
type TransfersFunc func() []Transfer

// how often the transfer panel reads progress again, every 300ms, so it looks live
const transferPollInterval = 300 * time.Millisecond

// the Peers tab: every EdgeGrid node Tailscale knows about
// online means Tailscale sees it, not that this node has talked to it
type peersModel struct {
	lc            *local.Client
	peers         []discovery.Peer
	err           error
	width, height int

	mode   peersMode
	cursor int

	// a copy, not an index: the 5s refresh can reorder peers, and an index would send to the wrong machine
	target    discovery.Peer
	input     textinput.Model
	path      string
	manifest  *blob.Manifest
	flowErr   error // e.g. bravo: refused: this node cannot run task kind "resnet"
	send      SendFunc
	trust     TrustFuncs
	trusted   map[string]bool
	tasks     TaskFuncs
	taskTrust map[string]bool
	kind      string             // the task kind typed in, e.g. hello
	taskID    string             // set once the peer accepted; the result wait is on
	stopTask  context.CancelFunc // ends the dispatch or result wait, never the task
	transfers TransfersFunc
	active    []Transfer
	sendNote  string // the line under the list, e.g. "sent 1.5 GiB to alpha5-297b"
}

func newPeersModel(lc *local.Client, send SendFunc, transfers TransfersFunc) peersModel {
	m := peersModel{lc: lc, send: send, transfers: transfers}
	return m.refresh()
}

func (m peersModel) WithSize(w, h int) peersModel {
	m.width, m.height = w, h
	return m
}

type peersRefreshMsg struct{}

// the result of hashing a file in the background
// a large file takes minutes to hash, which would freeze the TUI
type manifestBuiltMsg struct {
	path     string
	manifest *blob.Manifest
	err      error
}

// how a transfer ended; it runs in the background since it can take minutes
type blobSentMsg struct {
	peer discovery.Peer
	size int64
	err  error
}

// the peer's yes or no on a task; asked in the background since it can take seconds
type taskDispatchedMsg struct {
	peer   discovery.Peer
	kind   string
	taskID string
	err    error
}

func dispatchTaskCmd(ctx context.Context, dispatch func(context.Context, discovery.Peer, string) (string, error), peer discovery.Peer, kind string) tea.Cmd {
	return func() tea.Msg {
		id, err := dispatch(ctx, peer, kind)
		return taskDispatchedMsg{peer: peer, kind: kind, taskID: id, err: err}
	}
}

// how an accepted task ended, or why the wait stopped
type taskResultMsg struct {
	taskID string
	resp   task.ResultResponse
	err    error
}

func awaitResultCmd(ctx context.Context, await func(context.Context, discovery.Peer, string) (task.ResultResponse, error), peer discovery.Peer, taskID string) tea.Cmd {
	return func() tea.Msg {
		resp, err := await(ctx, peer, taskID)
		return taskResultMsg{taskID: taskID, resp: resp, err: err}
	}
}

type transferTickMsg struct{}

func transferTickCmd() tea.Cmd {
	return tea.Tick(transferPollInterval, func(time.Time) tea.Msg { return transferTickMsg{} })
}

func peersRefreshCmd() tea.Cmd {
	return tea.Tick(peersRefreshInterval, func(time.Time) tea.Msg { return peersRefreshMsg{} })
}

func sendBlobCmd(send SendFunc, peer discovery.Peer, path string, m *blob.Manifest) tea.Cmd {
	return func() tea.Msg {
		err := send(context.Background(), peer, path, m)
		return blobSentMsg{peer: peer, size: m.Size, err: err}
	}
}

func buildManifestCmd(path string) tea.Cmd {
	return func() tea.Msg {
		mf, err := blob.BuildManifest(path, sendMediaType, 0)
		return manifestBuiltMsg{path: path, manifest: mf, err: err}
	}
}

func (m peersModel) refresh() peersModel {
	if m.trust.List != nil {
		m.trusted = m.trust.List()
	}
	if m.tasks.ListTrust != nil {
		m.taskTrust = m.tasks.ListTrust()
	}
	if m.lc == nil {
		return m
	}
	m.peers, m.err = discovery.Snapshot(context.Background(), m.lc)
	if m.cursor >= len(m.peers) {
		m.cursor = max(len(m.peers)-1, 0)
	}
	return m
}

func (m peersModel) Init() tea.Cmd { return tea.Batch(peersRefreshCmd(), transferTickCmd()) }

// whether keys go to a text field (file path or task kind) instead of the shortcuts
func (m peersModel) capturesTextInput() bool {
	return m.mode == peersPickFile || m.mode == peersPickKind
}

// the footer help for the step that's showing
func (m peersModel) helpText() string {
	switch m.mode {
	case peersPickFile:
		return "enter hash the file   ctrl+u clear   esc cancel"
	case peersBuilding:
		return "hashing…   esc cancel"
	case peersReady:
		return "enter send   esc cancel"
	case peersSending:
		return "sending…"
	case peersPickKind:
		return "enter offer the task   esc cancel"
	case peersDispatching:
		return "waiting for their answer…   esc stop waiting"
	default:
		return "↑/↓ select   s send file   x send task   a allow files   t allow tasks   Tab tabs   q quit"
	}
}

func (m peersModel) Update(msg tea.Msg) (peersModel, tea.Cmd) {
	switch msg := msg.(type) {
	case peersRefreshMsg:
		return m.refresh(), peersRefreshCmd()

	case transferTickMsg:
		if m.transfers != nil {
			m.active = m.transfers()
		}
		return m, transferTickCmd()

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

	case taskDispatchedMsg:
		// esc while waiting already left the flow; a late answer is dropped.
		if m.mode != peersDispatching || msg.peer.ID != m.target.ID {
			return m, nil
		}
		if msg.err != nil {
			m = m.cancelFlow()
			m.flowErr = msg.err
			m.sendNote = ""
			return m, nil
		}
		if m.tasks.Await == nil {
			m = m.cancelFlow()
			m.sendNote = fmt.Sprintf("%s accepted %q — it runs there; output is in their log", peerLabel(msg.peer), msg.kind)
			return m, nil
		}
		m.taskID = msg.taskID
		var ctx context.Context
		m, ctx = m.startWait()
		return m, awaitResultCmd(ctx, m.tasks.Await, msg.peer, msg.taskID)

	case taskResultMsg:
		// esc already left the wait; a late result is dropped.
		if m.mode != peersDispatching || msg.taskID != m.taskID {
			return m, nil
		}
		label, kind := peerLabel(m.target), m.kind
		m = m.cancelFlow()
		switch {
		case msg.err != nil:
			m.flowErr = fmt.Errorf("%s: %w — the task may still be running there", label, msg.err)
		case msg.resp.Err() != nil:
			m.flowErr = fmt.Errorf("%s: %w", label, msg.resp.Err())
		default:
			m.sendNote = fmt.Sprintf("%s finished %q: %s", label, kind, outputPreview(msg.resp.Output))
		}
		return m, nil

	case manifestBuiltMsg:
		// cancelled while hashing, so a late result mustn't pull the tab back to confirm
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
		case peersPickKind:
			return m.updatePickKind(msg)
		case peersDispatching:
			if msg.Type == tea.KeyEsc {
				return m.cancelFlow(), nil
			}
			return m, nil
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
	case "a":
		if len(m.peers) == 0 || m.cursor >= len(m.peers) {
			return m, nil
		}
		if m.trust.Set == nil {
			m.flowErr = fmt.Errorf("trust isn't wired up")
			return m, nil
		}
		p := m.peers[m.cursor]
		// No entry or blocked becomes allowed; allowed becomes blocked.
		allow := !m.trusted[p.ID]
		if err := m.trust.Set(p.ID, peerLabel(p), allow); err != nil {
			m.flowErr = err
			return m, nil
		}
		m.flowErr = nil
		if m.trust.List != nil {
			m.trusted = m.trust.List()
		}
		if allow {
			m.sendNote = peerLabel(p) + " may now send you files"
		} else {
			m.sendNote = peerLabel(p) + " is blocked from sending you files"
		}
	case "t":
		if len(m.peers) == 0 || m.cursor >= len(m.peers) {
			return m, nil
		}
		if m.tasks.SetTrust == nil {
			m.flowErr = fmt.Errorf("task trust isn't wired up")
			return m, nil
		}
		p := m.peers[m.cursor]
		allow := !m.taskTrust[p.ID]
		if err := m.tasks.SetTrust(p.ID, peerLabel(p), allow); err != nil {
			m.flowErr = err
			return m, nil
		}
		m.flowErr = nil
		if m.tasks.ListTrust != nil {
			m.taskTrust = m.tasks.ListTrust()
		}
		if allow {
			m.sendNote = peerLabel(p) + " may now run tasks on this node"
		} else {
			m.sendNote = peerLabel(p) + " is blocked from running tasks on this node"
		}
	case "x":
		if len(m.peers) == 0 || m.cursor >= len(m.peers) {
			return m, nil
		}
		p := m.peers[m.cursor]
		if !p.Online {
			m.flowErr = fmt.Errorf("%s is offline", peerLabel(p))
			return m, nil
		}
		if m.tasks.Dispatch == nil {
			m.flowErr = fmt.Errorf("task dispatch isn't wired up")
			return m, nil
		}
		m.target = p
		m.flowErr = nil
		m.sendNote = ""
		m.kind = ""
		m.mode = peersPickKind
		m.input = newKindInput()
		return m, textinput.Blink
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
	case tea.KeyCtrlU:
		// a second dropped file is glued onto the first path, so this clears it
		m.input.SetValue("")
		m.flowErr = nil
		return m, nil
	case tea.KeyEnter:
		path := expandPath(strings.TrimSpace(m.input.Value()))
		if path == "" {
			return m, nil
		}
		if err := checkSinglePath(path); err != nil {
			m.flowErr = err
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
		return m, sendBlobCmd(m.send, m.target, m.path, m.manifest)
	}
	return m, nil
}

func (m peersModel) updatePickKind(key tea.KeyMsg) (peersModel, tea.Cmd) {
	switch key.Type {
	case tea.KeyEsc:
		return m.cancelFlow(), nil
	case tea.KeyEnter:
		kind := strings.TrimSpace(m.input.Value())
		if kind == "" {
			return m, nil
		}
		m.kind = kind
		m.flowErr = nil
		m.mode = peersDispatching
		m.input.Blur()
		m, ctx := m.startWait()
		return m, dispatchTaskCmd(ctx, m.tasks.Dispatch, m.target, kind)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	return m, cmd
}

func (m peersModel) cancelFlow() peersModel {
	if m.stopTask != nil {
		m.stopTask()
		m.stopTask = nil
	}
	m.mode = peersBrowsing
	m.input.Blur()
	m.manifest = nil
	m.flowErr = nil
	m.path = ""
	m.kind = ""
	m.taskID = ""
	return m
}

// gives the next task wait a context that esc can cancel, ending the previous one
func (m peersModel) startWait() (peersModel, context.Context) {
	if m.stopTask != nil {
		m.stopTask()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.stopTask = cancel
	return m, ctx
}

// a task's output cut to one short line for the status note
// e.g. "hello\nworld\n" gives "hello world"; past 200 characters it ends in …
func outputPreview(out []byte) string {
	const limit = 200
	s := []rune(strings.Join(strings.Fields(string(out)), " "))
	if len(s) == 0 {
		return "(no output)"
	}
	if len(s) > limit {
		return string(s[:limit]) + "…"
	}
	return string(s)
}

func newKindInput() textinput.Model {
	ti := newPathInput()
	ti.Placeholder = "task kind, e.g. hello"
	ti.CharLimit = 64
	return ti
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

// expands a leading ~ and undoes the quotes or backslashes a terminal adds to a dropped file
// e.g. '/home/me/my file.mp4' or /home/me/my\ file.mp4 gives /home/me/my file.mp4
func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if len(p) >= 2 {
		if (p[0] == '\'' && p[len(p)-1] == '\'') || (p[0] == '"' && p[len(p)-1] == '"') {
			p = p[1 : len(p)-1]
		}
	}
	// not on Windows: backslash is its path separator, so unescaping would break real paths
	if runtime.GOOS != "windows" {
		p = strings.ReplaceAll(p, `\ `, " ")
	}

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

// a byte count for the screen, e.g. 1.5 GiB; the manifest keeps exact counts
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

	switch m.mode {
	case peersBrowsing:
	case peersPickKind, peersDispatching:
		return renderPane("TASK", m.taskView(), width, height)
	default:
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
		row := fmt.Sprintf("%s f%s t%s %-20s %-16s %s%s", statusPill, m.trustMarker(p.ID), m.taskTrustMarker(p.ID), peerLabel(p), ip, routeCell(p), seen)
		if i == m.cursor {
			rows = append(rows, style.Selected.Render("▸ ")+row)
		} else {
			rows = append(rows, "  "+row)
		}
	}

	if tp := m.transfersView(); tp != "" {
		rows = append(rows, "", tp)
	}

	if m.flowErr != nil {
		rows = append(rows, "", lipgloss.NewStyle().Foreground(style.Danger).Render(m.flowErr.Error()))
	} else if m.sendNote != "" {
		rows = append(rows, "", lipgloss.NewStyle().Foreground(greenColor).Render(m.sendNote))
	}

	return renderPane("PEERS", strings.Join(rows, "\n"), width, height)
}

// the send screen: which peer, which file, and once hashed, what the manifest says
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
		if tp := m.transfersView(); tp != "" {
			lines = append(lines, "", tp)
		}
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

// the task screen: which peer, which kind, and whether we're still waiting for yes or no
func (m peersModel) taskView() string {
	muted := lipgloss.NewStyle().Foreground(style.Muted)
	accent := lipgloss.NewStyle().Foreground(style.Accent).Bold(true)

	lines := []string{muted.Render("to  ") + accent.Render(peerLabel(m.target))}
	switch m.mode {
	case peersPickKind:
		lines = append(lines, muted.Render("kind")+" "+m.input.View())
	case peersDispatching:
		status := "offered… waiting for them to accept or refuse"
		if m.taskID != "" {
			status = "accepted… running there, waiting for the result"
		}
		lines = append(lines, muted.Render("kind")+" "+m.kind, "", muted.Render(status))
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

	// show a few chunk hashes, not a wall of them
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

// a fixed-width progress bar where full is good, unlike Overview's load meter
func transferBar(frac float64, width int) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac * float64(width))
	if filled > width {
		filled = width
	}
	return lipgloss.NewStyle().Foreground(greenColor).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(style.Muted).Render(strings.Repeat("░", width-filled))
}

// every transfer this node is running, both ways
// incoming ones were started by the peer, so this is the only place they show
func (m peersModel) transfersView() string {
	if len(m.active) == 0 {
		return ""
	}

	muted := lipgloss.NewStyle().Foreground(style.Muted)
	barWidth := 24
	if m.width > 0 && m.width < 70 {
		barWidth = 12
	}

	rows := []string{muted.Render("transfers")}
	for _, t := range m.active {
		arrow := "→"
		if t.Direction == "receiving" {
			arrow = "←"
		}

		var status string
		switch {
		case t.Err != nil:
			status = lipgloss.NewStyle().Foreground(style.Danger).Render("failed: " + t.Err.Error())
		case t.Done:
			status = lipgloss.NewStyle().Foreground(greenColor).Render("done  " + humanBytes(t.Total))
		case t.Verifying:
			// all bytes are in but the file is still being checked,
			// so without this the bar sits at 100% and looks stuck
			status = lipgloss.NewStyle().Foreground(style.Accent).Render(
				"verifying…  " + humanBytes(t.Total) + " received, checking the whole file")
		default:
			status = fmt.Sprintf("%s  %3.0f%%  %s / %s%s",
				transferBar(t.Frac, barWidth), t.Frac*100,
				humanBytes(t.BytesDone), humanBytes(t.Total),
				speedSuffix(t, time.Now()))
		}

		rows = append(rows, fmt.Sprintf("  %s %-16s %s", arrow, truncate(t.Peer, 16), status))
	}
	return strings.Join(rows, "\n")
}

// average speed since the start plus time left, e.g. "  12.0 MiB/s  ETA 30s"
// empty in the first second, when the speed jumps around too much
func speedSuffix(t Transfer, now time.Time) string {
	if t.Started.IsZero() || t.BytesDone <= 0 {
		return ""
	}
	elapsed := now.Sub(t.Started)
	if elapsed < time.Second {
		return ""
	}
	rate := float64(t.BytesDone) / elapsed.Seconds()
	out := "  " + humanBytes(int64(rate)) + "/s"
	if remaining := t.Total - t.BytesDone; remaining > 0 && rate > 0 {
		eta := time.Duration(float64(remaining) / rate * float64(time.Second))
		out += "  ETA " + eta.Round(time.Second).String()
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// refuses two dropped files glued into one path
// or os.Open fails later with a confusing error about a path nobody typed
func checkSinglePath(p string) error {
	multi := fmt.Errorf("looks like more than one file was dropped — ctrl+u to clear, then drop one")

	sep := string(os.PathSeparator)

	// the home folder twice means a second path was glued on;
	// a real path can hold it once, e.g. /mnt/backup/home/user/..., never twice
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if strings.Count(p, home+sep) > 1 {
			return multi
		}
	}

	// Space-separated drops: "/a/b.mp4 /c/d.mkv", or on Windows
	// "C:\a.mp4 C:\b.mkv".
	if strings.Contains(strings.TrimPrefix(p, sep), " "+sep) {
		return multi
	}
	if runtime.GOOS == "windows" && strings.Count(p, ":"+sep) > 1 {
		return multi
	}
	return nil
}

// whether a peer may send this node files: ✓ allowed, ✗ blocked, or · no choice yet (the default applies)
func (m peersModel) trustMarker(stableID string) string {
	return decisionMarker(m.trusted, stableID)
}

// the same marker, for who may send tasks
func (m peersModel) taskTrustMarker(stableID string) string {
	return decisionMarker(m.taskTrust, stableID)
}

func decisionMarker(decisions map[string]bool, stableID string) string {
	allowed, decided := decisions[stableID]
	switch {
	case !decided:
		return lipgloss.NewStyle().Foreground(style.Muted).Render("·")
	case allowed:
		return lipgloss.NewStyle().Foreground(greenColor).Render("✓")
	default:
		return lipgloss.NewStyle().Foreground(style.Danger).Render("✗")
	}
}

// how traffic reaches a peer, e.g. direct or relay, padded so later columns line up
// relay is much slower than direct, so it's shown as a warning
func routeCell(p discovery.Peer) string {
	const width = 10
	r := p.Route()
	pad := strings.Repeat(" ", max(width-len(r), 0))
	switch r {
	case "direct":
		return lipgloss.NewStyle().Foreground(greenColor).Render(r) + pad
	case "relay", "peer relay":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render(r) + pad
	default:
		return lipgloss.NewStyle().Foreground(style.Muted).Render("—") + strings.Repeat(" ", width-1)
	}
}
