package node

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/edgegrid/edgegrid/internal/blob"
	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
)

// the type sent with a file someone picked by hand; blob never reads it
const sendBlobMediaType = "application/octet-stream"

// the longest any outgoing transfer may run, 30 minutes, even while chunks keep moving
const blobSendTimeout = 30 * time.Minute

// reuses the given manifest if the file is still the same size, and hashes the file again if not
// e.g. the TUI already hashed it for the preview, so it isn't read twice
// a same-size edit is still caught by the receiver, which checks every chunk
func manifestFor(path string, m *blob.Manifest) (*blob.Manifest, error) {
	if m != nil {
		if fi, err := os.Stat(path); err == nil && fi.Size() == m.Size {
			return m, nil
		}
	}
	return blob.BuildManifest(path, sendBlobMediaType, 0)
}

// sends a file to a peer: the manifest first, then every chunk in order
// pass the manifest if you already built one, or nil to build it here
func (a *Node) SendBlob(ctx context.Context, peer discovery.Peer, path string, m *blob.Manifest) error {
	m, err := manifestFor(path, m)
	if err != nil {
		return err
	}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := a.Dial(dialCtx, "tcp", net.JoinHostPort(peer.IP.String(), strconv.Itoa(discovery.Port)))
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := discovery.ExchangeAsDialer(conn, a.selfHello(discovery.IntentBlob), 10*time.Second); err != nil {
		return err
	}

	began := time.Now()
	if err := conn.SetDeadline(began.Add(verdictTimeout)); err != nil {
		return err
	}

	log.Printf("blob: sending %s (%d bytes, %d chunks) to %s", path, m.Size, len(m.Chunks), peer.Hostname)

	t := a.transfers.start(Outbound, peer.Hostname)
	historyID, haveHistory := recordTransferStart(a.history, db.Outbound, peer.ID, peer.Hostname, m.Name, m.Size)

	report := t.progressFunc()
	progress := func(p blob.Progress) {
		refreshDeadline(conn, began, idleTimeout, blobSendTimeout)
		report(p)
	}

	err = blob.Send(conn, path, m, progress)
	a.transfers.finish(t, err)

	var refused *blob.RefusedError
	recordTransferFinish(a.history, historyID, haveHistory, err, errors.As(err, &refused), m.SHA256)

	elapsed := time.Since(began)
	if err == nil {
		// route says direct or relay, the first thing to check when the speed is low
		log.Printf("blob: sent %s to %s: %d bytes in %s (%.1f MB/s), route=%s",
			path, peer.Hostname, m.Size, elapsed.Round(time.Millisecond),
			rateMBps(m.Size, elapsed), routeLabel(peer))
	}
	return err
}

func routeLabel(p discovery.Peer) string {
	if r := p.Route(); r != "" {
		return r
	}
	return "unknown"
}
