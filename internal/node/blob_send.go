package node

import (
	"context"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/edgegrid/edgegrid/internal/blob"
	"github.com/edgegrid/edgegrid/internal/discovery"
)

// sendBlobMediaType labels a file picked by a human — blob never
// interprets it.
const sendBlobMediaType = "application/octet-stream"

// blobSendTimeout bounds a whole transfer. The hello exchange clears its
// own deadline on return, so without this the connection has none.
const blobSendTimeout = 30 * time.Minute

// SendBlob dials peer with IntentBlob and streams path to it: manifest
// first, then every chunk in order.
func (a *Node) SendBlob(ctx context.Context, peer discovery.Peer, path string) error {
	m, err := blob.BuildManifest(path, sendBlobMediaType, 0)
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
	err = blob.Send(conn, path, m, t.progressFunc())
	a.transfers.finish(t, err)
	return err
}
