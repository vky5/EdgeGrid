package node

import (
	"context"
	"log"
	"time"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
)

// how this node goes back and asks about tasks it sent
// runs inside `up` and the dashboard, never inside `task send`
//
//	once at startup, then every checkEvery (1 minute)
//	┌──────────────────────────────────────┐
//	│ feeder, 1 goroutine (feedDueTasks)   │
//	│                                      │
//	│ 1. which sent tasks are due?  ───────┼──► tasks table (DueSentTasks)
//	│ 2. who is on the network right now? ─┼──► Tailscale (currentPeers)
//	│    no answer: skip this round        │
//	│ 3. claim each due task        ───────┼──► tasks table (ClaimSentTask):
//	│    claim failed: skip it             │    attempts + 1,
//	│                                      │    next check = now + checkWait
//	│ 4. put it on the channel             │
//	└──────────────────┬───────────────────┘
//	                   │
//	                   ▼
//	      ══════════ jobs channel ══════════   feeder waits here while
//	        │      │      │      │      │      every worker is busy
//	        ▼      ▼      ▼      ▼      ▼
//	      ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐
//	      │ w1 │ │ w2 │ │ w3 │ │ w4 │ │ w5 │   TASK_CHECK_WORKERS, default 5
//	      └─┬──┘ └────┘ └────┘ └────┘ └────┘
//	        │
//	        │  each worker, one task at a time (checkSentTask):
//	        │
//	        │  other node offline or gone?   don't dial
//	        │  otherwise ask it ───────────────────────────►  other node
//	        │                  ◄───────────────────────────  answer
//	        │
//	        │  running               → leave it, next check is already set
//	        │  done / failed / lost  → save it on the row, finished
//	        │  no answer, < 7 days   → leave it
//	        │  no answer, ≥ 7 days   → lost: "no answer from <host>"
//	        ▼

// how often feeder checks in the db for the task
const checkEvery = time.Minute

// most due tasks the feeder reads from the table in one look
// e.g. 250 overdue after a week off: 100 now, 100 next minute, 50 after that
const maxDuePerRound = 100

// package feeder gives to a worker
type checkJob struct {
	rec   db.TaskRecord             // the task we are checking
	peers map[string]discovery.Peer // who was online when feeder ran
}

// gets who is on the network right now, keyed by Tailscale ID
// e.g. bravo's ID → bravo, online
type peersFunc func(ctx context.Context) (map[string]discovery.Peer, error)

// starts the feeder and workers, if this node keeps a task history
// called from Start, so `up` and the dashboard run it and `task send` doesn't
func (a *Node) startTaskChecks(ctx context.Context) {
	if a.history == nil {
		return
	}
	a.runTaskChecks(ctx, a.cfg.TaskCheckWorkers, checkEvery, a.askResult, a.currentPeers, time.Now)
}

// starts one feeder and the workers, joined by the jobs channel
// e.g. 5 workers, and a feeder that looks now and then every minute
func (a *Node) runTaskChecks(startCtx context.Context, workers int, interval time.Duration, ask askFunc, peers peersFunc, now func() time.Time) {
	// stops on Ctrl+C or when the node closes, whichever comes first
	ctx, cancel := context.WithCancel(a.runCtx)
	stop := context.AfterFunc(startCtx, cancel)

	// no room inside, so each send waits for a free worker
	// and the channel hands each task to exactly one worker
	jobs := make(chan checkJob)

	// the workers, e.g. 5: each takes a task off the channel, checks it, takes the next
	// they stop when the feeder closes the channel
	for range workers {
		a.runs.Go(func() {
			for job := range jobs {
				a.checkSentTask(ctx, ask, job.rec, job.peers, now())
			}
		})
	}

	// the feeder, just one: looks for due tasks now, then every interval, until shutdown
	// counted on a.runs like the workers, so Close waits for both before closing the db
	a.runs.Go(func() {
		defer stop()
		defer cancel()
		defer close(jobs) // tells the workers there is nothing more coming

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			a.feedDueTasks(ctx, jobs, peers, now())
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

// claims each due task, then hands it to a free worker
// e.g. 12 due: hands out 5, then the next one each time a worker frees up
func (a *Node) feedDueTasks(ctx context.Context, jobs chan<- checkJob, peers peersFunc, now time.Time) {
	due, err := a.history.DueSentTasks(now, maxDuePerRound)
	if err != nil {
		log.Printf("task: list tasks due a check: %v", err)
		return
	}
	if len(due) == 0 {
		return
	}
	// no peer list means every node looks offline,
	// so skip the round rather than give up on week-old tasks
	online, err := peers(ctx)
	if err != nil {
		log.Printf("task: skipping this round of checks, no peer list: %v", err)
		return
	}
	for _, rec := range due {
		claimed, err := a.history.ClaimSentTask(rec.TaskID, now, now.Add(checkWait(rec.Attempts+1)))
		if err != nil {
			log.Printf("task: %q: claim for a check: %v", rec.TaskID, err)
			continue
		}
		if !claimed {
			continue // already done, or another process on this profile took it
		}
		rec.Attempts++
		select {
		case jobs <- checkJob{rec: rec, peers: online}:
		case <-ctx.Done():
			return
		}
	}
}

// the real peersFunc: every edgegrid device Tailscale knows right now, online or not
func (a *Node) currentPeers(ctx context.Context) (map[string]discovery.Peer, error) {
	lc, err := a.LocalClient()
	if err != nil {
		return nil, err
	}
	list, err := discovery.Snapshot(ctx, lc)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]discovery.Peer, len(list))
	for _, p := range list {
		byID[p.ID] = p
	}
	return byID, nil
}
