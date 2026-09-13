package node

import (
	"flag"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	DataDir     string // directory for node identity and token files (default ./data)
	ProfileName string // profile that produced DataDir, "" if a --data-dir/DATA_DIR override was used instead — see resolveDataDir

	TailscaleAuthKey  string // tsnet auth key for joining the tailnet (optional; falls back to interactive login)
	TailscaleHostname string // hostname this node presents on the tailnet (default: os.Hostname() + a random suffix, see defaultHostname)
}

// ResolveDataDir applies the flag → DATA_DIR env → active profile → ./data
// fallback used everywhere a data dir is needed (node startup, the plain
// `logs` CLI) — one implementation so all of them agree on where a node's
// state lives. The flag and env var stay first so they remain an explicit
// escape hatch even once a profile is active.
func ResolveDataDir(flagVal string) string {
	dir, _ := resolveDataDir(flagVal)
	return dir
}

// resolveDataDir is ResolveDataDir's logic, but also reports which profile
// (if any) it used — from the same single read of the active-profile file
// that decided DataDir, not a second, later one. A second read is a real
// race: another EdgeGrid process switching profiles in the gap between the
// two reads leaves DataDir resolved correctly but the reported name
// pointing at whatever that other process just switched to.
func resolveDataDir(flagVal string) (dir, profileName string) {
	if flagVal != "" {
		return flagVal, ""
	}
	if v := os.Getenv("DATA_DIR"); v != "" {
		return v, ""
	}
	if name := ActiveProfile(); name != "" {
		if root, err := ProfileRoot(); err == nil {
			return filepath.Join(root, name), name
		}
	}
	return "./data", ""
}

var (
	loadOnce  sync.Once
	loadedCfg *Config
)

// LoadConfig parses flags/env into a Config exactly once per process and
// caches the result — registering the same flag name on the global
// flag.CommandLine twice panics ("flag redefined"), so every call after the
// first just returns the cached Config.
func LoadConfig() *Config {
	loadOnce.Do(func() {
		loadedCfg = loadConfigOnce()
	})
	return loadedCfg
}

func loadConfigOnce() *Config {
	dataDir := flag.String("data-dir", "", "Directory for node identity and credential files (default ./data)")
	tsAuthKey := flag.String("ts-authkey", "", "tsnet auth key for joining the tailnet (default: interactive login, or TS_AUTHKEY env)")
	tsHostname := flag.String("ts-hostname", "", "hostname to present on the tailnet (default: os.Hostname() plus a random suffix)")

	// Subcommands arrive as os.Args[1] (e.g. `edgegrid up`); Parse stops at the
	// first non-flag argument and returns cleanly, so env vars are the reliable
	// way to configure a subcommand.
	flag.Parse()

	resolvedDataDir, profileName := resolveDataDir(*dataDir)

	finalTailscaleHostname := firstNonEmpty(*tsHostname, os.Getenv("TS_HOSTNAME"))
	if finalTailscaleHostname == "" {
		finalTailscaleHostname = defaultHostname(resolvedDataDir)
	}

	return &Config{
		DataDir:           resolvedDataDir,
		ProfileName:       profileName,
		TailscaleAuthKey:  firstNonEmpty(*tsAuthKey, os.Getenv("TS_AUTHKEY")),
		TailscaleHostname: finalTailscaleHostname,
	}
}

// defaultHostname is os.Hostname() plus a short random suffix, so two data
// dirs that happen to share an OS hostname (two profiles on one machine,
// or two machines with the same name) never collide on Tailscale's own
// hostname. The suffix is generated once per data dir and persisted
// alongside node.id (see LoadOrCreateIdentity) — regenerating it on every
// restart would make Tailscale see a "new" hostname each time instead of
// the same device reconnecting.
func defaultHostname(dataDir string) string {
	base, _ := os.Hostname()
	if base == "" {
		base = "edgegrid"
	}

	const suffixFile = "hostname.suffix"
	suffix := LoadToken(dataDir, suffixFile)
	if suffix == "" {
		var err error
		suffix, err = RandomToken(2) // 2 bytes = 4 hex chars
		if err != nil || suffix == "" {
			return base
		}
		if err := SaveToken(dataDir, suffixFile, suffix); err != nil {
			return base
		}
	}
	return base + "-" + suffix
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
