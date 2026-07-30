package config

// KnownExecutors is the closed list of worker executor backends.
// UI pickers should cycle this list instead of free-text entry.
var KnownExecutors = []string{
	"training", // runs real Python scripts, streams logs via NATS
	"mock",     // short fake run for tests/CI without Python
}

// NormalizeExecutor returns a known executor name, or "" if invalid.
func NormalizeExecutor(s string) string {
	switch s {
	case "training", "mock":
		return s
	default:
		return ""
	}
}

// ExecutorIndex returns the index in KnownExecutors, or 0 if unknown.
func ExecutorIndex(s string) int {
	for i, e := range KnownExecutors {
		if e == s {
			return i
		}
	}
	return 0
}

// CycleExecutor moves ±1 through KnownExecutors (wraps).
func CycleExecutor(current string, delta int) string {
	i := ExecutorIndex(current)
	n := len(KnownExecutors)
	i = (i + delta%n + n) % n
	return KnownExecutors[i]
}
