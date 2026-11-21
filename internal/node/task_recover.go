package node

import (
	"time"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/task"
)

// maxCheckWait is the longest gap between two checks on a sent task.
const maxCheckWait = 24 * time.Hour

// checkWait is how long to wait before the next check, given how many checks
// have already been made: 10 minutes, doubled each time, at most a day.
func checkWait(attempts int) time.Duration {
	wait := db.FirstCheckAfter // time between sending a task and first time feeder checks on it
	for i := 0; i < attempts; i++ {
		wait *= 2
		if wait >= maxCheckWait {
			return maxCheckWait
		}
	}
	return wait
}

// shouldGiveUp reports whether a sent task is too old to keep asking about:
// the other node has thrown its result away by now.
func shouldGiveUp(startedAt, now time.Time) bool {
	return now.Sub(startedAt) >= task.ResultRetention
}
