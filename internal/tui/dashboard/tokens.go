package dashboard

import (
	"encoding/base64"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/tailscaleapi"
	"github.com/edgegrid/edgegrid/internal/tui/style"
)

// tokensModel mints Tailscale auth keys directly through tailscaleapi.Client.
// Mint-only, on purpose: the old version listed every previously-minted key
// with a used/revoked status pulled from a coordinator's admin store — that
// store doesn't exist anymore, and tailscaleapi has no ListKeys, so there's
// nothing to back a history table with. justMinted is the entire state: the
// key from the most recent "m", shown once, revocable while it's still on
// screen, then gone — nothing here is persisted to disk.
type tokensModel struct {
	client        *tailscaleapi.Client
	width, height int

	justMinted *tailscaleapi.MintedKey
	revoked    bool
	copied     bool
	// hidden blanks the secret from the screen while keeping justMinted set,
	// so "r" can still revoke the key it belongs to. Dropping justMinted
	// outright would clear the display but also strand the key: its ID would
	// be gone from memory and revoking would mean going to the Tailscale
	// console instead.
	hidden bool
	err    error
}

func newTokensModel(c *tailscaleapi.Client) tokensModel {
	return tokensModel{client: c, width: 80, height: 10}
}

func (m tokensModel) WithSize(w, h int) tokensModel {
	m.width, m.height = w, h
	return m
}

// copyToClipboard writes an OSC 52 escape sequence directly to the
// terminal — works over SSH/tmux, unlike an OS clipboard library, since the
// terminal emulator (not the remote process) owns the clipboard. Best
// effort: unsupported terminals just ignore the sequence.
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
		// Hide on copy, not on keypress: this arrives once the OSC 52 sequence
		// has actually been written, so the secret stays up until the copy has
		// been attempted rather than vanishing on an keystroke that did nothing.
		m.copied = true
		m.hidden = true
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "m":
			minted, err := m.client.CreateKey()
			if err != nil {
				m.err = err
				return m, nil
			}
			m.err = nil
			m.justMinted = minted
			m.copied = false
			m.revoked = false
			m.hidden = false
			return m, nil
		case "c":
			if m.justMinted != nil && !m.revoked && !m.hidden {
				return m, copyToClipboard(m.justMinted.Key)
			}
		case "esc":
			// Dismiss without copying — for when the key is already written
			// down and you just want it off the screen.
			if m.justMinted != nil && !m.revoked {
				m.hidden = true
			}
		case "r":
			if m.justMinted != nil && !m.revoked {
				if err := m.client.RevokeKey(m.justMinted.ID); err != nil {
					m.err = err
					return m, nil
				}
				m.err = nil
				m.revoked = true
			}
		}
	}
	return m, nil
}

func (m tokensModel) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	height := m.height
	if height <= 0 {
		height = 10
	}

	// Everything is rendered inside the same full-width pane the Overview tab
	// uses, so switching tabs changes only what's in the pane — not the shape
	// of the page or where the nav sits.
	inner := max(width-6, 20)
	label := lipgloss.NewStyle().Foreground(style.Muted)

	var body string
	switch {
	case m.justMinted != nil && !m.revoked && m.hidden:
		note := "Token hidden."
		if m.copied {
			note = "Token copied to clipboard and hidden."
		}
		body = label.Width(inner).Render(
			note + "\n\nIt is no longer recoverable from this screen — if the paste " +
				"didn't land, press m for a fresh one.\n\npress r to revoke it, or m to mint another")
	case m.justMinted == nil:
		body = label.Width(inner).Render(
			"No token minted this session.\n\n" +
				"Press m to mint a Tailscale auth key, then share it with whoever's " +
				"device should join. Each key is single-use and pre-authorized.")
	case m.revoked:
		body = label.Width(inner).Render(
			"Key revoked — it can no longer be used to join.\n\npress m to mint another")
	default:
		// The key wraps rather than overflowing: auth keys run past 60
		// characters and a truncated one is worse than useless, since it looks
		// copyable but isn't.
		key := lipgloss.NewStyle().
			Foreground(style.Accent).
			Bold(true).
			Width(inner).
			Render(m.justMinted.Key)

		copyHint := "press c to copy"
		if m.copied {
			copyHint = "copied to clipboard"
		}
		body = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true).
			Render("NEW TOKEN — shown once, save it now") + "\n\n" +
			key + "\n\n" +
			label.Width(inner).Render("share this with whoever's device should join    ("+copyHint+")")
	}

	if m.err != nil {
		body += "\n\n" + style.ErrorText.Width(inner).Render(m.err.Error())
	}

	return lipgloss.NewStyle().MaxHeight(height).MaxWidth(width).
		Render(renderPane("TOKENS", body, width, height))
}
