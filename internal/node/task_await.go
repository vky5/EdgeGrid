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

// pollPlan is how AwaitResult paces itself: first wait, doubling up to max,
// until giveUp has passed. A var so tests can shrink it.
type pollPlan struct {
	first, max, giveUp time.Duration
}

var defaultPollPlan = pollPlan{
	first: time.Second, 
	max: 10 * time.Second, 
	giveUp: 10 * time.Minute,
}

// ErrAwaitTimeout means the polling window ended with the task still running
// or the peer unreachable. The task itself is not cancelled, and the peer
// keeps its result for later.
var ErrAwaitTimeout = errors.New("gave up waiting for the task result")

// AwaitResult polls peer until taskID has finished, then stores the outcome on
// this node's sent row and returns it. A failed poll (peer offline, network
// blip) is retried, not treated as the task failing.
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

// awaitResult is the polling loop, separate from dialing so it can be tested.
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

// askResult is one poll: dial, hello with the result intent, ask, hang up.
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

// recordSentResult stores the finished outcome on the sent row. Unknown means
// the peer lost the task (pruned, or its database was wiped), so the sender
// records it as failed rather than leaving it running forever.
func (a *Node) recordSentResult(taskID string, resp task.ResultResponse) {
	if a.history == nil {
		return
	}
	status, errMsg := db.TaskStatus(resp.Status), resp.Error
	if resp.Status == task.StatusUnknown {
		status, errMsg = db.TaskFailed, resp.Err().Error()
	}
	if err := a.history.RecordTaskFinish(db.RoleSent, taskID, status, resp.Output, errMsg); err != nil && !errors.Is(err, db.ErrTaskNotFound) {
		log.Printf("task: could not record result of %q: %v", taskID, err)
	}
}
