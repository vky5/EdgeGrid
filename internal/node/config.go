package node

import (
	"flag"
	"os"
	"sync"
)

type Config struct {
	DataDir string // directory for node identity and token files (default ./data)

	TailscaleAuthKey  string // tsnet auth key for joining the tailnet (optional; falls back to interactive login)
	TailscaleHostname string // hostname this node presents on the tailnet (default: os.Hostname())
}

// ResolveDataDir applies the flag → DATA_DIR env → active profile → ./data
// fallback used everywhere a data dir is needed (node startup, the plain
// `logs` CLI) — one implementation so all of them agree on where a node's
// state lives. The flag and env var stay first so they remain an explicit
// escape hatch even once a profile is active.
func ResolveDataDir(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("DATA_DIR"); v != "" {
		return v
	}
	if dir := ProfileDir(); dir != "" {
		return dir
	}
	return "./data"
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
	tsHostname := flag.String("ts-hostname", "", "hostname to present on the tailnet (default: os.Hostname())")

	// Subcommands arrive as os.Args[1] (e.g. `edgegrid up`); Parse stops at the
	// first non-flag argument and returns cleanly, so env vars are the reliable
	// way to configure a subcommand.
	flag.Parse()

	finalTailscaleHostname := firstNonEmpty(*tsHostname, os.Getenv("TS_HOSTNAME"))
	if finalTailscaleHostname == "" {
		finalTailscaleHostname, _ = os.Hostname()
	}

	return &Config{
		DataDir:           ResolveDataDir(*dataDir),
		TailscaleAuthKey:  firstNonEmpty(*tsAuthKey, os.Getenv("TS_AUTHKEY")),
		TailscaleHostname: finalTailscaleHostname,
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
