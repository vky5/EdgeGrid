package node

import (
	"errors"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
	"tailscale.com/client/tailscale/apitype"
)

// taskRetention is how long finished task rows are kept for the dispatcher
// to collect.
const taskRetention = 7 * 24 * time.Hour

// receiveTask handles an inbound task offer: ACL, kind, slot, then runs it in
// the background. The outcome is stored for the dispatcher to ask for later
// (see receiveResult).
func (a *Node) receiveTask(
	who *apitype.WhoIsResponse,
	hello discovery.Hello,
	conn net.Conn,
) error {
	// Identity for the ACL is what Tailscale attributes, never
	// hello.NodeID — that's the peer's own claim.
	var stableID, label string
	if who != nil && who.Node != nil {
		stableID = string(who.Node.StableID)
		label = who.Node.ComputedName
	}
	if label == "" {
		label = hello.NodeID
	}

	var claimed *task.Task // set once accept has claimed the slot and recorded the row
	accept := func(t *task.Task) error {
		if err := a.checkTaskAccept(stableID, label); err != nil {
			return err
		}
		if !a.executor.Supports(t.Kind) {
			log.Printf("task: refused %s: kind %q not runnable here", label, t.Kind)
			return fmt.Errorf("this node cannot run task kind %q", t.Kind)
		}
		// Without a store the result could never be reported, so a task
		// accepted here would fail silently.
		if a.history == nil {
			log.Printf("task: refused %s: no database to record the result", label)
			return errors.New("this node can't record results")
		}
		if !a.taskSlot.TryClaim() {
			log.Printf("task: refused %s: already running a task", label)
			return errors.New("this node is already running a task")
		}
		// Recorded before the verdict goes out: the dispatcher can only ask
		// for the result after it has the verdict, so the row always exists.
		if err := a.history.RecordTaskStart(db.RoleReceived, t.ID, stableID, label, t.Kind); err != nil {
			a.taskSlot.Release()
			log.Printf("task: refused %s: %v", label, err)
			return errors.New("this node can't record results")
		}
		claimed = t
		return nil
	}

	log.Printf("task: inbound offer from %s", label)
	t, err := task.ProcessTask(conn, accept)
	if err != nil {
		// accept can succeed and the verdict write still fail — the slot
		// must not stay claimed for a task that will never run.
		if claimed != nil {
			a.taskSlot.Release()
			a.finishTask(claimed, db.TaskFailed, nil, "verdict not delivered")
		}
		return err
	}
	if err := a.history.PruneTasks(time.Now().Add(-taskRetention)); err != nil {
		log.Printf("task: prune old results: %v", err)
	}

	a.runs.Go(func() {
		defer a.taskSlot.Release()
		out, err := a.executor.Run(a.runCtx, t.Kind, t.Input)
		if err != nil {
			log.Printf("task: %q (kind=%s) from %s failed: %v", t.ID, t.Kind, label, err)
			status := db.TaskFailed
			if a.runCtx.Err() != nil {
				status = db.TaskInterrupted
			}
			a.finishTask(t, status, nil, err.Error())
			return
		}
		log.Printf("task: %q (kind=%s) from %s finished: %q", t.ID, t.Kind, label, out)
		a.finishTask(t, db.TaskDone, out, "")
	})
	return nil
}

// finishTask stores the outcome. If this write fails the row stays running
// until the next restart marks it interrupted, so say so loudly.
func (a *Node) finishTask(t *task.Task, status db.TaskStatus, out []byte, errMsg string) {
	if err := a.history.RecordTaskFinish(db.RoleReceived, t.ID, status, out, errMsg); err != nil {
		log.Printf("task: %q: could not store its result, dispatcher will see it running: %v", t.ID, err)
	}
}
