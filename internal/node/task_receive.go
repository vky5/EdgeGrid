package node

import (
	"errors"
	"log"
	"net"

	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
	"tailscale.com/client/tailscale/apitype"
)

// receiveTask handles an inbound task offer: checks the ACL and slot, then
// (v1) runs a hardcoded no-op. Real isolation is still out of scope.
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

	accept := func(t *task.Task) error {
		if err := a.checkTaskAccept(stableID, label); err != nil {
			return err
		}
		if !a.taskSlot.TryClaim() {
			log.Printf("task: refused %s: already running a task", label)
			return errors.New("this node is already running a task")
		}
		return nil
	}

	log.Printf("task: inbound offer from %s", label)
	t, err := task.ProcessTask(conn, accept)
	if err != nil {
		return err
	}
	defer a.taskSlot.Release()

	log.Printf("task: ran %q (kind=%s) from %s", t.ID, t.Kind, label)
	return nil
}
