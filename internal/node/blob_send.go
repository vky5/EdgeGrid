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

// sendBlobMediaType labels a file picked by a human — blob never
// interprets it.
const sendBlobMediaType = "application/octet-stream"

// blobSendTimeout bounds a whole transfer. The hello exchange clears its
// own deadline on return, so without this the connection has none.
const blobSendTimeout = 30 * time.Minute

// manifestFor returns m if it still describes the file at path, and builds a
// fresh one if not. Hashing is a full read of the file, so a caller that
// already has a manifest (the TUI builds one to show a preview) should pass it
// in rather than make SendBlob hash the same file a second time.
//
// The only staleness check is size: a file edited to a different length since
// it was hashed is caught here, and one edited in place to the same length is
// caught by the receiver, which verifies every chunk against the manifest and
// refuses a mismatch.
func manifestFor(path string, m *blob.Manifest) (*blob.Manifest, error) {
	if m != nil {
		if fi, err := os.Stat(path); err == nil && fi.Size() == m.Size {
			return m, nil
		}
	}
	return blob.BuildManifest(path, sendBlobMediaType, 0)
}

// SendBlob dials peer with IntentBlob and streams path to it: manifest
// first, then every chunk in order. m is an already-built manifest for path,
// or nil to have one built here.
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

	if err := conn.SetDeadline(time.Now().Add(blobSendTimeout)); err != nil {
		return err
	}

	log.Printf("blob: sending %s (%d bytes, %d chunks) to %s", path, m.Size, len(m.Chunks), peer.Hostname)

	t := a.transfers.start(Outbound, peer.Hostname)
	historyID, haveHistory := recordTransferStart(a.history, db.Outbound, peer.ID, peer.Hostname, m.Name, m.Size)

	began := time.Now()
	err = blob.Send(conn, path, m, t.progressFunc())
	a.transfers.finish(t, err)

	var refused *blob.RefusedError
	recordTransferFinish(a.history, historyID, haveHistory, err, errors.As(err, &refused), m.SHA256)

	elapsed := time.Since(began)
	if err == nil {
		// route says whether this went direct or through a relay — the
		// first thing to look at when the rate is low.
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
