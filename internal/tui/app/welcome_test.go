package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"path/filepath"
	"testing"

	"github.com/edgegrid/edgegrid/internal/node"
	"github.com/edgegrid/edgegrid/internal/tailscaleapi"
)

func TestSettingsRoundTripEnablesTokensTab(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // ProfileRoot() -> $HOME/.edgegrid
	if err := node.UseProfile("worker-1"); err != nil {
		t.Fatalf("UseProfile: %v", err)
	}
	dir := profileDataDir("worker-1")

	m := welcomeModel{selectedProfileName: "worker-1"}
	m.openProfileSettings()
	if m.settingsDir != dir {
		t.Fatalf("settingsDir = %q want %q", m.settingsDir, dir)
	}
	want := map[string]string{
		"Tailscale API Client ID":     "kabc123",
		"Tailscale API Client Secret": "tskey-client-SECRETVALUE",
		"Tailscale API Tailnet":       "example.com",
		"Tailscale API Tag":           "tag:edgegrid",
		"API Port":                    "8080",
		"Require Approval":            "true",
	}
	for k, v := range want {
		m.settingsVals[k] = v
	}
	if err := m.persistProfileSettings(); err != nil {
		t.Fatalf("persist: %v", err)
	}

	// these files are what LoadCredentials reads, and a client from it adds the Tokens tab
	if tailscaleapi.LoadCredentials(dir) == nil {
		t.Fatal("LoadCredentials nil after save — Tokens tab would stay hidden")
	}

	var m2 welcomeModel
	m2.selectedProfileName = "worker-1"
	m2.openProfileSettings()
	for k, v := range want {
		if got := m2.settingsVals[k]; got != v {
			t.Errorf("%s: round-trip got %q want %q", k, got, v)
		}
	}

	// clearing a field deletes its file instead of leaving it empty
	m2.settingsVals["Tailscale API Client Secret"] = ""
	if err := m2.persistProfileSettings(); err != nil {
		t.Fatalf("persist after clear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ts_api_client_secret")); !os.IsNotExist(err) {
		t.Errorf("cleared secret should be removed, stat err = %v", err)
	}
	if tailscaleapi.LoadCredentials(dir) != nil {
		t.Error("LoadCredentials should be nil once the secret is cleared")
	}
}

func TestAPIPortRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"nope", "0", "70000", "-1"} {
		m := welcomeModel{
			settingsDir:    t.TempDir(),
			settingsFields: settingsFieldOrder,
			settingsVals:   map[string]string{"API Port": bad},
		}
		if err := m.persistProfileSettings(); err == nil {
			t.Errorf("API Port %q should have been rejected", bad)
		}
	}
}

func TestDefaultProfileDirIsLocalData(t *testing.T) {
	if got := profileDataDir(""); got != "./data" {
		t.Errorf("profileDataDir(\"\") = %q want ./data", got)
	}
}

// the save message clears itself, and an old timer doesn't wipe a newer message
// e.g. a second ctrl+s soon after the first keeps its message
func TestSettingsStatusExpiry(t *testing.T) {
	var m welcomeModel

	// read seq from the model instead of running the cmd: tea.Tick really sleeps
	m, cmd := m.setSettingsStatus("saved once")
	if cmd == nil {
		t.Fatal("setSettingsStatus returned no expiry command")
	}
	firstTick := settingsStatusExpiredMsg{seq: m.settingsStatusSeq}

	// Save again before the first tick fires.
	m, cmd = m.setSettingsStatus("saved twice")
	if cmd == nil {
		t.Fatal("second save scheduled no expiry")
	}
	secondTick := settingsStatusExpiredMsg{seq: m.settingsStatusSeq}
	if firstTick.seq == secondTick.seq {
		t.Fatalf("both saves scheduled seq %d — stale ticks are indistinguishable", firstTick.seq)
	}

	// The stale tick must be ignored.
	m, _ = m.updateProfileSettings(firstTick)
	if m.settingsStatus != "saved twice" {
		t.Errorf("stale tick cleared the newer status: got %q", m.settingsStatus)
	}

	// The current one clears it.
	m, _ = m.updateProfileSettings(secondTick)
	if m.settingsStatus != "" {
		t.Errorf("current tick did not clear status: got %q", m.settingsStatus)
	}
}

// messages that aren't keys still reach the settings screen, e.g. the expiry tick and the cursor blink
func TestSettingsReceivesNonKeyMessages(t *testing.T) {
	m := welcomeModel{subMode: 6}
	m, _ = m.setSettingsStatus("saved")
	got, _ := m.update(settingsStatusExpiredMsg{seq: m.settingsStatusSeq})
	if got.settingsStatus != "" {
		t.Errorf("expiry tick never reached the settings screen: status still %q", got.settingsStatus)
	}
}

func TestExtractLoginURL(t *testing.T) {
	line := "2026/09/09 12:42:07 To start this tsnet server, restart with TS_AUTHKEY set, or go to: https://login.tailscale.com/a/701180b014f14"
	if got := extractLoginURL(line); got != "https://login.tailscale.com/a/701180b014f14" {
		t.Errorf("got %q", got)
	}
	if got := extractLoginURL("wgengine: Reconfig done"); got != "" {
		t.Errorf("non-auth line yielded %q", got)
	}
}

// "joined" means tailscale.ip exists, written only after a join works
// tsnet/tailscaled.state isn't enough: it's there even after a failed login
func TestProfileHasJoinedUsesTailscaleIPNotTsnetState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := node.UseProfile("p1"); err != nil {
		t.Fatal(err)
	}
	dir := profileDataDir("p1")

	if profileHasJoined("p1") {
		t.Error("fresh profile reported as joined")
	}
	// A half-finished bring-up leaves tsnet state behind but no IP.
	if err := os.MkdirAll(filepath.Join(dir, "tsnet"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := node.SaveToken(filepath.Join(dir, "tsnet"), "tailscaled.state", "{}"); err != nil {
		t.Fatal(err)
	}
	if profileHasJoined("p1") {
		t.Error("tsnet state alone should not count as joined")
	}

	if err := node.SaveToken(dir, "tailscale.ip", "100.92.16.79"); err != nil {
		t.Fatal(err)
	}
	if !profileHasJoined("p1") {
		t.Error("profile with a tailscale.ip should count as joined")
	}
}

func TestProfileHasTailscaleAPINeedsAllThree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := node.UseProfile("p2"); err != nil {
		t.Fatal(err)
	}
	dir := profileDataDir("p2")

	for _, f := range []string{"ts_api_client_id", "ts_api_client_secret"} {
		if err := node.SaveToken(dir, f, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if profileHasTailscaleAPI("p2") {
		t.Error("two of three files should not be enough — LoadCredentials needs the tailnet too")
	}
	if err := node.SaveToken(dir, "ts_api_tailnet", "tail2225ba.ts.net"); err != nil {
		t.Fatal(err)
	}
	if !profileHasTailscaleAPI("p2") {
		t.Error("all three present but reported unconfigured")
	}
}

// a profile that already joined skips the role screen
// neither option fits it, and tsnet ignores a join key once it has joined
func TestStartNodeSkipsRoleScreenWhenAlreadyJoined(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := node.UseProfile("joined"); err != nil {
		t.Fatal(err)
	}
	if err := node.SaveToken(profileDataDir("joined"), "tailscale.ip", "100.1.2.3"); err != nil {
		t.Fatal(err)
	}

	m := welcomeModel{selectedProfileName: "joined", subMode: 3, submenuIdx: 0}
	cmd := m.triggerSubmenu()
	if cmd == nil || m.action != WelcomeStart {
		t.Fatalf("joined profile should boot straight away, got action=%v cmd=%v", m.action, cmd != nil)
	}

	// A fresh one goes to the role screen instead.
	if err := node.UseProfile("fresh"); err != nil {
		t.Fatal(err)
	}
	m2 := welcomeModel{selectedProfileName: "fresh", subMode: 3, submenuIdx: 0}
	if cmd := m2.triggerSubmenu(); cmd != nil {
		t.Error("fresh profile should not quit welcome")
	}
	if m2.subMode != 7 {
		t.Errorf("fresh profile should land on the role screen, got subMode %d", m2.subMode)
	}
}

// "New network" without an API login warns but doesn't block; the login can be added later
func TestNewNetworkWarnsWhenCredentialsMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := node.UseProfile("n1"); err != nil {
		t.Fatal(err)
	}

	m := welcomeModel{selectedProfileName: "n1", subMode: 7, roleIdx: 0}
	if cmd := m.triggerRole(); cmd != nil {
		t.Error("should not boot while credentials are missing")
	}
	if m.subMode != 9 {
		t.Errorf("expected the configure-settings notice, got subMode %d", m.subMode)
	}

	dir := profileDataDir("n1")
	for _, f := range []string{"ts_api_client_id", "ts_api_client_secret", "ts_api_tailnet"} {
		if err := node.SaveToken(dir, f, "x"); err != nil {
			t.Fatal(err)
		}
	}
	m2 := welcomeModel{selectedProfileName: "n1", subMode: 7, roleIdx: 0}
	if cmd := m2.triggerRole(); cmd == nil || m2.action != WelcomeStart {
		t.Error("configured profile should boot straight away")
	}
}

// the join screen hands its key out of welcome, since tsnet needs it before it starts
func TestJoinScreenReturnsAuthKey(t *testing.T) {
	m := welcomeModel{selectedProfileName: "j1", subMode: 7, roleIdx: 1}
	m.triggerRole()
	if m.subMode != 8 {
		t.Fatalf("join should open the auth-key screen, got subMode %d", m.subMode)
	}
	m.input.SetValue("  tskey-auth-PASTED  ")

	got, _ := m.update(tea.KeyMsg{Type: tea.KeyEnter})
	if got.Result().AuthKey != "tskey-auth-PASTED" {
		t.Errorf("auth key not carried out (or not trimmed): %q", got.Result().AuthKey)
	}
	if got.Result().Action != WelcomeStart {
		t.Error("enter on the join screen should start the node")
	}
}
