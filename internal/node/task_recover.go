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

// the longest gap between two checks on a sent task, a day
const maxCheckWait = 24 * time.Hour

// how long to wait before the next check, from how many checks came before
// 10 minutes, doubled each time, at most a day
//
//	attempts   wait          time since sent
//	0          10 min        10 min
//	1          20 min        30 min
//	2          40 min        1 h 10 min
//	7          21 h 20 min   1 day 18 h 30 min
//	8 or more  1 day         then once a day
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

// whether a sent task is too old to keep asking about
// the node that ran it keeps results 7 days, so asking after that can't help
// e.g. sent 2026-11-14 09:00: false until 2026-11-21 09:00, true from then on
func shouldGiveUp(startedAt, now time.Time) bool {
	return now.Sub(startedAt) >= task.ResultRetention
}

// asks the other computer how one task went; a.askResult in real use, a fake in tests
//
//	ask(ctx, alpha5-297b, "fba47d80-…") → {Status: "done", Output: "hello from B\n"}, nil
//	ask(ctx, alpha5-297b, "fba47d80-…") → {Status: "running"}, nil
//	ask(ctx, alpha5-297b, "fba47d80-…") → {Status: "lost", Error: "no record of this task"}, nil
//	ask(ctx, alpha5-297b, "fba47d80-…") → {}, "context deadline exceeded"   (B didn't answer)
type askFunc func(ctx context.Context, peer discovery.Peer, taskID string) (task.ResultResponse, error)

// asks the node that ran a sent task how it went, and saves the answer once the task is over
// peers is who is on the network right now, by Tailscale ID, e.g.
//
//	peers := map[string]discovery.Peer{
//		"nK7xQ2CNTRL": {
//			ID:       "nK7xQ2CNTRL",
//			Hostname: "alpha5-297b",
//			IP:       netip.MustParseAddr("100.x.y.z"),
//			Online:   true, // dialled
//		},
//		"nP3mR8CNTRL": {
//			ID:       "nP3mR8CNTRL",
//			Hostname: "old-laptop",
//			IP:       netip.MustParseAddr("100.x.y.z"),
//			Online:   false, // known to Tailscale but switched off: skipped, unless the task is 7 days old
//			LastSeen: time.Date(2026, 11, 19, 22, 4, 0, 0, time.Local),
//		},
//	}
//
// what happens to the row, for a task sent to alpha5-297b:
//
//	answer                          row afterwards
//	running                         left alone, however old the task is
//	done, "hello from B"            done, output saved
//	failed or lost, with a reason   the same status and reason
//	no answer, sent 2 days ago      left for its next check
//	no answer, sent 8 days ago      lost, "no answer from alpha5-297b"
//	this node shutting down         left alone
func (a *Node) checkSentTask(ctx context.Context, ask askFunc, rec db.TaskRecord, peers map[string]discovery.Peer, now time.Time) {
	peer, found := peers[rec.PeerID]
	tooOld := shouldGiveUp(rec.StartedAt, now)

	// an offline peer is still dialled once before giving up on its task
	// so Tailscale being a minute late to say it's back can't lose a finished result
	if found && (peer.Online || tooOld) {
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

	if !tooOld {
		return
	}
	log.Printf("task: %q: giving up, no answer from %s since %s", rec.TaskID, rec.PeerHostname, rec.StartedAt.Format(time.DateTime))
	a.recordSentResult(rec.TaskID, task.ResultResponse{
		Status: task.StatusLost,
		Error:  fmt.Sprintf("no answer from %s", rec.PeerHostname),
	})
}
