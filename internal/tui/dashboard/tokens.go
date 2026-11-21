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

// the tab that makes Tailscale join keys with "m"
// only the newest key is kept, in memory: shown once and revocable while it's on screen
// there's no list of older keys, since tailscaleapi can't list keys
type tokensModel struct {
	client        *tailscaleapi.Client
	width, height int

	justMinted *tailscaleapi.MintedKey
	revoked    bool
	copied     bool
	// blanks the key on screen but keeps it in justMinted, so "r" can still revoke it
	// clearing justMinted instead would lose the key's ID, leaving only the Tailscale console
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

// copies text through the terminal itself (OSC 52), so it works over SSH and tmux
// terminals that don't support it just ignore it
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
		// hide once the copy was actually sent, not on the keypress,
		// so the key doesn't vanish before it was copied
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
			// hide without copying, for when the key is already written down
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

	// same full-width pane as Overview, so switching tabs doesn't move the tab bar
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
		// wrap the key, never cut it: keys run past 60 characters
		// and a cut one looks copyable but doesn't work
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
