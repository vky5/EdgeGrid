package node

import (
	"errors"
	"log"
	"net"
	"time"

	"github.com/edgegrid/edgegrid/internal/blob"
	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/task"
	"tailscale.com/client/tailscale/apitype"
)

// how long the ask and the answer may take, 10s; both messages are small
// sending files after that gets more time, see resultFilesTimeout
const resultTimeout = 10 * time.Second

// the longest sending a task's files may take, 30 minutes, the same cap as any file transfer
// within that, a transfer that stops moving for 60s is dropped
const resultFilesTimeout = blobSendTimeout

// answers a peer asking how a task it sent us went, then sends the files it asks for
// who is asking comes from Tailscale, and only the node that sent the task gets an answer
func (a *Node) receiveResult(who *apitype.WhoIsResponse, conn net.Conn) {
	var requester string
	if who != nil && who.Node != nil {
		requester = string(who.Node.StableID)
	}
	began := time.Now()
	if err := conn.SetDeadline(began.Add(resultTimeout)); err != nil {
		return
	}
	lookup := func(taskID string) (task.ResultResponse, string) {
		resp := a.lookupResult(taskID, requester)
		if len(resp.Files) == 0 {
			return resp, ""
		}
		// the asker now has up to 60s to say which files it wants, and each chunk moved pushes that on
		refreshDeadline(conn, began, idleTimeout, resultFilesTimeout)
		dir, err := resultsDirFor(a.cfg.DataDir, taskID)
		if err != nil {
			log.Printf("task: %s: results folder: %v", taskID, err)
		}
		return resp, dir
	}
	progress := func(blob.Progress) { refreshDeadline(conn, began, idleTimeout, resultFilesTimeout) }
	if err := task.ServeResult(conn, lookup, progress); err != nil {
		log.Printf("task: result request failed: %v", err)
	}
}

// turns a saved task row into the answer sent back
// a missing task and someone else's task both get {"status":"lost","error":"no record of this task"}
// so a peer can't guess IDs
//
// on bravo; task 3f2b8c1e-… was sent by alpha5-297b (nK7xQ2CNTRL)
//	lookupResult("3f2b8c1e-…", "nK7xQ2CNTRL")   → {Status: "running"} while it runs
//	lookupResult("3f2b8c1e-…", "nK7xQ2CNTRL")   → {Status: "done", Output: "hello world\n"} after
//	lookupResult("a7d40e19-…", "nK7xQ2CNTRL")   → {Status: "done", Output: "epoch 10 loss 0.021\n",
//	                                               Files: [{model.pt 10485760 9f2c41…}]} for a run that left a file
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
	resp := task.ResultResponse{
		Status: task.ResultStatus(rec.Status),
		Output: rec.Output,
		Error:  rec.Error,
	}
	for _, f := range rec.Files {
		resp.Files = append(resp.Files, task.ResultFile{Name: f.Name, Size: f.Size, SHA256: f.SHA256})
	}
	return resp
}
