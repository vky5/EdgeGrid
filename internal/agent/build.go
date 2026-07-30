package agent

import (
	"fmt"
	"log"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/coordinator"
	"github.com/edgegrid/edgegrid/internal/natsserver"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"github.com/edgegrid/edgegrid/internal/worker"
	"github.com/edgegrid/edgegrid/internal/worker/executor"
	"github.com/nats-io/nats.go"
	"tailscale.com/tsnet"
)

// buildCoordinator constructs (not starts) the Coordinator if server role is enabled; else no-op.
func buildCoordinator(cfg *config.Config, nc *nats.Conn, embeddedNATS *natsserver.EmbeddedServer, ts *tsnet.Server) (*coordinator.Coordinator, error) {
	if !cfg.Server.Enabled {
		return nil, nil
	}

	coord, err := coordinator.NewCoordinatorWithConn(nc, cfg.Replicas, embeddedNATS)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to initialize coordinator: %w", err)
	}
	coord.SetDataDir(cfg.DataDir)
	coord.SetTsnetServer(ts)

	// load/generate admin token (guards admin HTTP endpoints)
	adminToken := nodeident.LoadToken(cfg.DataDir, "admin.token")
	if adminToken == "" {
		adminToken, err = nodeident.RandomToken(32)
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("generate admin token: %w", err)
		}
		if err := nodeident.SaveToken(cfg.DataDir, "admin.token", adminToken); err != nil {
			log.Printf("warning: could not save admin token: %v", err)
		}
		log.Printf("[edgegrid] ✦ admin token (save this): %s", adminToken)
	} else {
		log.Printf("[edgegrid] admin token loaded from %s/admin.token", cfg.DataDir)
	}
	coord.SetAdminToken(adminToken)

	if err := coord.EnsureStream(); err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to initialize coordinator stream: %w", err)
	}

	return coord, nil
}

// buildWorker constructs (not starts) the Worker if client role is enabled; else no-op.
func buildWorker(cfg *config.Config, nc *nats.Conn) (*worker.Worker, error) {
	if !cfg.Client.Enabled {
		return nil, nil
	}

	// JetStream log publisher shared by training (real stdout) and mock (synthetic lines).
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to init JetStream for log publishing: %w", err)
	}
	logPublish := func(jobID, line string) {
		if _, err := js.Publish(broker.SubjectLogsPrefix+jobID, []byte(line)); err != nil {
			log.Printf("failed to publish log line for job %s: %v", jobID, err)
		}
	}

	var execInstance executor.Executor
	switch cfg.Client.Executor {
	case "training":
		execInstance = executor.NewTrainingExecutor(logPublish)
	case "mock", "":
		execInstance = executor.NewMockExecutor(logPublish)
	default:
		nc.Close()
		return nil, fmt.Errorf("unknown executor type %q — valid options: training, mock", cfg.Client.Executor)
	}
	log.Printf("worker executor=%q (training runs Python + streams logs; mock fakes a short run)", cfg.Client.Executor)

	workerAgent, err := worker.NewWorkerWithConn(
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

	return workerAgent, nil
}
