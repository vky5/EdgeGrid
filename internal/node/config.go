package node

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// where this node keeps its files and what it calls itself on the tailnet
//
//	// edgegrid dashboard, profile primary active, on a machine named alpha5
//	node.Config{
//		DataDir:           "~/.edgegrid/primary",   // full path in practice, from the home folder
//		ProfileName:       "primary",
//		TailscaleHostname: "alpha5-297b",           // machine name plus the suffix saved in hostname.suffix
//	}
//	// DATA_DIR=./data2 TS_HOSTNAME=bravo edgegrid dashboard
//	node.Config{DataDir: "./data2", ProfileName: "", TailscaleHostname: "bravo"}
//
//	what can end up in ~/.edgegrid/primary/ (inbox, runs and the acl files only once used or hand-made)
//	node.id  hostname.suffix  tailscale.ip  tsnet/  edgegrid.db  edgegrid.log  tasks.json
//	runs/  inbox/  acl/  acl.default  acl-tasks/  acl-tasks.default  max_blob_bytes
type Config struct {
	DataDir     string // directory for node identity and token files (default ./data)
	ProfileName string // profile that gave DataDir, "" if --data-dir or DATA_DIR was used instead

	TailscaleAuthKey  string // tsnet auth key for joining the tailnet (optional; falls back to interactive login)
	TailscaleHostname string // name on the tailnet (default: OS hostname plus a random suffix)

	TaskCheckWorkers int // how many sent tasks we check on at once, e.g. TASK_CHECK_WORKERS=10
}

// sent tasks checked at once when TASK_CHECK_WORKERS isn't set
const defaultTaskCheckWorkers = 5

// reads TASK_CHECK_WORKERS, falling back to 5
// e.g. "3" → 3; "", "0" or "lots" → 5
func taskCheckWorkers() int {
	n, err := strconv.Atoi(os.Getenv("TASK_CHECK_WORKERS"))
	if err != nil || n < 1 {
		return defaultTaskCheckWorkers
	}
	return n
}

// picks the data folder: --data-dir, then DATA_DIR, then the active profile, then ./data
// the flag and env come first so they still win once a profile is active
//
//	--data-dir ./data2, DATA_DIR=/tmp/eg, profile primary   → "./data2"
//	no flag, DATA_DIR=/tmp/eg, profile primary              → "/tmp/eg"
//	no flag, no DATA_DIR, profile primary                   → "~/.edgegrid/primary" (made absolute)
//	no flag, no DATA_DIR, no active profile                 → "./data"
func ResolveDataDir(flagVal string) string {
	dir, _ := resolveDataDir(flagVal)
	return dir
}

// same as ResolveDataDir, but also says which profile it used
// from the same read of the active profile, since another edgegrid process may switch profiles in between
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

// reads flags and env into a Config once, and returns that same Config after
// registering a flag twice panics, so later calls can't parse again
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

	// parsing stops at a subcommand, e.g. `edgegrid up`
	// so flags after it are not read; use env vars to set up a subcommand
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
		TaskCheckWorkers:  taskCheckWorkers(),
	}
}

// the OS hostname plus a saved random suffix, e.g. laptop-3f9a
// so two profiles or two machines with the same name never share a Tailscale name
// the suffix is made once per data folder, or Tailscale would see a new device on every restart
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
