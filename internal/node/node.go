package node

import (
	"context"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/edgegrid/edgegrid/internal/db"
	"github.com/edgegrid/edgegrid/internal/discovery"
	"tailscale.com/client/local"
	"tailscale.com/tsnet"
)

// historyFile is this node's local SQLite database, relative to DataDir.
const historyFile = "edgegrid.db"

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
//
// The package is split by concern; this file is only the core (construction,
// tsnet access, Start, Close). Everything else lives beside it:
//
//	peer.go          the hello handshake: selfHello, handlePeer (routes a
//	                 connection by its Intent), dialAndGreet
//	blob_send.go     SendBlob: dial a peer and stream a file to it
//	blob_receive.go  receiveBlob: take an inbound file into the inbox
//	acl.go           who may send this node a file: the ACL, the size cap,
//	                 and checkAccept, the policy applied to every offer
//	transfers.go     the registry of in-flight transfers the TUI polls
//	config.go, ident.go, profile.go, log.go   profiles, identity, token files
type Node struct {
	cfg         *Config
	tsnetServer *tsnet.Server
	tailscaleIP string
	nodeID      string

	closeOnce sync.Once
	transfers registry

	// history is this node's local transfer database, nil if it never opened.
	history *db.Store
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

	history, err := db.Open(filepath.Join(cfg.DataDir, historyFile))
	if err != nil {
		// Best-effort: a node that can't record history should still transfer files
		log.Printf("warning: could not open history database, transfers will not be recorded: %v", err)
	} else if err := history.ReconcileStaleTransfers(); err != nil { // marks stale in-progress rows interrupted after a restart
		log.Printf("warning: could not reconcile stale transfer records: %v", err)
	}

	return &Node{
		cfg:         cfg,
		tsnetServer: ts,
		tailscaleIP: ip4.String(),
		nodeID:      ident.NodeID,
		history:     history,
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
		if a.history != nil {
			if err := a.history.Close(); err != nil {
				log.Printf("closing history database: %v", err)
			}
		}
	})
}
