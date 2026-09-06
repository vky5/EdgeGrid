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
	client *tailscaleapi.Client
	height int

	justMinted *tailscaleapi.MintedKey
	revoked    bool
	copied     bool
	err        error
}

func newTokensModel(c *tailscaleapi.Client) tokensModel {
	return tokensModel{client: c, height: 10}
}

func (m tokensModel) WithHeight(h int) tokensModel {
	m.height = h
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
		m.copied = true
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
			return m, nil
		case "c":
			if m.justMinted != nil && !m.revoked {
				return m, copyToClipboard(m.justMinted.Key)
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
	var body string
	switch {
	case m.justMinted == nil:
		body = style.Title.Render("No token minted this session") + "\n\n" +
			style.Help.Render("Press m to mint a new Tailscale auth key and share it with whoever's device should join.")
	case m.revoked:
		body = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(style.Muted).
			Padding(0, 1).
			Render(style.Help.Render("Key revoked — it can no longer be used to join.") + "\n\n" +
				style.Help.Render("press m to mint another"))
	default:
		copyHint := "press c to copy"
		if m.copied {
			copyHint = "copied!"
		}
		body = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(style.Accent).
			Padding(0, 1).
			Render(
				style.Title.Render("NEW TOKEN — shown once, save it now") + "\n\n" +
					lipgloss.NewStyle().Foreground(style.Accent).Bold(true).Render(m.justMinted.Key) + "\n\n" +
					style.Help.Render("share this with whoever's device should join   ("+copyHint+")   (r revoke)"),
			)
	}
	if m.err != nil {
		body += "\n\n" + style.ErrorText.Render(m.err.Error())
	}
	return body
}
