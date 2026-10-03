package node

import (
	"errors"
	"log"
	"net"
	"time"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/task"
	"tailscale.com/client/tailscale/apitype"
)

// resultTimeout bounds one result exchange; both messages are tiny.
const resultTimeout = 10 * time.Second

// receiveResult answers a dispatcher asking how a task it sent us went. The
// requester is identified by Tailscale, never by anything it says, and only
// the dispatcher recorded for the task gets an answer.
func (a *Node) receiveResult(who *apitype.WhoIsResponse, conn net.Conn) {
	var requester string
	if who != nil && who.Node != nil {
		requester = string(who.Node.StableID)
	}
	if err := conn.SetDeadline(time.Now().Add(resultTimeout)); err != nil {
		return
	}
	err := task.ServeResult(conn, func(taskID string) task.ResultResponse {
		return a.lookupResult(taskID, requester)
	})
	if err != nil {
		log.Printf("task: result request failed: %v", err)
	}
}

// lookupResult maps a stored row to the wire answer. Missing, someone else's,
// and unreadable all read as unknown: a peer can't tell them apart.
func (a *Node) lookupResult(taskID, requester string) task.ResultResponse {
	unknown := task.ResultResponse{Status: task.StatusUnknown}
	if a.history == nil || requester == "" {
		return unknown
	}
	rec, err := a.history.ReceivedTaskFor(taskID, requester)
	if err != nil {
		if !errors.Is(err, db.ErrTaskNotFound) {
			log.Printf("task: look up result %q: %v", taskID, err)
		}
		return unknown
	}
	return task.ResultResponse{Status: task.ResultStatus(rec.Status), Output: rec.Output, Error: rec.Error}
}
