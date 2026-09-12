package node

import (
	"context"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"sync"

	"tailscale.com/client/local"
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
	})
}
