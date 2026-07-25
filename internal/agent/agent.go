package agent

import (
	"context"
	"fmt"
	"log"
	"path/filepath"

	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/coordinator"
	"github.com/edgegrid/edgegrid/internal/natsserver"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"github.com/edgegrid/edgegrid/internal/worker"
	"github.com/nats-io/nats.go"
	"tailscale.com/tsnet"
)

// Entire lifecycle of the application
type Agent struct {
	cfg         *config.Config
	tsnetServer *tsnet.Server
	natsConn    *nats.Conn
	natsServer  *natsserver.EmbeddedServer
	coordinator *coordinator.Coordinator
	worker      *worker.Worker
}

func NewAgent(ctx context.Context, cfg *config.Config) (*Agent, error) {
	ts := &tsnet.Server{
		Dir:      filepath.Join(cfg.DataDir, "tsnet"),
		Hostname: cfg.TailscaleHostname,
		AuthKey:  cfg.TailscaleAuthKey,
	}
	status, err := ts.Up(ctx) //
	if err != nil {
		return nil, fmt.Errorf("tsnet up: %w", err)
	}
	ip4, ip6 := ts.TailscaleIPs()
	log.Printf("tsnet up: hostname=%s ip4=%s ip6=%s backend=%s", cfg.TailscaleHostname, ip4, ip6, status.BackendState)

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
		embeddedNATS, err = startEmbeddedNATS(cfg, natsCred, clusterSecret, clusterRoutes)
		if err != nil {
			return nil, err
		}
		// Accept inbound route connections arriving over the tailnet and
		// relay them to the (now always-bound) local cluster port.
		bridgeInboundCluster(ts, cfg.ClusterPort)
	}

	// dial NATS with the resolved credential (own or received via approval)
	log.Printf("connecting to NATS at %s (node: %s)", cfg.NatsURL, ident.NodeID)
	connectOpts := []nats.Option{
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * nats.DefaultReconnectWait),
	}
	if natsCred.Username != "" {
		connectOpts = append(connectOpts, nats.UserInfo(natsCred.Username, natsCred.Password))
	}

	nc, err := nats.Connect(cfg.NatsURL, connectOpts...) // for worker, this is the PUB conn to coordinator
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}
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
