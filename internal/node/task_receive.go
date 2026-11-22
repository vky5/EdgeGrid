package node

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
	"tailscale.com/client/tailscale/apitype"
)

// takes a task from a peer if allowed, runs it in the background, and saves the result for the sender to ask for
// the checks run in this order, and the first "no" is what the sender hears
//
//	alpha5-297b offers bravo...                     bravo answers
//	not allowed in acl-tasks                        "this node is not accepting tasks from you"
//	kind "resnet", not in tasks.json                "this node cannot run task kind \"resnet\""
//	no database open                                "this node can't record results"
//	another task still running                      "this node is already running a task"
//	kind "hello", all fine                          yes; a "received" row is saved as running, then the run starts
//
//	how the run then ends up in the row
//	hello prints "hello world\n"                   done, output "hello world\n"
//	fail exits 3                                   failed, "bare: [sh -c echo boom >&2; exit 3]: exit status 3 (stderr: \"boom\\n\")"
//	sleep is running when the node shuts down      lost, "bare: [sleep 30]: signal: killed (stderr: \"\")"
//	sender hangs up before hearing yes             failed, "verdict not delivered", and nothing runs
func (a *Node) receiveTask(
	who *apitype.WhoIsResponse,
	hello discovery.Hello,
	conn net.Conn,
) error {
	// who the peer is comes from Tailscale, never from the ID in its hello
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
		// with nowhere to save the result, the sender could never learn how it went, so refuse
		if a.history == nil {
			log.Printf("task: refused %s: no database to record the result", label)
			return errors.New("this node can't record results")
		}
		if !a.taskSlot.TryClaim() {
			log.Printf("task: refused %s: already running a task", label)
			return errors.New("this node is already running a task")
		}
		// saved before the verdict, so the row exists by the time the sender can ask
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
		// the verdict can fail after accept, so free the slot or it stays taken by a task that never runs
		if claimed != nil {
			a.taskSlot.Release()
			a.finishTask(claimed, db.TaskFailed, nil, "verdict not delivered", nil)
		}
		return err
	}
	a.pruneOldResults()

	a.runs.Go(func() {
		defer a.taskSlot.Release()
		a.runTask(t, label)
	})
	return nil
}

// runs an accepted task and saves how it ended, with any files it left for the sender
// the results folder is kept only for a done run that left files in it
//
//	train-mnist writes model.pt, prints "epoch 5 loss 0.03"   → done, output and [model.pt] saved, folder kept
//	hello prints "hello world", writes nothing                → done, no files, folder removed
//	fail exits 3 after writing half a model.pt                → failed, folder removed
//	node shuts down mid-run                                   → lost, folder removed
func (a *Node) runTask(t *task.Task, label string) {
	outDir, err := resultsDirFor(a.cfg.DataDir, t.ID)
	if err == nil {
		err = os.MkdirAll(outDir, 0o700)
	}
	if err != nil {
		log.Printf("task: %q (kind=%s) from %s: results folder: %v", t.ID, t.Kind, label, err)
		a.finishTask(t, db.TaskFailed, nil, "this node could not make a results folder", nil)
		return
	}

	out, err := a.executor.Run(a.runCtx, t.Kind, t.Input, outDir)
	if err != nil {
		os.RemoveAll(outDir)
		log.Printf("task: %q (kind=%s) from %s failed: %v", t.ID, t.Kind, label, err)
		status := db.TaskFailed
		if a.runCtx.Err() != nil {
			status = db.TaskLost
		}
		a.finishTask(t, status, nil, err.Error(), nil)
		return
	}

	files, err := listResultFiles(t.ID, outDir)
	if err != nil {
		os.RemoveAll(outDir)
		log.Printf("task: %q (kind=%s) from %s: %v", t.ID, t.Kind, label, err)
		a.finishTask(t, db.TaskFailed, nil, "could not read the result files: "+err.Error(), nil)
		return
	}
	if len(files) == 0 {
		os.RemoveAll(outDir)
	}
	log.Printf("task: %q (kind=%s) from %s finished: %q, %d file(s)", t.ID, t.Kind, label, out, len(files))
	a.finishTask(t, db.TaskDone, out, "", files)
}

// saves how a task ended
// if this fails the row shows running until the next restart marks it lost, so it is logged
func (a *Node) finishTask(t *task.Task, status db.TaskStatus, out []byte, errMsg string, files []db.TaskFile) {
	if err := a.history.RecordTaskFinish(db.RoleReceived, t.ID, status, out, errMsg, files); err != nil {
		log.Printf("task: %q: could not store its result, dispatcher will see it running: %v", t.ID, err)
	}
}
