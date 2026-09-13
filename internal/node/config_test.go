package node

import (
	"os"
	"regexp"
	"testing"
)

var hostnameSuffixPattern = regexp.MustCompile(`^[0-9a-f]{4}$`)

func TestDefaultHostnameStableAcrossCalls(t *testing.T) {
	dir := t.TempDir()

	first := defaultHostname(dir)
	second := defaultHostname(dir)
	if first != second {
		t.Errorf("defaultHostname changed across calls for the same data dir: %q then %q", first, second)
	}

	base, _ := os.Hostname()
	suffix, ok := hostnameSuffix(t, first, base)
	if !ok {
		t.Fatalf("defaultHostname %q does not look like %q-<4 hex chars>", first, base)
	}
	if !hostnameSuffixPattern.MatchString(suffix) {
		t.Errorf("suffix %q is not 4 hex chars", suffix)
	}
}

// Two different data dirs must not collide on the same hostname — that's
// the entire point of the suffix (see defaultHostname's doc comment).
func TestDefaultHostnameDiffersAcrossDataDirs(t *testing.T) {
	a := defaultHostname(t.TempDir())
	b := defaultHostname(t.TempDir())
	if a == b {
		t.Errorf("two independent data dirs produced the same hostname %q", a)
	}
}

// hostnameSuffix splits "base-XXXX" back into its suffix, failing the test
// (via ok=false) rather than panicking on an unexpected shape.
func hostnameSuffix(t *testing.T, hostname, base string) (suffix string, ok bool) {
	t.Helper()
	prefix := base + "-"
	if len(hostname) <= len(prefix) || hostname[:len(prefix)] != prefix {
		return "", false
	}
	return hostname[len(prefix):], true
}

// An explicit --data-dir must win outright, with no profile name attached —
// this is the escape hatch resolveDataDir's doc comment promises stays
// ahead of the active profile.
func TestResolveDataDirFlagWinsWithNoProfileName(t *testing.T) {
	dir, profile := resolveDataDir("/explicit/path")
	if dir != "/explicit/path" {
		t.Errorf("dir = %q, want the flag value untouched", dir)
	}
	if profile != "" {
		t.Errorf("profile = %q, want empty — a flag override isn't a profile", profile)
	}
}

// DATA_DIR must win over the active profile, same reasoning — and same
// "no profile name attached" rule, since it's also an override, not a
// profile selection.
func TestResolveDataDirEnvWinsOverProfileWithNoProfileName(t *testing.T) {
	t.Setenv("DATA_DIR", "/env/path")
	dir, profile := resolveDataDir("")
	if dir != "/env/path" {
		t.Errorf("dir = %q, want DATA_DIR's value", dir)
	}
	if profile != "" {
		t.Errorf("profile = %q, want empty — an env override isn't a profile", profile)
	}
}
