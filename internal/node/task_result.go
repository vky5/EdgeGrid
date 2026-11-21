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

// how long one ask for a result may take, 10s; both messages are tiny
const resultTimeout = 10 * time.Second

// answers a peer asking how a task it sent us went
// who is asking comes from Tailscale, and only the node that sent the task gets an answer
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

// turns a saved task row into the answer sent back
// a missing task and someone else's task both get {"status":"lost","error":"no record of this task"}
// so a peer can't guess IDs
//
//	// on bravo; task 3f2b8c1e-… was sent by alpha5-297b (nK7xQ2CNTRL)
//	lookupResult("3f2b8c1e-…", "nK7xQ2CNTRL")   → {Status: "running"} while it runs
//	lookupResult("3f2b8c1e-…", "nK7xQ2CNTRL")   → {Status: "done", Output: "hello world\n"} after
//	lookupResult("3f2b8c1e-…", "nT9wL4CNTRL")   → {Status: "lost", Error: "no record of this task"}, not theirs
//	lookupResult("3f2b8c1e-…", "")              → the same lost answer: Tailscale didn't say who is asking
//	lookupResult(any ID, any peer), no database   → the same lost answer
func (a *Node) lookupResult(taskID, requester string) task.ResultResponse {
	unknown := task.ResultResponse{Status: task.StatusLost, Error: task.NoRecord}
	if a.history == nil || requester == "" {
		return unknown
	}
	rec, err := a.history.ReceivedTaskFor(taskID, requester) // takes requester as well for security check that the node that is asking for the result is same with which submitted the query
	if err != nil {
		if !errors.Is(err, db.ErrTaskNotFound) {
			log.Printf("task: look up result %q: %v", taskID, err)
		}
		return unknown
	}
	return task.ResultResponse{
		Status: task.ResultStatus(rec.Status),
		Output: rec.Output,
		Error:  rec.Error,
	}
}
