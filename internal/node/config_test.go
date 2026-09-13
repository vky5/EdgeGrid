package node

import "testing"

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
