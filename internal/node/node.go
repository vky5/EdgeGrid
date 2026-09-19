package node

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/edgegrid/edgegrid/internal/blob"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tsnet"
)

func NewWithLogging(
	ctx context.Context,
	cfg *Config,
	onProgress func(string),
	tuiMode bool,
) (*Node, func() error, error) {
	closeLog, err := SetupLog(cfg.DataDir, tuiMode)
	if err != nil {
		log.Printf("warning: could not open log file, logging to stdout only: %v", err)
		closeLog = func() error { return nil }
	}

	nodeAgent, err := New(ctx, cfg, onProgress)
	if err != nil {
		return nil, closeLog, err
	}
	return nodeAgent, closeLog, nil
}

// Entire lifecycle of the application
type Node struct {
	cfg         *Config
	tsnetServer *tsnet.Server
	tailscaleIP string
	nodeID      string

	closeOnce sync.Once
}

// returns tailscale IP address of this node or "" if tsnet is not running
func (a *Node) TailscaleIP() string { return a.tailscaleIP }

// NodeID is this node's persistent identity (see nodeident).
func (a *Node) NodeID() string { return a.nodeID }

// TailscaleHostname is the hostname this node presents on the tailnet — the
// name that shows up in peers' Peers tabs and the Tailscale admin console.
func (a *Node) TailscaleHostname() string { return a.cfg.TailscaleHostname }

// LocalClient exposes tsnet's local API — Status() for membership/liveness,
// WhoIs() for attributing an inbound connection to a tailnet peer (used by
// internal/discovery). Callers outside this package go through this instead
// of reaching into tsnetServer directly, since that field is unexported.
func (a *Node) LocalClient() (*local.Client, error) {
	return a.tsnetServer.LocalClient()
}

// Listen opens a listener reachable only from other tailnet members — see
// internal/discovery, which uses this for the peer-discovery port. Traffic
// stays inside the tailnet; tsnet never exposes it to the public internet.
func (a *Node) Listen(network, addr string) (net.Listener, error) {
	return a.tsnetServer.Listen(network, addr)
}

// Dial opens a connection to another tailnet member through tsnet's own
// userspace stack. This must not be net.Dial: tsnet has no TUN device, so
// tailnet IPs are not in the kernel routing table and a plain dial leaves
// via the default route into CGNAT space, where it hangs until TCP times
// out.
func (a *Node) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return a.tsnetServer.Dial(ctx, network, addr)
}

// dialTimeout bounds reaching a peer. Without it a dial to an unreachable
// tailnet IP blocks for the kernel's full TCP timeout.
const dialTimeout = 15 * time.Second

// Build the Node struct and authenticate tsnet
func New(ctx context.Context, cfg *Config, onProgress func(string)) (*Node, error) {
	ts := &tsnet.Server{
		Dir:      filepath.Join(cfg.DataDir, "tsnet"),
		Hostname: cfg.TailscaleHostname,
		AuthKey:  cfg.TailscaleAuthKey,
		Logf: func(format string, args ...any) {
			log.Printf(format, args...)
		},
	}
	if onProgress != nil {
		ts.UserLogf = func(format string, args ...any) {
			line := fmt.Sprintf(format, args...)
			log.Print(line)
			onProgress(line)
		}
	}
	status, err := ts.Up(ctx) // for auth/login (if no AuthKey passed, through url)
	if err != nil {
		return nil, fmt.Errorf("tsnet up: %w", err)
	}
	ip4, ip6 := ts.TailscaleIPs() // tailscale ips for this node
	tsnetUpLine := fmt.Sprintf("tsnet up: hostname=%s ip4=%s ip6=%s backend=%s", cfg.TailscaleHostname, ip4, ip6, status.BackendState)
	log.Print(tsnetUpLine)
	if onProgress != nil {
		onProgress(tsnetUpLine) // not a ts.UserLogf() need to send ourself
	}

	// Persist tailscale IP for this node
	if ip4.IsValid() {
		if err := SaveToken(cfg.DataDir, "tailscale.ip", ip4.String()); err != nil {
			log.Printf("warning: could not save tailscale ip: %v", err)
		}
	}

	// Load or generate persistent node identity.
	ident, err := LoadOrCreateIdentity(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("node identity: %w", err)
	}

	return &Node{
		cfg:         cfg,
		tsnetServer: ts,
		tailscaleIP: ip4.String(),
		nodeID:      ident.NodeID,
	}, nil
}

func (a *Node) Start(ctx context.Context) error {
	lc, err := a.LocalClient() // tailscale information
	if err != nil {
		return fmt.Errorf("local client: %w", err)
	}
	ln, err := a.Listen("tcp", fmt.Sprintf(":%d", discovery.Port))
	if err != nil {
		return fmt.Errorf("discovery listen: %w", err)
	}

	server := discovery.NewServer(ln, lc, a.selfHello(discovery.IntentHello))
	server.Logf = log.Printf
	server.OnPeer = a.handlePeer

	go func() {
		if err := server.Serve(ctx); err != nil {
			log.Printf("discovery: serve: %v", err)
		}
	}()

	peers, err := discovery.Snapshot(ctx, lc)
	if err != nil {
		log.Printf("discovery: snapshot: %v", err)
	}

	// This greeting is not periodic and it is for inital handshake and record keeping
	for _, p := range peers {
		if !p.Online {
			continue
		}
		go func(p discovery.Peer) {
			if err := a.dialAndGreet(ctx, p, a.selfHello(discovery.IntentHello)); err != nil {
				log.Printf("discovery: dial %s: %v", p.Hostname, err)
			}
		}(p)
	}

	log.Println("starting EdgeGrid services")
	<-ctx.Done()
	return nil
}

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
		a.receiveBlob(hello, conn)
	default:
		// IntentHello, empty (a peer older than the field)
		// TODO record the peer somewhere (could be store or memory)
	}
}

// blobReceiveTimeout bounds a whole inbound transfer. The hello exchange
// clears its own deadline on return, so without this a peer could claim
// IntentBlob and then hold the connection open forever.
const blobReceiveTimeout = 30 * time.Minute

// receiveBlob reads a manifest and its chunks into the profile's inbox.
// blob.Receive verifies every chunk against the manifest before writing.
func (a *Node) receiveBlob(hello discovery.Hello, conn net.Conn) {
	if err := conn.SetDeadline(time.Now().Add(blobReceiveTimeout)); err != nil {
		log.Printf("blob: set deadline: %v", err)
		return
	}

	dir := filepath.Join(a.cfg.DataDir, "inbox")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("blob: inbox %s: %v", dir, err)
		return
	}

	// The filename carries no peer-supplied string on purpose — NodeID is
	// self-reported, so letting it into a path invites ../ escapes.
	dest := filepath.Join(dir, fmt.Sprintf("%d.blob", time.Now().UnixNano()))

	log.Printf("blob: receiving from %s into %s", hello.NodeID, dest)
	m, err := blob.Receive(conn, dest)
	if err != nil {
		log.Printf("blob: receive from %s failed: %v", hello.NodeID, err)
		if rmErr := os.Remove(dest); rmErr != nil {
			log.Printf("blob: could not remove partial %s: %v", dest, rmErr)
		}
		return
	}

	log.Printf("blob: received %d bytes in %d chunks from %s -> %s (sha256 %s)",
		m.Size, len(m.Chunks), hello.NodeID, dest, m.SHA256)
}

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
	return blob.Send(conn, path, m)
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

// Close shuts the agent down. It is intentionally idempotent because more than
// one path can legitimately ask the same agent to close — the background runner
// noticing Start returned, and the caller's own deferred cleanup. Only the first
// call should release tsnet, so later calls are no-ops and we never double-close
// the same handle.
func (a *Node) Close() {
	a.closeOnce.Do(func() {
		log.Println("shutting down EdgeGrid services")
		if a.tsnetServer != nil {
			if err := a.tsnetServer.Close(); err != nil {
				log.Printf("closing tsnet server: %v", err)
			}
		}
	})
}
