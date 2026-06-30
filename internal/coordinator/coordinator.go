package coordinator

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/coordinator/workerman"
	"github.com/nats-io/nats.go"
)

type Coordinator struct {
	jsBroker *broker.Broker
	manager  *workerman.WorkerManager
}

func NewCoordinatorWithConn(nc *nats.Conn, replicas int) (*Coordinator, error) {
	jsBroker, err := broker.NewBroker(nc, replicas)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize shared broker: %w", err)
	}

	manager, err := workerman.NewWorkerManager(jsBroker)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize worker manager: %w", err)
	}

	return &Coordinator{
		jsBroker: jsBroker,
		manager:  manager,
	}, nil
}

func (c *Coordinator) EnsureStream() error {
	return c.jsBroker.EnsureStream()
}

func (c *Coordinator) Start(ctx context.Context, apiAddr string) error {
	log.Println("starting coordinator")

	if err := c.jsBroker.EnsureStream(); err != nil {
		return fmt.Errorf("failed to verify/ensure NATS Stream: %w", err)
	}

	log.Println("worker manager initialized")

	go c.manager.StartHealthChecker(ctx, 2*time.Minute)
	log.Println("worker health checker started")

	if err := c.SubscribeToWorkerEvents(ctx); err != nil {
		return fmt.Errorf("failed to subscribe to worker NATS events: %w", err)
	}

	if err := c.SubscribeToResults(ctx); err != nil {
		return fmt.Errorf("failed to subscribe to job results: %w", err)
	}

	go StartHTTPServer(apiAddr, c.jsBroker, c.manager)

	<-ctx.Done()
	log.Println("shutting down coordinator")
	return nil
}
