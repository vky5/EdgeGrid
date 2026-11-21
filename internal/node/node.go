// Package node is one EdgeGrid node: it joins the tailnet, talks to peers,
// and sends and receives files and tasks.
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
	"github.com/edgegrid/edgegrid/internal/executor"
	"tailscale.com/client/local"
	"tailscale.com/tsnet"
)

// this node's SQLite database file, inside the data folder
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

// one running EdgeGrid node: its tailnet connection, database and task runner
type Node struct {
	cfg         *Config
	tsnetServer *tsnet.Server
	tailscaleIP string
	nodeID      string

	closeOnce sync.Once
	transfers registry
	taskSlot  TaskSlot

	executor *executor.Executor
	runCtx   context.Context // cancelled by Close, which kills any running task
	stopRuns context.CancelFunc
	runs     sync.WaitGroup // Close waits on this so no task outlives the node

	// the local database of transfers and tasks, nil if it never opened
	history *db.Store
}

// this node's Tailscale IPv4 address, or "" if tsnet is not running
func (a *Node) TailscaleIP() string { return a.tailscaleIP }

// this node's own ID, the same across restarts
func (a *Node) NodeID() string { return a.nodeID }

// the name this node shows on the tailnet, in peers' Peers tabs and the Tailscale admin console
func (a *Node) TailscaleHostname() string { return a.cfg.TailscaleHostname }

// the Tailscale client other packages use to list peers and ask who is calling
func (a *Node) LocalClient() (*local.Client, error) {
	return a.tsnetServer.LocalClient()
}

// opens a port only other tailnet members can reach, e.g. the peer port 9797
// it is never open to the public internet
func (a *Node) Listen(network, addr string) (net.Listener, error) {
	return a.tsnetServer.Listen(network, addr)
}

// connects to another node through tsnet
// a plain net.Dial would leave by the normal route and hang until TCP gives up
func (a *Node) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	return a.tsnetServer.Dial(ctx, network, addr)
}

// how long to try reaching a peer before giving up
// so a dial to an offline node doesn't wait for the full TCP timeout
const dialTimeout = 15 * time.Second

// joins the tailnet and builds the Node
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
		// a node that can't record history should still send and receive files
		log.Printf("warning: could not open history database, transfers will not be recorded: %v", err)
	} else {
		// mark what a crash left half-done: transfers as interrupted, tasks as lost
		// two separate steps, so one failing doesn't skip the other
		if err := history.ReconcileStaleTransfers(); err != nil {
			log.Printf("warning: could not reconcile stale transfer records: %v", err)
		}
		if err := history.ReconcileStaleTasks(); err != nil {
			log.Printf("warning: could not reconcile stale task records: %v", err)
		}
	}

	runCtx, stopRuns := context.WithCancel(context.Background())
	return &Node{
		cfg:         cfg,
		tsnetServer: ts,
		tailscaleIP: ip4.String(),
		nodeID:      ident.NodeID,
		history:     history,
		executor:    loadExecutor(cfg.DataDir),
		runCtx:      runCtx,
		stopRuns:    stopRuns,
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

	// greet each online peer once at startup, not on a timer
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

	a.startTaskChecks(ctx)

	log.Println("starting EdgeGrid services")
	<-ctx.Done()
	return nil
}

// shuts the node down; calling it again does nothing
// so the background runner and the caller's defer can both call it
func (a *Node) Close() {
	a.closeOnce.Do(func() {
		log.Println("shutting down EdgeGrid services")
		a.stopRuns()
		a.runs.Wait()
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
