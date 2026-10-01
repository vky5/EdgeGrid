package node

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/edgegrid/edgegrid/internal/discovery"
	"github.com/edgegrid/edgegrid/internal/task"
)

// DispatchTask tries each peer in order and stops at the first one that
// accepts. A peer that's unreachable, refuses, or errors just gets skipped.
func (a *Node) DispatchTask(ctx context.Context, peers []discovery.Peer, t *task.Task) error {
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

		return nil // this peer accepted — dispatch is done
	}

	errs = append(errs, errors.New("no peer accepted the task"))
	return errors.Join(errs...)
}
