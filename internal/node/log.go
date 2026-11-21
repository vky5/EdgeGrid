package node

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const filename = "edgegrid.log"

// where a node's log file is, inside its data folder
func LogPath(dataDir string) string {
	return filepath.Join(dataDir, filename)
}

// sends every log.Printf to the log file, and to the terminal too unless the TUI is showing
// e.g. tsnet status, NATS connects and join events all land in edgegrid.log
// the returned func closes the file on shutdown
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

// the last lines of a node's log file, for `edgegrid logs` and the TUI's /logs
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
