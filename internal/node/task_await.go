package node

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
)

// how often to ask a peer for a result, and when to stop
// e.g. after 1s, 2s, 4s, 8s, then every 10s, giving up after 10 minutes
// tests pass a shorter one
type pollPlan struct {
	first, max, giveUp time.Duration
}

var defaultPollPlan = pollPlan{
	first:  time.Second,
	max:    10 * time.Second,
	giveUp: 10 * time.Minute,
}

// waiting ended while the task was still running or the peer was unreachable
// the task keeps running, and the peer keeps its result for later
var ErrAwaitTimeout = errors.New("gave up waiting for the task result")

// keeps asking the peer until the task is over, then saves and returns the result
// a failed ask, e.g. the peer is offline, is tried again, not taken as the task failing
//
//	asks to bravo        answer                       what happens
//	at 0s                running                      wait 1s
//	at 1s                (can't connect)              not a failure, wait 2s
//	at 3s                running                      wait 4s
//	at 7s                done, "hello world\n"         saved on the "sent" row and returned
//
//	still running at 10 min      → ErrAwaitTimeout, "gave up waiting for the task result"
//	last ask failed at 10 min    → "gave up waiting for the task result (last error: …)"
//	bravo answers failed or lost → returned with a nil error; resp.Err() says why
func (a *Node) AwaitResult(ctx context.Context, peer discovery.Peer, taskID string) (task.ResultResponse, error) {
	resp, err := awaitResult(
		ctx,
		defaultPollPlan,
		func(ctx context.Context) (task.ResultResponse, error) {
			return a.askResult(ctx, peer, taskID)
		})
	if err != nil {
		return resp, err
	}
	a.recordSentResult(taskID, resp)
	return resp, nil
}

// the asking loop, kept apart from dialing so tests can run it
func awaitResult(ctx context.Context, plan pollPlan, ask func(context.Context) (task.ResultResponse, error)) (task.ResultResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, plan.giveUp)
	defer cancel()

	var lastErr error
	wait := plan.first
	for {
		resp, err := ask(ctx) // peer, taaskid coming from AwaitResult
		switch {
		case err == nil && resp.Status.Finished():
			return resp, nil
		case err != nil:
			lastErr = err
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return task.ResultResponse{}, fmt.Errorf("%w (last error: %v)", ErrAwaitTimeout, lastErr)
			}
			if errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
				return task.ResultResponse{}, ErrAwaitTimeout
			}
			return task.ResultResponse{}, ctx.Err()
		case <-time.After(wait):
		}
		wait = min(wait*2, plan.max)
	}
}

// asks a peer once how a task went: connect, say hello, ask, fetch any files, hang up
func (a *Node) askResult(ctx context.Context, peer discovery.Peer, taskID string) (task.ResultResponse, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := a.Dial(dialCtx, "tcp", net.JoinHostPort(peer.IP.String(), strconv.Itoa(discovery.Port)))
	if err != nil {
		return task.ResultResponse{}, err
	}
	defer conn.Close()

	if _, err := discovery.ExchangeAsDialer(conn, a.selfHello(discovery.IntentResult), 10*time.Second); err != nil {
		return task.ResultResponse{}, err
	}
	return a.askOver(conn, taskID)
}

// the ask itself, on a connection that has already said hello
// a done answer counts only once every file it lists is saved here, so a failed download is an error
// and the caller asks again later, the same as when the peer can't be reached
//
//	bravo answers running                          → running, nil
//	bravo answers done, no files                   → done, nil
//	bravo answers done with [model.pt], fetched    → done, nil; model.pt is in results/<task-id>/
//	bravo answers done with [model.pt], drop at 60% → error "fetch result files: …"; asked again later
func (a *Node) askOver(conn net.Conn, taskID string) (task.ResultResponse, error) {
	began := time.Now()
	if err := conn.SetDeadline(began.Add(resultTimeout)); err != nil {
		return task.ResultResponse{}, err
	}
	resp, err := task.AskResult(conn, taskID)
	if err != nil || resp.Status != task.StatusDone || len(resp.Files) == 0 {
		return resp, err
	}
	if err := a.fetchResultFiles(conn, began, taskID, resp.Files); err != nil {
		return task.ResultResponse{}, fmt.Errorf("fetch result files: %w", err)
	}
	return resp, nil
}

// saves the result on this node's row for the sent task
// with the other node's reason, e.g. "node restarted" or "no record of this task"
// and, for done, the files it fetched into results/<task-id>/
func (a *Node) recordSentResult(taskID string, resp task.ResultResponse) {
	if a.history == nil {
		return
	}
	var files []db.TaskFile
	for _, f := range resp.Files {
		files = append(files, db.TaskFile{Name: f.Name, Size: f.Size, SHA256: f.SHA256})
	}
	if err := a.history.RecordTaskFinish(db.RoleSent, taskID, db.TaskStatus(resp.Status), resp.Output, resp.Error, files); err != nil && !errors.Is(err, db.ErrTaskNotFound) {
		log.Printf("task: could not record result of %q: %v", taskID, err)
	}
}
