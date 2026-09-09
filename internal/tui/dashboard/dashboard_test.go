package dashboard

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/edgegrid/edgegrid/internal/tailscaleapi"
)

// app.View centers the dashboard horizontally, so a tab whose content is
// narrower than the terminal used to drag the tab bar into the middle of the
// screen along with it — the nav visibly jumped when switching to Tokens.
// Every tab must therefore render at the full width it was given.
func TestTabsRenderFullWidthSoNavDoesNotMove(t *testing.T) {
	const w, h = 100, 24

	d := New("88c12417791e89d21aa", "100.92.16.79", "./data", &tailscaleapi.Client{})
	d.width, d.height = w, h
	d.resize()
	d.tokens.justMinted = &tailscaleapi.MintedKey{
		ID:  "k1",
		Key: "tskey-auth-kSxggEGSD311CNTRL-8fJq2mNpXvZ4tR7wYbC1dEaLgH",
	}

	navCols := func(view string) (cols [3]int, width int) {
		bar := strings.SplitN(view, "\n", 2)[0]
		plain := stripANSI(bar)
		for i, word := range []string{"OVERVIEW", "TOKENS", "( press"} {
			idx := strings.Index(plain, word)
			if idx < 0 {
				t.Fatalf("nav item %q missing from tab bar: %q", word, plain)
			}
			cols[i] = idx
		}
		return cols, lipgloss.Width(bar)
	}

	d.tab = tabOverview
	overviewCols, overviewW := navCols(d.View())
	d.tab = tabTokens
	tokensCols, tokensW := navCols(d.View())

	if overviewW != w || tokensW != w {
		t.Errorf("tab bar widths overview=%d tokens=%d, both should be %d", overviewW, tokensW, w)
	}
	if overviewCols != tokensCols {
		t.Errorf("nav moved between tabs: overview=%v tokens=%v", overviewCols, tokensCols)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Copying must take the secret off the screen, but must not strand the key:
// "r" still has to be able to revoke what was just minted.
func TestCopyHidesSecretButKeepsRevokable(t *testing.T) {
	const secret = "tskey-auth-kGnKjCGsLo11CNTRL-fNuqh2GjA13WAraAk6GXz25izc5ST4Upf"
	m := tokensModel{
		width: 100, height: 20,
		justMinted: &tailscaleapi.MintedKey{ID: "k1", Key: secret},
	}

	if !strings.Contains(stripANSI(m.View()), secret) {
		t.Fatal("freshly minted key should be on screen")
	}

	// "c" schedules the copy; the hide lands when the copy actually completes.
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Fatal("c produced no copy command")
	}
	if strings.Contains(stripANSI(m2.View()), secret) == false {
		t.Error("key should still show until the copy completes")
	}
	m3, _ := m2.Update(cmd())

	if strings.Contains(stripANSI(m3.View()), secret) {
		t.Error("secret still on screen after copy completed")
	}
	if m3.justMinted == nil || m3.justMinted.ID != "k1" {
		t.Error("key ID dropped — r could no longer revoke it")
	}

	// esc hides without copying.
	m4, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if strings.Contains(stripANSI(m4.View()), secret) {
		t.Error("esc did not hide the secret")
	}
	if m4.copied {
		t.Error("esc should not claim the key was copied")
	}

	// Minting again brings a new key back into view.
	m5 := m3
	m5.justMinted = &tailscaleapi.MintedKey{ID: "k2", Key: "tskey-auth-SECOND"}
	m5.hidden = false
	if !strings.Contains(stripANSI(m5.View()), "tskey-auth-SECOND") {
		t.Error("newly minted key not shown")
	}
}
