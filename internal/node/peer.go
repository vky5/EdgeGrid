package node

import (
	"context"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/edgegrid/edgegrid/internal/discovery"
	"tailscale.com/client/tailscale/apitype"
)

// selfHello is what this node says about itself on a connection
func (a *Node) selfHello(intent discovery.IntentType) discovery.Hello {
	return discovery.Hello{NodeID: a.NodeID(), Intent: intent}
}

// handlePeer routes an identified, greeted connection by what the peer said
// it wanted. The dispatch lives here rather than in discovery
func (a *Node) handlePeer(who *apitype.WhoIsResponse, hello discovery.Hello, conn net.Conn) {
	defer conn.Close() // close TCP connection after anything

	switch hello.Intent {
	case discovery.IntentBlob:
		a.receiveBlob(who, hello, conn)
	default:
		// IntentHello, empty (a peer older than the field)
		// TODO record the peer somewhere (could be store or memory)
	}
}

func (a *Node) dialAndGreet(ctx context.Context, peer discovery.Peer, msg discovery.Hello) error {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := a.Dial(dialCtx, "tcp", net.JoinHostPort(peer.IP.String(), strconv.Itoa(discovery.Port)))
	if err != nil {
		return err
	}

	defer conn.Close()

	var rnMsg discovery.Hello

	rnMsg, err = discovery.ExchangeAsDialer(conn, msg, 10*time.Second)
	if err != nil {
		return err
	}

	log.Printf("discovery: %s self-reports node_id=%s", peer.Hostname, rnMsg.NodeID)

	return nil
}
