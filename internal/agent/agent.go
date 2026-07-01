package agent

import (
	"context"
	"fmt"
	"log"

	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/coordinator"
	"github.com/edgegrid/edgegrid/internal/natsserver"
	"github.com/edgegrid/edgegrid/internal/worker"
	"github.com/edgegrid/edgegrid/internal/worker/executor"
	"github.com/nats-io/nats.go"
)

type Agent struct {
	cfg         *config.Config
	natsConn    *nats.Conn
	natsServer  *natsserver.EmbeddedServer
	coordinator *coordinator.Coordinator
	worker      *worker.Worker
}

func NewAgent(cfg *config.Config) (*Agent, error) {
	var embeddedNATS *natsserver.EmbeddedServer

	if cfg.EmbedNATS {
		ns, err := natsserver.Start(cfg.NATSPort, cfg.NATSStore)
		if err != nil {
			return nil, fmt.Errorf("failed to start embedded NATS: %w", err)
		}
		embeddedNATS = ns
		// Use the URL the embedded server is actually listening on.
		cfg.NatsURL = ns.ClientURL()
	}

	log.Printf("connecting to NATS at %s", cfg.NatsURL)
	nc, err := nats.Connect(cfg.NatsURL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*nats.DefaultReconnectWait),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	var coord *coordinator.Coordinator
	if cfg.Server.Enabled {
		coord, err = coordinator.NewCoordinatorWithConn(nc, cfg.Replicas)
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("failed to initialize coordinator: %w", err)
		}
		if err := coord.EnsureStream(); err != nil {
			nc.Close()
			return nil, fmt.Errorf("failed to initialize coordinator stream: %w", err)
		}
	}

	var workerAgent *worker.Worker
	if cfg.Client.Enabled {
		var execInstance executor.Executor
		switch cfg.Client.Executor {
		case "training":
			js, err := nc.JetStream()
			if err != nil {
				nc.Close()
				return nil, fmt.Errorf("failed to init JetStream for log publishing: %w", err)
			}
			execInstance = executor.NewTrainingExecutor(func(jobID, line string) {
				if _, err := js.Publish("jobs.logs."+jobID, []byte(line)); err != nil {
					log.Printf("failed to publish log line for job %s: %v", jobID, err)
				}
			})
		case "mock", "":
			execInstance = executor.NewMockExecutor()
		default:
			nc.Close()
			return nil, fmt.Errorf("unknown executor type %q — valid options: training, mock", cfg.Client.Executor)
		}

		workerAgent, err = worker.NewWorkerWithConn(
			nc,
			cfg.Client.WorkerID,
			execInstance,
			cfg.Replicas,
			cfg.Client.RequireApproval,
		)
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("failed to initialize worker: %w", err)
		}
	}

	return &Agent{
		cfg:         cfg,
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
}
