package node

import (
	"errors"
	"fmt"
	"log"
	"net"

	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
	"tailscale.com/client/tailscale/apitype"
)

// receiveTask handles an inbound task offer: ACL, kind, slot, then runs it in
// the background. The result is only logged until result delivery exists.
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

	claimed := false
	accept := func(t *task.Task) error {
		if err := a.checkTaskAccept(stableID, label); err != nil {
			return err
		}
		if !a.executor.Supports(t.Kind) {
			log.Printf("task: refused %s: kind %q not runnable here", label, t.Kind)
			return fmt.Errorf("this node cannot run task kind %q", t.Kind)
		}
		if !a.taskSlot.TryClaim() {
			log.Printf("task: refused %s: already running a task", label)
			return errors.New("this node is already running a task")
		}
		claimed = true
		return nil
	}

	log.Printf("task: inbound offer from %s", label)
	t, err := task.ProcessTask(conn, accept)
	if err != nil {
		// accept can succeed and the verdict write still fail — the slot
		// must not stay claimed for a task that will never run.
		if claimed {
			a.taskSlot.Release()
		}
		return err
	}

	a.runs.Go(func() {
		defer a.taskSlot.Release()
		out, err := a.executor.Run(a.runCtx, t.Kind, t.Input)
		if err != nil {
			log.Printf("task: %q (kind=%s) from %s failed: %v", t.ID, t.Kind, label, err)
			return
		}
		log.Printf("task: %q (kind=%s) from %s finished: %q", t.ID, t.Kind, label, out)
	})
	return nil
}
