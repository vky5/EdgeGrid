package dashboard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/tui/client"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

const maxLoadBytes = 512 * 1024

// jobSubmittedMsg is emitted once SubmitJob succeeds, returning to the list.
type jobSubmittedMsg struct{}

type submitJobModel struct {
	client       client.Client
	canSubmit    bool // false for pure-worker Stub — no coordinator HTTP API
	focusIndex   int  // 0: script, 1: requirements, 2: dataset, 3: baseModel, 4: requiresGPU
	script       textarea.Model
	requirements textarea.Model
	dataset      textinput.Model
	baseModel    textinput.Model
	requiresGPU  bool
	width        int
	height       int

	// Explicit file load (ctrl+o) into the focused script/requirements pane.
	loadingPath bool
	pathInput   textinput.Model
	status      string
}

func newSubmitJobModel(c client.Client, canSubmit bool) submitJobModel {
	scriptTa := textarea.New()
	scriptTa.Placeholder = "Python training script — type or ctrl+o to load a file"
	scriptTa.ShowLineNumbers = true
	scriptTa.SetHeight(12)
	scriptTa.SetWidth(40) // after ShowLineNumbers so gutter is reserved

	reqsTa := textarea.New()
	reqsTa.Placeholder = "requirements.txt — type or ctrl+o to load a file"
	reqsTa.ShowLineNumbers = false
	reqsTa.SetHeight(4)
	reqsTa.SetWidth(30)

	dsTi := textinput.New()
	dsTi.Placeholder = "hf://wikitext or object_store://my-dataset"
	dsTi.Width = 30

	modelTi := textinput.New()
	modelTi.Placeholder = "hf://gpt2 or object_store://base-model"
	modelTi.Width = 30

	pathTi := textinput.New()
	pathTi.Placeholder = "/path/to/file.py"
	pathTi.CharLimit = 512
	pathTi.Width = 48

	m := submitJobModel{
		client:       c,
		canSubmit:    canSubmit,
		focusIndex:   0,
		script:       scriptTa,
		requirements: reqsTa,
		dataset:      dsTi,
		baseModel:    modelTi,
		pathInput:    pathTi,
	}
	if !canSubmit {
		m.status = "no coordinator API on this worker — submit is disabled (Stub client)"
	}
	return m.updateFocus()
}

func (m submitJobModel) WithSize(width, height int) submitJobModel {
	m.width, m.height = width, height
	return m.applyFieldSizes()
}

func (m submitJobModel) paneSizes() (leftW, rightW, cardH int) {
	w, h := m.width, m.height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}

	// Title row + status/path row + small gaps inside the content area.
	cardH = h - 4
	if cardH < 12 {
		cardH = 12
	}

	gap := 2
	leftW = (w - gap) * 58 / 100
	rightW = w - gap - leftW
	if leftW < 36 {
		leftW = 36
		rightW = w - gap - leftW
	}
	if rightW < 28 {
		rightW = 28
		leftW = w - gap - rightW
		if leftW < 24 {
			leftW = 24
		}
	}
	return leftW, rightW, cardH
}

// rightStackChrome is the vertical rows taken by everything in the right
// column except the requirements textarea body:
//
//	reqs label+border (3) + gap + dataset box (4) + gap + model box (4) + gap + gpu box (4)
//	= 3 + 1 + 4 + 1 + 4 + 1 + 4 = 18
//
// Short boxes are label(1)+border(2)+one content line(1) = 4.
const rightStackChrome = 18

func (m submitJobModel) applyFieldSizes() submitJobModel {
	leftW, rightW, cardH := m.paneSizes()

	// lipgloss Width is content; border (2) sits outside, padding (0,1) sits inside.
	// Usable text width for a pane of total visual width W: W - 2 (border) - 2 (pad).
	innerLeft := leftW - 4
	if innerLeft < 16 {
		innerLeft = 16
	}
	innerRight := rightW - 4
	if innerRight < 14 {
		innerRight = 14
	}

	// Fit the right stack into cardH, then grow the script so its field box
	// ends on the same row as HARDWARE (GPU) — not short above it.
	reqsH := cardH - rightStackChrome
	if reqsH < 3 {
		reqsH = 3
	}
	rightTotal := reqsH + rightStackChrome
	// Script field box = label(1)+border(2)+textarea body.
	scriptH := rightTotal - 3
	if scriptH < 5 {
		scriptH = 5
	}

	m.script.SetWidth(innerLeft)
	m.script.SetHeight(scriptH)
	m.requirements.SetWidth(innerRight)
	m.requirements.SetHeight(reqsH)
	m.dataset.Width = innerRight
	m.baseModel.Width = innerRight
	pathW := m.width - 28
	if pathW < 20 {
		pathW = 20
	}
	m.pathInput.Width = pathW
	return m
}

func (m submitJobModel) updateFocus() submitJobModel {
	m.script.Blur()
	m.requirements.Blur()
	m.dataset.Blur()
	m.baseModel.Blur()
	m.pathInput.Blur()

	if m.loadingPath {
		_ = m.pathInput.Focus()
		return m
	}

	switch m.focusIndex {
	case 0:
		_ = m.script.Focus()
	case 1:
		_ = m.requirements.Focus()
	case 2:
		_ = m.dataset.Focus()
	case 3:
		_ = m.baseModel.Focus()
	}
	return m
}

func (m submitJobModel) Init() tea.Cmd { return textarea.Blink }

// CapturesTextInput reports whether keystrokes should go into a free-form
// field (script, requirements, refs, or the ctrl+o path prompt) so the
// app-level "/" command bar and "q" quit do not steal them.
func (m submitJobModel) CapturesTextInput() bool {
	if m.loadingPath {
		return true
	}
	return m.focusIndex >= 0 && m.focusIndex <= 3
}

func (m submitJobModel) beginPathLoad() submitJobModel {
	if m.focusIndex != 0 && m.focusIndex != 1 {
		m.status = "ctrl+o loads a file into Script or Requirements only"
		return m
	}
	m.loadingPath = true
	m.status = ""
	m.pathInput.SetValue("")
	m.pathInput.Placeholder = pathPlaceholder(m.focusIndex)
	m = m.updateFocus()
	return m
}

func pathPlaceholder(focus int) string {
	if focus == 1 {
		return "/path/to/requirements.txt"
	}
	return "/path/to/training_script.py"
}

func (m submitJobModel) cancelPathLoad() submitJobModel {
	m.loadingPath = false
	m.pathInput.SetValue("")
	return m.updateFocus()
}

func (m submitJobModel) confirmPathLoad() (submitJobModel, tea.Cmd) {
	path := strings.TrimSpace(m.pathInput.Value())
	path = strings.Trim(path, `"'`)
	if path == "" {
		m.status = "enter a file path"
		return m, nil
	}

	content, base, nlines, err := readLoadFile(path)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}

	switch m.focusIndex {
	case 0:
		m.script.SetValue(content)
		m.script.CursorStart()
	case 1:
		m.requirements.SetValue(content)
		m.requirements.CursorStart()
	}

	m.loadingPath = false
	m.pathInput.SetValue("")
	m.status = fmt.Sprintf("loaded %s (%d lines)", base, nlines)
	m = m.updateFocus()
	return m, nil
}

func readLoadFile(path string) (content, base string, lines int, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", "", 0, fmt.Errorf("cannot open: %v", err)
	}
	if info.IsDir() {
		return "", "", 0, fmt.Errorf("%s is a directory", filepath.Base(path))
	}
	if info.Size() > maxLoadBytes {
		return "", "", 0, fmt.Errorf("file too large (max %dKB)", maxLoadBytes/1024)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", 0, fmt.Errorf("read failed: %v", err)
	}
	content = string(b)
	// Normalize CRLF so the textarea doesn't show odd carets.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	base = filepath.Base(path)
	lines = strings.Count(content, "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		lines++
	}
	return content, base, lines, nil
}

func (m submitJobModel) Update(msg tea.Msg) (submitJobModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.loadingPath {
			switch msg.String() {
			case "esc":
				return m.cancelPathLoad(), nil
			case "enter":
				return m.confirmPathLoad()
			}
			var cmd tea.Cmd
			m.pathInput, cmd = m.pathInput.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return backToJobsMsg{} }
		case "tab":
			m.focusIndex = (m.focusIndex + 1) % 5
			m = m.updateFocus()
			return m, nil
		case "shift+tab":
			m.focusIndex = (m.focusIndex - 1 + 5) % 5
			m = m.updateFocus()
			return m, nil
		case "ctrl+o":
			return m.beginPathLoad(), textinput.Blink
		case "space", "enter":
			if m.focusIndex == 4 {
				m.requiresGPU = !m.requiresGPU
				return m, nil
			}
		case "ctrl+s":
			if !m.canSubmit {
				m.status = "submit disabled — pure workers have no admin HTTP client (would be a no-op)"
				return m, nil
			}
			dsType, dsRef := parseScheme(m.dataset.Value())
			modelType, modelRef := parseScheme(m.baseModel.Value())

			params := client.JobParams{
				Script:       m.script.Value(),
				Requirements: m.requirements.Value(),
				DatasetType:  dsType,
				DatasetRef:   dsRef,
				ModelType:    modelType,
				ModelRef:     modelRef,
				RequiresGPU:  m.requiresGPU,
			}
			if err := m.client.SubmitJob(params); err != nil {
				m.status = "submit failed: " + err.Error()
				return m, nil
			}
			return m, func() tea.Msg { return jobSubmittedMsg{} }
		}
	}

	var cmd tea.Cmd
	switch m.focusIndex {
	case 0:
		m.script, cmd = m.script.Update(msg)
	case 1:
		m.requirements, cmd = m.requirements.Update(msg)
	case 2:
		m.dataset, cmd = m.dataset.Update(msg)
	case 3:
		m.baseModel, cmd = m.baseModel.Update(msg)
	}
	return m, cmd
}

func parseScheme(val string) (string, string) {
	val = strings.TrimSpace(val)
	if val == "" {
		return "", ""
	}
	if strings.HasPrefix(val, "hf://") {
		return "hf", strings.TrimPrefix(val, "hf://")
	}
	if strings.HasPrefix(val, "object_store://") {
		return "object_store", strings.TrimPrefix(val, "object_store://")
	}
	return "hf", val
}

// drawFieldBox renders label + bordered field. width is the desired total
// visual width of the border box (borders outside lipgloss Width).
func drawFieldBox(focused bool, label string, fieldView string, width int) string {
	if width < 12 {
		width = 12
	}
	// Content width: total visual minus left/right border.
	contentW := width - 2
	if contentW < 10 {
		contentW = 10
	}
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Muted).
		Padding(0, 1).
		Width(contentW)

	if focused {
		borderStyle = borderStyle.BorderForeground(style.Accent)
	}

	labelStyle := lipgloss.NewStyle().Bold(true)
	if focused {
		labelStyle = labelStyle.Foreground(style.Accent)
	} else {
		labelStyle = labelStyle.Foreground(lipgloss.Color("255"))
	}

	return labelStyle.Render(label) + "\n" + borderStyle.Render(fieldView)
}

func (m submitJobModel) View() string {
	// Geometry comes from WithSize; fall back so first paint before
	// WindowSizeMsg still has usable defaults.
	if m.width <= 0 || m.height <= 0 {
		m = m.WithSize(max(m.width, 80), max(m.height, 24))
	}
	leftW, rightW, _ := m.paneSizes()

	titleW := m.width
	if titleW <= 0 {
		titleW = 80
	}
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(style.Accent).
		Width(titleW)

	var banner string
	if !m.canSubmit {
		banner = style.ErrorText.Render(
			"Pure worker — no coordinator HTTP API. Submit is disabled (would no-op on Stub).",
		) + "\n\n"
	}

	scriptView := drawFieldBox(
		m.focusIndex == 0 && !m.loadingPath,
		" PYTHON SCRIPT",
		m.script.View(),
		leftW,
	)

	reqsView := drawFieldBox(
		m.focusIndex == 1 && !m.loadingPath,
		" PIP REQUIREMENTS",
		m.requirements.View(),
		rightW,
	)
	dsView := drawFieldBox(
		m.focusIndex == 2 && !m.loadingPath,
		" DATASET",
		m.dataset.View(),
		rightW,
	)
	modelView := drawFieldBox(
		m.focusIndex == 3 && !m.loadingPath,
		" BASE MODEL",
		m.baseModel.View(),
		rightW,
	)

	gpuCheckbox := "[ ] Requires GPU (CUDA)"
	if m.requiresGPU {
		gpuCheckbox = "[x] Requires GPU (CUDA)"
	}
	gpuView := drawFieldBox(
		m.focusIndex == 4 && !m.loadingPath,
		" HARDWARE",
		"  "+gpuCheckbox,
		rightW,
	)

	rightCol := lipgloss.JoinVertical(
		lipgloss.Left,
		reqsView,
		"",
		dsView,
		"",
		modelView,
		"",
		gpuView,
	)

	// If the measured right stack differs from the sizing model (terminal
	// quirks, style frame), re-sync script height so bottoms still line up.
	rightH := lipgloss.Height(rightCol)
	leftH := lipgloss.Height(scriptView)
	if rightH != leftH && rightH > 3 {
		syncH := rightH - 3
		if syncH < 5 {
			syncH = 5
		}
		m.script.SetHeight(syncH)
		scriptView = drawFieldBox(
			m.focusIndex == 0 && !m.loadingPath,
			" PYTHON SCRIPT",
			m.script.View(),
			leftW,
		)
	}

	// Bottom-align so the script box ends with HARDWARE even if a 1-row drift remains.
	grid := lipgloss.JoinHorizontal(lipgloss.Bottom, scriptView, "  ", rightCol)

	var footer string
	if m.loadingPath {
		target := "script"
		if m.focusIndex == 1 {
			target = "requirements"
		}
		footer = style.Help.Render(fmt.Sprintf("Load into %s: ", target)) + m.pathInput.View() +
			"  " + style.Help.Render("enter load   esc cancel")
	} else {
		parts := []string{"Tab navigate   ctrl+o load file   ctrl+s submit   esc cancel"}
		if m.status != "" {
			statusStyle := lipgloss.NewStyle().Foreground(style.Accent)
			if strings.HasPrefix(m.status, "cannot") ||
				strings.HasPrefix(m.status, "read") ||
				strings.HasPrefix(m.status, "file") ||
				strings.Contains(m.status, "directory") ||
				strings.HasPrefix(m.status, "enter") ||
				strings.HasPrefix(m.status, "ctrl+o") ||
				strings.HasPrefix(m.status, "submit") ||
				strings.HasPrefix(m.status, "no coordinator") {
				statusStyle = style.ErrorText
			}
			parts = []string{statusStyle.Render(m.status), style.Help.Render(parts[0])}
			footer = strings.Join(parts, "   ")
		} else {
			footer = style.Help.Render(parts[0])
		}
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		titleStyle.Render("SUBMIT TRAINING JOB"),
		"",
		banner,
		grid,
		"",
		footer,
	)
}
