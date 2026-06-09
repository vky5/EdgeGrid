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

func NewCoordinatorWithConn(nc *nats.Conn) (*Coordinator, error) {
	jsBroker, err := broker.NewBroker(nc)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize shared broker: %w", err)
	}

	manager := workerman.NewWorkerManager()

	return &Coordinator{
		jsBroker: jsBroker,
		manager:  manager,
	}, nil
}

func (c *Coordinator) Start(ctx context.Context, apiAddr string) error {
	log.Println("🔄 Starting coordinator")

	// Ensure NATS Stream is initialized
	if err := c.jsBroker.EnsureStream(); err != nil {
		return fmt.Errorf("failed to verify/ensure NATS Stream: %w", err)
	}

	log.Println("🛠️ Worker manager initialized")

	go c.manager.StartHealthChecker(ctx, 2*time.Minute)
	log.Println("🩺 Health checker started for workers")

	if err := c.SubscribeToWorkerEvents(ctx); err != nil {
		return fmt.Errorf("failed to subscribe to worker NATS events: %w", err)
	}

	if err := c.SubscribeToResults(ctx); err != nil {
		return fmt.Errorf("failed to subscribe to job results: %w", err)
	}

	go StartHTTPServer(apiAddr, c.jsBroker)

	<-ctx.Done()
	log.Println("👋 Shutting down coordinator gracefully...")
	return nil
}
