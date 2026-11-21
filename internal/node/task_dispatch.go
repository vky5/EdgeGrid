package node

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"log"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
)

// offers a task to each peer in turn and returns the first that accepts, so the caller knows whom to ask for the result
// it tries every peer it is given, online or not; callers drop offline ones first
//
//	peers = []discovery.Peer{alpha5-a720, bravo}, task kind "hello"
//	alpha5-a720 says busy, bravo says yes   → bravo, nil; a "sent" row is saved for bravo
//
//	both say no → discovery.Peer{}, and an error of one line per peer:
//		alpha5-a720: peer refused the transfer: this node is already running a task
//		bravo: peer refused the transfer: this node cannot run task kind "hello"
//		no peer accepted the task
func (a *Node) DispatchTask(ctx context.Context, peers []discovery.Peer, t *task.Task) (discovery.Peer, error) {
	var errs []error

	for _, peer := range peers {
		dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		conn, err := a.Dial(dialCtx, "tcp", net.JoinHostPort(peer.IP.String(), strconv.Itoa(discovery.Port)))
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", peer.Hostname, err))
			continue
		}

		if _, err := discovery.ExchangeAsDialer(conn, a.selfHello(discovery.IntentTask), 10*time.Second); err != nil {
			conn.Close()
			errs = append(errs, fmt.Errorf("%s: %w", peer.Hostname, err))
			continue
		}

		err = task.Offer(conn, t)
		conn.Close()

		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", peer.Hostname, err))
			continue
		}

		a.recordSentTask(t, peer)
		return peer, nil // this peer accepted — dispatch is done
	}

	errs = append(errs, errors.New("no peer accepted the task"))
	return discovery.Peer{}, errors.Join(errs...)
}

// saves that the task was sent, so its result can be matched to it later
// without a database the task still runs, it just isn't tracked here
func (a *Node) recordSentTask(t *task.Task, peer discovery.Peer) {
	if a.history == nil {
		return
	}
	if err := a.history.RecordTaskStart(db.RoleSent, t.ID, peer.ID, peer.Hostname, t.Kind); err != nil {
		log.Printf("task: could not record dispatch of %q: %v", t.ID, err)
	}
}
