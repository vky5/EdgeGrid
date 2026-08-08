// Package nodelog is the single place that knows where a node's logs live
// on disk. `edgegrid logs` (plain CLI) and the TUI's /logs command both read
// through Tail — one implementation, not two copies of "how to get logs."
package node

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const filename = "edgegrid.log"

// Path returns where a node's log file lives under its data directory.
func LogPath(dataDir string) string {
	return filepath.Join(dataDir, filename)
}

// Setup makes the standard log package write to dataDir's log file, so every
// existing log.Printf call in the codebase — tsnet status, NATS connects, join
// events — is persisted without any of those call sites changing. In headless
// mode logs are also mirrored to stdout; in TUI mode they stay file-only so
// Bubble Tea can own the terminal. Returns a close func to flush the file on
// shutdown.
func SetupLog(dataDir string, tuiMode bool) (closeFn func() error, err error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(LogPath(dataDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	if tuiMode {
		// In TUI mode, we do NOT want logs written to os.Stdout because it corrupts the Bubble Tea screen.
		log.SetOutput(f) // set go's output to log file, not stdout
	} else {
		log.SetOutput(io.MultiWriter(os.Stdout, f))
	}
	return f.Close, nil
}

// Tail returns up to the last maxLines lines of a node's log file.
func TailLog(dataDir string, maxLines int) (string, error) {
	data, err := os.ReadFile(LogPath(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return "(no logs yet)", nil
		}
		return "", err
	}
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return "(no logs yet)", nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n"), nil
}
