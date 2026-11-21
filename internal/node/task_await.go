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

// asks a peer once how a task went: connect, say hello, ask, hang up
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
	if err := conn.SetDeadline(time.Now().Add(resultTimeout)); err != nil {
		return task.ResultResponse{}, err
	}
	return task.AskResult(conn, taskID)
}

// saves the result on this node's row for the sent task
// with the other node's reason, e.g. "node restarted" or "no record of this task"
func (a *Node) recordSentResult(taskID string, resp task.ResultResponse) {
	if a.history == nil {
		return
	}
	if err := a.history.RecordTaskFinish(db.RoleSent, taskID, db.TaskStatus(resp.Status), resp.Output, resp.Error); err != nil && !errors.Is(err, db.ErrTaskNotFound) {
		log.Printf("task: could not record result of %q: %v", taskID, err)
	}
}
