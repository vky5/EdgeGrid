package node

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
)

func TestCheckWaitDoublesUpToADay(t *testing.T) {
	cases := map[int]time.Duration{
		-1:  10 * time.Minute,
		0:   10 * time.Minute,
		1:   20 * time.Minute,
		2:   40 * time.Minute,
		3:   80 * time.Minute,
		4:   160 * time.Minute,
		5:   320 * time.Minute,
		6:   640 * time.Minute,
		7:   21*time.Hour + 20*time.Minute,
		8:   24 * time.Hour,
		9:   24 * time.Hour,
		500: 24 * time.Hour, // a task checked daily for over a year
	}
	for attempts, want := range cases {
		if got := checkWait(attempts); got != want {
			t.Errorf("checkWait(%d) = %v, want %v", attempts, got, want)
		}
	}
}

func TestShouldGiveUpAfterSevenDays(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name    string
		started time.Time
		want    bool
	}{
		{"just sent", now, false},
		{"one minute short of 7 days", now.Add(-7*day + time.Minute), false},
		{"exactly 7 days", now.Add(-7 * day), true},
		{"8 days", now.Add(-8 * day), true},
		{"start time ahead of the clock", now.Add(time.Hour), false},
	}
	for _, c := range cases {
		if got := shouldGiveUp(c.started, now); got != c.want {
			t.Errorf("%s: shouldGiveUp = %v, want %v", c.name, got, c.want)
		}
	}
}

// bravo is the node that ran the task in these tests.
var bravo = discovery.Peer{ID: "bravo-id", Hostname: "bravo", Online: true}

// sentRow records a task sent to bravo and returns its row, aged by age.
func sentRow(t *testing.T, n *Node, age time.Duration) db.TaskRecord {
	t.Helper()
	tk, _ := task.New("slow", task.Requirements{})
	n.recordSentTask(tk, bravo)
	rec, err := n.history.Task(db.RoleSent, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec.StartedAt = time.Now().Add(-age)
	return rec
}

// answers returns an ask that always gives resp and err, counting its calls.
func answers(resp task.ResultResponse, err error) (askFunc, *int) {
	calls := 0
	return func(context.Context, discovery.Peer, string) (task.ResultResponse, error) {
		calls++
		return resp, err
	}, &calls
}

func rowAfter(t *testing.T, n *Node, rec db.TaskRecord) db.TaskRecord {
	t.Helper()
	got, err := n.history.Task(db.RoleSent, rec.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

const day = 24 * time.Hour

var online = map[string]discovery.Peer{bravo.ID: bravo}

func TestCheckStoresAFinishedAnswer(t *testing.T) {
	cases := map[string]task.ResultResponse{
		"done":   {Status: task.StatusDone, Output: []byte("42")},
		"failed": {Status: task.StatusFailed, Error: "exit status 1"},
		"lost":   {Status: task.StatusLost, Error: task.NoRecord},
	}
	for name, resp := range cases {
		n := nodeWithStore(t)
		rec := sentRow(t, n, time.Hour)
		ask, _ := answers(resp, nil)
		n.checkSentTask(context.Background(), ask, rec, online, time.Now())

		got := rowAfter(t, n, rec)
		if string(got.Status) != string(resp.Status) || got.Error != resp.Error || string(got.Output) != string(resp.Output) {
			t.Errorf("%s: row = %+v, want %+v", name, got, resp)
		}
	}
}

func TestCheckLeavesARunningTaskEvenWhenOld(t *testing.T) {
	n := nodeWithStore(t)
	rec := sentRow(t, n, 30*day)
	ask, _ := answers(task.ResultResponse{Status: task.StatusRunning}, nil)
	n.checkSentTask(context.Background(), ask, rec, online, time.Now())

	if got := rowAfter(t, n, rec); got.Status != db.TaskRunning {
		t.Errorf("row = %+v, want still running: the other node is alive and working", got)
	}
}

func TestCheckUnreachableYoungTaskWaitsForNextCheck(t *testing.T) {
	offline := bravo
	offline.Online = false
	cases := map[string]struct {
		peers   map[string]discovery.Peer
		askErr  error
		wantAsk bool
	}{
		"not on the network": {map[string]discovery.Peer{}, nil, false},
		"offline":            {map[string]discovery.Peer{bravo.ID: offline}, nil, false},
		"dial fails":         {online, errors.New("connection refused"), true},
	}
	for name, c := range cases {
		n := nodeWithStore(t)
		rec := sentRow(t, n, time.Hour)
		ask, calls := answers(task.ResultResponse{}, c.askErr)
		n.checkSentTask(context.Background(), ask, rec, c.peers, time.Now())

		if (*calls > 0) != c.wantAsk {
			t.Errorf("%s: asked %d times, want asked = %v", name, *calls, c.wantAsk)
		}
		if got := rowAfter(t, n, rec); got.Status != db.TaskRunning {
			t.Errorf("%s: row = %+v, want still running", name, got)
		}
	}
}

func TestCheckGivesUpOnAnUnreachableOldTask(t *testing.T) {
	n := nodeWithStore(t)
	rec := sentRow(t, n, 8*day)
	ask, _ := answers(task.ResultResponse{}, errors.New("connection refused"))
	n.checkSentTask(context.Background(), ask, rec, online, time.Now())

	got := rowAfter(t, n, rec)
	if got.Status != db.TaskLost || got.Error != "no answer from bravo" {
		t.Errorf("row = %+v, want lost: no answer from bravo", got)
	}
}

func TestCheckDuringShutdownNeverGivesUp(t *testing.T) {
	n := nodeWithStore(t)
	rec := sentRow(t, n, 8*day)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ask, _ := answers(task.ResultResponse{}, context.Canceled)
	n.checkSentTask(ctx, ask, rec, online, time.Now())

	if got := rowAfter(t, n, rec); got.Status != db.TaskRunning {
		t.Errorf("row = %+v, want still running: this node stopping says nothing about bravo", got)
	}
}
