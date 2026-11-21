package node

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
)

// node with a database and run tracking, stopped after the test
func checkingNode(t *testing.T) *Node {
	t.Helper()
	n := nodeWithStore(t)
	n.runCtx, n.stopRuns = context.WithCancel(context.Background())
	t.Cleanup(func() { n.stopRuns(); n.runs.Wait() })
	return n
}

// records count tasks sent to bravo and returns their IDs, e.g. 30 for the pool test
func sendTasks(t *testing.T, n *Node, count int) []string {
	t.Helper()
	ids := make([]string, count)
	for i := range ids {
		tk, _ := task.New("slow", task.Requirements{})
		n.recordSentTask(tk, bravo)
		ids[i] = tk.ID
	}
	return ids
}

// peer list where bravo is online
func bravoOnline(context.Context) (map[string]discovery.Peer, error) { return online, nil }

// a time when every task sent just now is due, 11 min from now
func afterFirstCheck() time.Time { return time.Now().Add(db.FirstCheckAfter + time.Minute) }

func TestFeedClaimsEachDueTaskOnceAndSetsItsNextCheck(t *testing.T) {
	n := checkingNode(t)
	ids := sendTasks(t, n, 3)
	now := afterFirstCheck()
	jobs := make(chan checkJob, 10)

	n.feedDueTasks(context.Background(), jobs, bravoOnline, now)
	if len(jobs) != 3 {
		t.Fatalf("first round handed out %d tasks, want 3", len(jobs))
	}
	for range 3 {
		if job := <-jobs; job.rec.Attempts != 1 || job.peers[bravo.ID].Hostname != "bravo" {
			t.Errorf("job = %+v, want attempt 1 with bravo in the peer list", job)
		}
	}
	for _, id := range ids {
		rec, _ := n.history.Task(db.RoleSent, id)
		if want := now.Add(20 * time.Minute); rec.Attempts != 1 || rec.NextCheckAt.Unix() != want.Unix() {
			t.Errorf("row: attempts %d, next check %v, want 1 and %v", rec.Attempts, rec.NextCheckAt, want)
		}
	}

	n.feedDueTasks(context.Background(), jobs, bravoOnline, now)
	if len(jobs) != 0 {
		t.Errorf("same minute again handed out %d tasks, want 0", len(jobs))
	}
}

func TestFeedSkipsTheRoundWithoutAPeerList(t *testing.T) {
	n := checkingNode(t)
	ids := sendTasks(t, n, 1)
	jobs := make(chan checkJob, 1)
	noList := func(context.Context) (map[string]discovery.Peer, error) {
		return nil, errors.New("tailscale not ready")
	}

	n.feedDueTasks(context.Background(), jobs, noList, afterFirstCheck())
	if len(jobs) != 0 {
		t.Error("handed out a task with no peer list")
	}
	if rec, _ := n.history.Task(db.RoleSent, ids[0]); rec.Attempts != 0 {
		t.Errorf("claimed with no peer list: attempts %d", rec.Attempts)
	}
}

func TestChecksNeverRunMoreThanTheWorkerCount(t *testing.T) {
	n := checkingNode(t)
	ids := sendTasks(t, n, 30)

	var mu sync.Mutex
	inFlight, most := 0, 0
	asked := map[string]int{}
	var calls atomic.Int32
	ask := func(_ context.Context, _ discovery.Peer, id string) (task.ResultResponse, error) {
		mu.Lock()
		inFlight++
		most = max(most, inFlight)
		asked[id]++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		calls.Add(1)
		return task.ResultResponse{Status: task.StatusDone, Output: []byte("ok")}, nil
	}

	n.runTaskChecks(context.Background(), 5, time.Hour, ask, bravoOnline, afterFirstCheck)

	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 30 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if most != 5 {
		t.Errorf("at most %d checks ran at once, want exactly 5", most)
	}
	for _, id := range ids {
		if asked[id] != 1 {
			t.Errorf("%s asked %d times, want 1", id, asked[id])
		}
	}
	waitForStatus(t, n, ids, db.TaskDone)
}

func TestChecksStopWhenStartsContextEnds(t *testing.T) {
	n := checkingNode(t)
	ctx, cancel := context.WithCancel(context.Background())
	n.runTaskChecks(ctx, 5, time.Hour, nil, bravoOnline, time.Now)
	cancel()

	done := make(chan struct{})
	go func() { n.runs.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("feeder and workers still running after Start's context ended")
	}
}

func TestTaskCheckWorkersSetting(t *testing.T) {
	cases := map[string]int{"": 5, "3": 3, "0": 5, "-2": 5, "lots": 5}
	for value, want := range cases {
		t.Setenv("TASK_CHECK_WORKERS", value)
		if got := taskCheckWorkers(); got != want {
			t.Errorf("TASK_CHECK_WORKERS=%q: got %d, want %d", value, got, want)
		}
	}
}

// waits up to 5 s for every sent row to reach want, e.g. all done
func waitForStatus(t *testing.T, n *Node, ids []string, want db.TaskStatus) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for _, id := range ids {
		for {
			rec, _ := n.history.Task(db.RoleSent, id)
			if rec.Status == want {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s is %s, want %s", id, rec.Status, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
