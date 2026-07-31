package dashboard

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/tui/client"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

type tokensModel struct {
	client     client.Client
	table      table.Model
	tokens     []client.TokenSummary
	err        error
	justMinted client.MintedToken // shown once, right after "m" — never re-fetched from the server
	copied     bool

	// height is the full body budget handed down from Dashboard.resizeTables
	// (same h every other tab's table gets). Unlike every other tab, this
	// one prepends extra content above the table (the empty-state prompt or
	// the just-minted panel) — applyHeight() is what keeps the table's own
	// SetHeight in sync with that, so the tab's total rendered height never
	// exceeds the budget and overflows the frame.
	height int
}

func newTokensModel(c client.Client) tokensModel {
	m := tokensModel{
		client: c,
		height: 10,
		table: table.New(
			table.WithColumns([]table.Column{
				{Title: "STATUS", Width: 10},
				{Title: "NODE", Width: 16},
				{Title: "IP", Width: 14},
				{Title: "CREATED", Width: 12},
			}),
			table.WithFocused(true),
		),
	}
	return m.refresh()
}

// WithHeight sets the body-height budget (mirrors WithSize on other
// screens) and immediately re-applies it to the inner table.
func (m tokensModel) WithHeight(h int) tokensModel {
	m.height = h
	return m.applyHeight()
}

// applyHeight shrinks the table's own height by whatever extra content
// View() will render above it, so the two always sum to m.height instead of
// silently exceeding it.
func (m tokensModel) applyHeight() tokensModel {
	extra := lipgloss.Height(m.extraContent())
	m.table.SetHeight(max(m.height-extra, 3))
	return m
}

// extraContent is whatever View() renders above the table — factored out so
// applyHeight can measure the exact same block instead of a hand-counted
// guess at its line count.
func (m tokensModel) extraContent() string {
	if m.justMinted.Key != "" {
		copyHint := "press c to copy"
		if m.copied {
			copyHint = "copied!"
		}
		panel := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(style.Accent).
			Padding(0, 1).
			Render(
				style.Title.Render("NEW TOKEN — shown once, save it now") + "\n\n" +
					lipgloss.NewStyle().Foreground(style.Accent).Bold(true).Render(m.justMinted.Key) + "\n\n" +
					style.Help.Render("share this with whoever's device should join   ("+copyHint+")"),
			)
		return panel + "\n\n"
	}
	if len(m.tokens) == 0 {
		return style.Title.Render("No tokens minted yet") + "\n\n" +
			style.Help.Render("Press m to mint a new Tailscale auth key and share it with whoever's device should join.") + "\n\n"
	}
	return ""
}

func (m tokensModel) refresh() tokensModel {
	toks, err := m.client.ListTokens()
	m.err = err
	m.tokens = toks
	rows := make([]table.Row, 0, len(toks))
	for _, t := range toks {
		status := "unused"
		switch {
		case t.Revoked:
			status = "revoked"
		case t.Activated:
			status = "used"
		}
		node := t.Hostname
		if node == "" {
			node = t.NodeID
		}
		rows = append(rows, table.Row{status, node, t.NodeIP, t.CreatedAt})
	}
	m.table.SetRows(rows)
	m.table.SetCursor(0)
	return m.applyHeight()
}

// copyToClipboard writes an OSC 52 escape sequence directly to the
// terminal — works over SSH/tmux, unlike an OS clipboard library, since
// the terminal emulator (not the remote process) owns the clipboard.
// Best-effort: unsupported terminals just ignore the sequence.
func copyToClipboard(s string) tea.Cmd {
	return func() tea.Msg {
		encoded := base64.StdEncoding.EncodeToString([]byte(s))
		fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\x07", encoded)
		return tokenCopiedMsg{}
	}
}

type tokenCopiedMsg struct{}

func (m tokensModel) Init() tea.Cmd { return nil }

func (m tokensModel) Update(msg tea.Msg) (tokensModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tokenCopiedMsg:
		m.copied = true
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "m":
			minted, err := m.client.MintToken()
			if err != nil {
				m.err = err
				return m, nil
			}
			m.err = nil
			m.justMinted = minted
			m.copied = false
			return m.refresh(), nil
		case "c":
			if m.justMinted.Key != "" {
				return m, copyToClipboard(m.justMinted.Key)
			}
		case "r":
			if len(m.tokens) > 0 {
				row := m.table.Cursor()
				if row < len(m.tokens) && !m.tokens[row].Revoked {
					if err := m.client.RevokeToken(m.tokens[row].ID); err != nil {
						m.err = err
						return m, nil
					}
					return m.refresh(), nil
				}
			}
		}
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m tokensModel) View() string {
	body := m.extraContent() + m.table.View()
	if m.err != nil {
		body += "\n\n" + style.ErrorText.Render(m.err.Error())
	}
	return body
}
