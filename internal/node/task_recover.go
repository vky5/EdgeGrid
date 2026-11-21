package node

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
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

// askFunc asks peer how the task taskID went. Node.askResult is the real one.
type askFunc func(ctx context.Context, peer discovery.Peer, taskID string) (task.ResultResponse, error)

// checkSentTask asks the node that ran a sent task how it went, and stores the
// answer once the task is over. peers is who is on the network right now,
// keyed by Tailscale ID. If the node can't be reached, the task is left for
// its next check, unless it is old enough to give up on.
/*
peers := map[string]discovery.Peer{
    "nK7xQ2CNTRL": {
        ID:       "nK7xQ2CNTRL",
        Hostname: "alpha5-297b",                 // your 2nd-cluster profile, B
        IP:       netip.MustParseAddr("100.96.191.21"),
        Online:   true,
    },
    "nP3mR8CNTRL": {
        ID:       "nP3mR8CNTRL",
        Hostname: "old-laptop",
        IP:       netip.MustParseAddr("100.70.12.5"),
        Online:   false,                         // known to Tailscale, but switched off
        LastSeen: time.Date(2026, 11, 19, 22, 4, 0, 0, time.Local),
    },
}
*/
func (a *Node) checkSentTask(ctx context.Context, ask askFunc, rec db.TaskRecord, peers map[string]discovery.Peer, now time.Time) {
	peer, found := peers[rec.PeerID]
	if found && peer.Online {
		resp, err := ask(ctx, peer, rec.TaskID)
		switch {
		case err == nil && resp.Status == task.StatusRunning:
			return // still working, the next check is already set
		case err == nil:
			log.Printf("task: %q: %s answered %s on check %d", rec.TaskID, rec.PeerHostname, resp.Status, rec.Attempts)
			a.recordSentResult(rec.TaskID, resp)
			return
		case ctx.Err() != nil:
			return // this node is shutting down, which says nothing about the other one
		}
		log.Printf("task: %q: could not reach %s on check %d: %v", rec.TaskID, rec.PeerHostname, rec.Attempts, err)
	}

	if !shouldGiveUp(rec.StartedAt, now) {
		return
	}
	log.Printf("task: %q: giving up, no answer from %s since %s", rec.TaskID, rec.PeerHostname, rec.StartedAt.Format(time.DateTime))
	a.recordSentResult(rec.TaskID, task.ResultResponse{
		Status: task.StatusLost,
		Error:  fmt.Sprintf("no answer from %s", rec.PeerHostname),
	})
}
