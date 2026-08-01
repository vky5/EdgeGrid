package agent

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"

	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/coordinator"
	"github.com/edgegrid/edgegrid/internal/natsserver"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"github.com/edgegrid/edgegrid/internal/nodelog"
	"github.com/edgegrid/edgegrid/internal/worker"
	"github.com/nats-io/nats.go"
	"tailscale.com/tsnet"
)

func NewAgentWithLogging(
	ctx context.Context,
	cfg *config.Config,
	onProgress func(string),
	tuiMode bool,
) (*Agent, func() error, error) {
	closeLog, err := nodelog.Setup(cfg.DataDir, tuiMode)
	if err != nil {
		log.Printf("warning: could not open log file, logging to stdout only: %v", err)
		closeLog = func() error { return nil }
	}

	nodeAgent, err := NewAgent(ctx, cfg, onProgress)
	if err != nil {
		return nil, closeLog, err
	}
	return nodeAgent, closeLog, nil 
}

// Entire lifecycle of the application
type Agent struct {
	cfg         *config.Config
	tsnetServer *tsnet.Server
	natsConn    *nats.Conn
	natsServer  *natsserver.EmbeddedServer
	coordinator *coordinator.Coordinator
	worker      *worker.Worker

	tailscaleIP string
	nodeID      string

	closeOnce sync.Once
}

// returns tailscale IP address of this node or "" if tsnet is not running
func (a *Agent) TailscaleIP() string { return a.tailscaleIP }

// NodeID is this node's persistent identity (see nodeident).
func (a *Agent) NodeID() string { return a.nodeID }

// AdminToken is the bearer token guarding this node's admin HTTP endpoint (not for node without coordinator)
func (a *Agent) AdminToken() string {
	if a.coordinator == nil {
		return ""
	}
	return a.coordinator.AdminToken()
}

// WorkerSnap is an in-process snapshot for the node Overview TUI.
type WorkerSnap struct {
	Up       bool
	Busy     bool
	Active   []string // job ids of currently active jobs
	DoneOK   int
	DoneFail int
	Recent   []worker.FinishedJob // session-local, newest last
}

// create a snapshot of worker state
func (a *Agent) WorkerRuntime() WorkerSnap {
	if a == nil || a.worker == nil {
		return WorkerSnap{}
	}
	ok, fail, recent := a.worker.SessionStats()
	return WorkerSnap{
		Up:       true,
		Busy:     a.worker.IsBusy(),
		Active:   a.worker.ActiveJobIDs(),
		DoneOK:   ok,
		DoneFail: fail,
		Recent:   recent,
	}
}

// Build the Agent struct and authenticate tsnet
func NewAgent(ctx context.Context, cfg *config.Config, onProgress func(string)) (*Agent, error) {
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

	if cfg.AdvertiseHost == "" && ip4.IsValid() {
		cfg.AdvertiseHost = ip4.String()
	}
	
	// Persist tailscale IP for this node
	if ip4.IsValid() {
		if err := nodeident.SaveToken(cfg.DataDir, "tailscale.ip", ip4.String()); err != nil {
			log.Printf("warning: could not save tailscale ip: %v", err)
		}
	}

	// Load or generate persistent node identity.
	ident, err := nodeident.LoadOrCreate(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("node identity: %w", err)
	}

	natsCred, clusterSecret, clusterRoutes, err := resolveNATSCredential(ts, cfg, ident)
	if err != nil {
		return nil, err
	}

	var embeddedNATS *natsserver.EmbeddedServer
	if cfg.EmbedNATS {
		if len(clusterRoutes) > 0 {
			clusterRoutes = bridgeOutboundRoutes(ctx, ts, clusterRoutes)
		}
		
		embeddedNATS, err = startEmbeddedNATS(cfg, natsCred, clusterSecret, clusterRoutes)
		if err != nil {
			return nil, err
		}
		// Unconditional: joining nodes need this even with zero peer
		// coordinators — nats-server's own bind is invisible to tsnet.
		bridgeInboundPort(ts, cfg.NATSPort)
		if len(clusterRoutes) > 0 {
			// Accept inbound route connections arriving over the tailnet
			// and relay them to the local cluster port.
			bridgeInboundPort(ts, cfg.ClusterPort)
		}
	}

	// dial NATS with the resolved credential (own or received via approval)
	log.Printf("connecting to NATS at %s (node: %s)", cfg.NatsURL, ident.NodeID)
	connectOpts := []nats.Option{
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * nats.DefaultReconnectWait),
		nats.SetCustomDialer(tsnetDialer{ts}),
		
		nats.ConnectHandler(func(c *nats.Conn) {
			log.Printf("NATS connected: %s", c.ConnectedUrl())
		}),
		nats.DisconnectErrHandler(func(c *nats.Conn, err error) {
			log.Printf("NATS disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			log.Printf("NATS reconnected: %s", c.ConnectedUrl())
		}),
		nats.ErrorHandler(func(c *nats.Conn, sub *nats.Subscription, err error) {
			log.Printf("NATS async error: %v", err)
		}),
	}
	if natsCred.Username != "" {
		connectOpts = append(connectOpts, nats.UserInfo(natsCred.Username, natsCred.Password))
	}

	nc, err := nats.Connect(cfg.NatsURL, connectOpts...) // for worker, this is the PUB conn to coordinator
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}
	log.Printf("NATS connect() returned, status=%s", nc.Status())
	//! NOTE: an Agent can run with neither coordinator nor worker built — just a NATS connection.

	// build coordinator/worker (not started yet)
	coord, err := buildCoordinator(cfg, nc, embeddedNATS, ts)
	if err != nil {
		return nil, err
	}

	workerAgent, err := buildWorker(cfg, nc)
	if err != nil {
		return nil, err
	}

	return &Agent{
		cfg:         cfg,
		tsnetServer: ts,
		natsConn:    nc,
		natsServer:  embeddedNATS,
		coordinator: coord,
		worker:      workerAgent,
		tailscaleIP: ip4.String(),
		nodeID:      ident.NodeID,
	}, nil
}

func (a *Agent) Start(ctx context.Context) error {
	log.Println("starting EdgeGrid services")

	if a.coordinator != nil {
		go func() {
			if err := a.coordinator.Start(ctx, a.cfg.Server.Port); err != nil {
				log.Printf("coordinator stopped: %v", err)
			}
		}()
	}

	if a.worker != nil {
		go func() {
			if err := a.worker.Start(ctx); err != nil {
				log.Printf("worker stopped: %v", err)
			}
		}()
	}

	<-ctx.Done()
	return nil
}

// Close shuts the agent down. It is intentionally idempotent because the TUI
// can legitimately ask the same agent to close from more than one path:
//   - the onboarding wizard replaces an already-running node
//   - the background runner notices Start returned and performs cleanup
//
// Only the first call should close worker/NATS/tsnet resources. Later calls
// must be no-ops so we never double-close the same handles.
func (a *Agent) Close() {
	a.closeOnce.Do(func() {
		log.Println("shutting down EdgeGrid services")
		if a.worker != nil {
			a.worker.Close()
		}
		if a.natsConn != nil {
			a.natsConn.Close()
			log.Println("closed NATS connection")
		}
		if a.natsServer != nil {
			a.natsServer.Shutdown()
		}
		if a.tsnetServer != nil {
			if err := a.tsnetServer.Close(); err != nil {
				log.Printf("closing tsnet server: %v", err)
			}
		}
	})
}
