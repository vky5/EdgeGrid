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

// what this node says about itself when it connects, with what it wants
func (a *Node) selfHello(intent discovery.IntentType) discovery.Hello {
	return discovery.Hello{NodeID: a.NodeID(), Intent: intent}
}

// hands a connection to the right handler for what the peer wants, e.g. send a file
func (a *Node) handlePeer(who *apitype.WhoIsResponse, hello discovery.Hello, conn net.Conn) {
	defer conn.Close() // close TCP connection after anything

	switch hello.Intent {
	case discovery.IntentBlob:
		a.receiveBlob(who, hello, conn)
	case discovery.IntentTask:
		a.receiveTask(who, hello, conn)
	case discovery.IntentResult:
		a.receiveResult(who, conn)

	default:
		// a plain hello, or a peer too old to send what it wants
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
