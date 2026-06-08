package agent

import (
	"context"
	"fmt"
	"log"

	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/coordinator"
	workeragent "github.com/edgegrid/edgegrid/internal/worker/agent"
	"github.com/nats-io/nats.go"
)

type Agent struct {
	cfg         *config.Config
	natsConn    *nats.Conn
	coordinator *coordinator.Coordinator // coordinator object (can be nil if not loaded)
	worker      *workeragent.Agent // worker agent object (can be nil if not loaded)
}

func NewAgent(cfg *config.Config) (*Agent, error) {
	log.Printf("🔌 Connecting to NATS at %s...", cfg.NatsURL)
	nc, err := nats.Connect(cfg.NatsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	var coord *coordinator.Coordinator
	if cfg.Server.Enabled {
		coord, err = coordinator.NewCoordinatorWithConn(nc)
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("failed to initialize coordinator: %w", err)
		}
	}

	var worker *workeragent.Agent
	if cfg.Client.Enabled {
		worker, err = workeragent.NewAgentWithConn(nc, cfg.Client.SupportedModels, cfg.Client.WorkerID)
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("failed to initialize worker: %w", err)
		}
	}

	return &Agent{
		cfg:         cfg,
		natsConn:    nc,
		coordinator: coord,
		worker:      worker,
	}, nil
}

func (a *Agent) Start(ctx context.Context) error {
	log.Println("🚀 Starting EdgeGrid P2P Agent services...")

	// Start coordinator server if enabled
	if a.coordinator != nil {
		go func() {
			if err := a.coordinator.Start(ctx, a.cfg.Server.Port); err != nil {
				log.Printf("❌ Coordinator stopped with error: %v", err)
			}
		}()
	}

	// Start worker agent if enabled
	if a.worker != nil {
		go func() {
			if err := a.worker.Start(ctx); err != nil {
				log.Printf("❌ Worker stopped with error: %v", err)
			}
		}()
	}

	// Block until context cancellation
	<-ctx.Done()
	return nil
}

func (a *Agent) Close() {
	log.Println("👋 Shutting down EdgeGrid agent...")
	if a.worker != nil {
		a.worker.Close()
	}
	if a.coordinator != nil {
		a.coordinator.Close()
	}
	if a.natsConn != nil {
		a.natsConn.Close()
		log.Println("🔌 Closed shared NATS connection.")
	}
}
