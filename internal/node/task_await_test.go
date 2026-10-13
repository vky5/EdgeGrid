package node

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
)

var fastPlan = pollPlan{first: time.Millisecond, max: 5 * time.Millisecond, giveUp: 2 * time.Second}

// scripted returns each answer in turn, then repeats the last.
func scripted(answers ...func() (task.ResultResponse, error)) (func(context.Context) (task.ResultResponse, error), *int) {
	calls := 0
	return func(context.Context) (task.ResultResponse, error) {
		i := min(calls, len(answers)-1)
		calls++
		return answers[i]()
	}, &calls
}

func reply(s task.ResultStatus, out string) func() (task.ResultResponse, error) {
	return func() (task.ResultResponse, error) {
		return task.ResultResponse{Status: s, Output: []byte(out)}, nil
	}
}

func TestAwaitResultPollsUntilFinished(t *testing.T) {
	ask, calls := scripted(reply(task.StatusRunning, ""), reply(task.StatusRunning, ""), reply(task.StatusDone, "42"))

	resp, err := awaitResult(context.Background(), fastPlan, ask)
	if err != nil || resp.Status != task.StatusDone || string(resp.Output) != "42" {
		t.Fatalf("resp = %+v, err = %v", resp, err)
	}
	if *calls != 3 {
		t.Errorf("polled %d times, want 3", *calls)
	}
}

func TestAwaitResultRetriesAfterATransportError(t *testing.T) {
	down := func() (task.ResultResponse, error) { return task.ResultResponse{}, errors.New("peer offline") }
	ask, _ := scripted(down, down, reply(task.StatusFailed, ""))

	resp, err := awaitResult(context.Background(), fastPlan, ask)
	if err != nil || resp.Status != task.StatusFailed {
		t.Fatalf("resp = %+v, err = %v, want the failed status after retries", resp, err)
	}
}

func TestAwaitResultStopsOnUnknown(t *testing.T) {
	ask, calls := scripted(reply(task.StatusUnknown, ""))
	resp, err := awaitResult(context.Background(), fastPlan, ask)
	if err != nil || resp.Status != task.StatusUnknown || *calls != 1 {
		t.Errorf("resp = %+v, err = %v, calls = %d, want one poll", resp, err, *calls)
	}
}

func TestAwaitResultGivesUpWhileStillRunning(t *testing.T) {
	ask, _ := scripted(reply(task.StatusRunning, ""))
	plan := fastPlan
	plan.giveUp = 50 * time.Millisecond

	_, err := awaitResult(context.Background(), plan, ask)
	if !errors.Is(err, ErrAwaitTimeout) {
		t.Errorf("err = %v, want ErrAwaitTimeout", err)
	}
}

func TestAwaitResultTimeoutReportsTheLastTransportError(t *testing.T) {
	ask, _ := scripted(func() (task.ResultResponse, error) { return task.ResultResponse{}, errors.New("peer offline") })
	plan := fastPlan
	plan.giveUp = 50 * time.Millisecond

	_, err := awaitResult(context.Background(), plan, ask)
	if !errors.Is(err, ErrAwaitTimeout) || err.Error() == ErrAwaitTimeout.Error() {
		t.Errorf("err = %v, want a timeout that names the last error", err)
	}
}

func TestAwaitResultStopsWhenTheCallerCancels(t *testing.T) {
	ask, _ := scripted(reply(task.StatusRunning, ""))
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()

	_, err := awaitResult(ctx, fastPlan, ask)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrAwaitTimeout) {
		t.Errorf("err = %v, want plain cancellation, not a timeout", err)
	}
}

func nodeWithStore(t *testing.T) *Node {
	t.Helper()
	n := nodeWithDir(t)
	s, err := db.Open(filepath.Join(n.cfg.DataDir, historyFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	n.history = s
	return n
}

func TestSentRowIsRecordedAndFinished(t *testing.T) {
	n := nodeWithStore(t)
	tk, _ := task.New("hello", task.Requirements{})
	n.recordSentTask(tk, discovery.Peer{ID: "peer-b", Hostname: "bravo"})

	rec, err := n.history.Task(db.RoleSent, tk.ID)
	if err != nil || rec.Status != db.TaskRunning || rec.PeerHostname != "bravo" || rec.Kind != "hello" {
		t.Fatalf("after dispatch: %+v, %v", rec, err)
	}

	n.recordSentResult(tk.ID, task.ResultResponse{Status: task.StatusDone, Output: []byte("hi")})
	rec, _ = n.history.Task(db.RoleSent, tk.ID)
	if rec.Status != db.TaskDone || string(rec.Output) != "hi" {
		t.Errorf("after result: %+v", rec)
	}
}

func TestUnknownResultIsRecordedAsFailed(t *testing.T) {
	n := nodeWithStore(t)
	tk, _ := task.New("hello", task.Requirements{})
	n.recordSentTask(tk, discovery.Peer{ID: "p", Hostname: "h"})

	n.recordSentResult(tk.ID, task.ResultResponse{Status: task.StatusUnknown})
	rec, _ := n.history.Task(db.RoleSent, tk.ID)
	if rec.Status != db.TaskFailed || rec.Error == "" {
		t.Errorf("rec = %+v, want failed with a reason", rec)
	}
}

func TestSentRecordingWithoutAStoreIsANoop(t *testing.T) {
	n := nodeWithDir(t)
	tk, _ := task.New("hello", task.Requirements{})
	n.recordSentTask(tk, discovery.Peer{ID: "p"})
	n.recordSentResult(tk.ID, task.ResultResponse{Status: task.StatusDone})
}
