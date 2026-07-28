package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/coordinator"
	"github.com/edgegrid/edgegrid/internal/natsserver"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"github.com/edgegrid/edgegrid/internal/nodelog"
	"github.com/edgegrid/edgegrid/internal/worker"
	"github.com/nats-io/nats.go"
	"tailscale.com/tsnet"
)

func NewAgentWithLogging(ctx context.Context, cfg *config.Config, onProgress func(string), tuiMode bool) (*Agent, func() error, error) {
	closeLog, err := nodelog.Setup(cfg.DataDir)
	if err != nil {
		log.Printf("warning: could not open log file, logging to stdout only: %v", err)
		closeLog = func() error { return nil }
	}
	if tuiMode {
		// In TUI mode, we do NOT want logs written to os.Stdout because it corrupts the Bubble Tea screen.
		f, err := os.OpenFile(nodelog.Path(cfg.DataDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			log.SetOutput(f)
			oldClose := closeLog
			closeLog = func() error {
				f.Close()
				return oldClose()
			}
		}
	}
	a, err := NewAgent(ctx, cfg, onProgress)
	if err != nil {
		return nil, closeLog, err
	}
	return a, closeLog, nil
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
}

// TailscaleIP is this node's own tailnet address — the one other nodes
// need to reach it. Empty if tsnet never came up (NewAgent would have
// failed before returning in that case, so in practice this is always set
// on a successfully-built Agent).
func (a *Agent) TailscaleIP() string { return a.tailscaleIP }

// NodeID is this node's persistent identity (see nodeident).
func (a *Agent) NodeID() string { return a.nodeID }

// AdminToken is the bearer token guarding this node's admin HTTP
// endpoints — empty for a worker, which never runs a coordinator/admin
// API at all.
func (a *Agent) AdminToken() string {
	if a.coordinator == nil {
		return ""
	}
	return a.coordinator.AdminToken()
}

// NewAgent brings up a full node. onProgress, if non-nil, receives every
// status line tsnet would otherwise only send to log.Printf — notably the
// interactive login URL — so a caller like the onboarding TUI can surface
// it directly instead of the user needing to go find it in stdout/log
// files. Always still logged normally too; this is additive.
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
	status, err := ts.Up(ctx) //
	if err != nil {
		return nil, fmt.Errorf("tsnet up: %w", err)
	}
	ip4, ip6 := ts.TailscaleIPs()
	tsnetUpLine := fmt.Sprintf("tsnet up: hostname=%s ip4=%s ip6=%s backend=%s", cfg.TailscaleHostname, ip4, ip6, status.BackendState)
	log.Print(tsnetUpLine)
	if onProgress != nil {
		// Unlike tsnet's own UserLogf-routed lines above, this one is our
		// own log.Printf — never reached onProgress before, so a caller
		// like the onboarding TUI's "Starting" screen never showed the
		// address it just got, only the raw log file did.
		onProgress(tsnetUpLine)
	}
	if cfg.AdvertiseHost == "" && ip4.IsValid() {
		cfg.AdvertiseHost = ip4.String()
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
			// Rewrite remote route addresses (e.g. from a join/approve
			// response) to local proxies dialed through tsnet — nats-server's
			// own dialer can't reach a tsnet address directly.
			clusterRoutes = bridgeOutboundRoutes(ctx, ts, clusterRoutes)
		}
		// A lone bootstrap coordinator (no --routes, nothing joined yet)
		// stays standalone — natsserver.buildOpts only enables clustering
		// when clusterRoutes is non-empty (see its comment: a self-route
		// can't satisfy this, nats-server always kills it as a duplicate
		// at runtime, and clustered JetStream never stabilizes with zero
		// real peers). Cluster-mode wiring for a node that later gets
		// joined is a separate follow-up, not handled yet.
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
		// RetryOnFailedConnect means Connect() below can return success
		// before a real connection exists yet — these make that visible
		// instead of silent, since otherwise the only symptom is an
		// unrelated-looking timeout wherever the connection is first
		// actually used (e.g. the coordinator's KV bucket creation).
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

func (a *Agent) Close() {
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
}
