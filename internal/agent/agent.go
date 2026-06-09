package agent

import (
	"context"
	"fmt"
	"log"

	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/coordinator"
	"github.com/edgegrid/edgegrid/internal/worker"
	"github.com/nats-io/nats.go"
)

type Agent struct {
	cfg         *config.Config
	natsConn    *nats.Conn
	coordinator *coordinator.Coordinator
	worker      *worker.Worker
}

func NewAgent(cfg *config.Config) (*Agent, error) {
	log.Printf("connecting to NATS at %s", cfg.NatsURL)
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

	var workerAgent *worker.Worker
	if cfg.Client.Enabled {
		workerAgent, err = worker.NewWorkerWithConn(nc, cfg.Client.SupportedModels, cfg.Client.WorkerID)
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("failed to initialize worker: %w", err)
		}
	}

	return &Agent{
		cfg:         cfg,
		natsConn:    nc,
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
	if a.natsConn != nil {
		a.natsConn.Close()
		log.Println("closed NATS connection")
	}
}
