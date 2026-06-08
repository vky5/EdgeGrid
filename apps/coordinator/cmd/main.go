// this is the entrypoint of the buil-orchestrator

/*
a job dispatcher with concurrency control, health checks, worker orchestration, gRPC coordination, and context-aware cancellation for jobs
*/

// I will add emojis in logs

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/edgegrid/edgegrid/coordinator/internal/broker"
	"github.com/edgegrid/edgegrid/coordinator/internal/workerman"
)

func main() {
	log.Println("🔄 Starting orchestrator")
	ctx, cancel := context.WithCancel(context.Background()) // creating a main context for the orchestrator to close everything at once
	defer cancel()

	if err := run(ctx); err != nil {
		log.Printf("❌ Fatal error: %v", err)
		os.Exit(1) // defer calls in `main()` are still respected because `run()` returned
	}
}

func run(ctx context.Context) error {
	// ------- Initialize NATS JetStream Broker ------
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}
	jsBroker, err := broker.NewBroker(natsURL)
	if err != nil {
		return fmt.Errorf("failed to initialize NATS JetStream: %w", err)
	}
	defer jsBroker.Close()

	// ------- Initialize Worker Manager and Dispatcher State ------
	manager := workerman.NewWorkerManager()

	log.Println("🛠️ Worker manager initialized")
	go manager.StartHealthChecker(ctx, 2*time.Minute) // this will periodically check health in every 2 minutes
	log.Println("🩺 Health checker started for workers")

	// Subscribe to worker events (registrations/heartbeats) via NATS JetStream
	if err := jsBroker.SubscribeToWorkerEvents(ctx, manager); err != nil {
		return fmt.Errorf("failed to subscribe to worker NATS events: %w", err)
	}

	// Subscribe to job results from workers via NATS JetStream
	if err := jsBroker.SubscribeToResults(ctx); err != nil {
		return fmt.Errorf("failed to subscribe to job results: %w", err)
	}

	// Start HTTP API for job submission
	apiAddr := os.Getenv("PORT")
	if apiAddr == "" {
		apiAddr = ":8080"
	} else if apiAddr[0] != ':' {
		apiAddr = ":" + apiAddr
	}
	go startHTTPServer(apiAddr, jsBroker)

	// starting the dispatcher
	defer shutdownGracefully() // ensure graceful shutdown on exit

	// ❗BLOCK HERE until termination
	<-ctx.Done()
	return nil
}

/*
Pure NATS JetStream Event-Driven Embedding Inference Flow:

1. Job Ingestion & Queuing:
   - External clients or APIs publish job requests to NATS JetStream under the subject:
     `jobs.build.<model_name>` (e.g., `jobs.build.nomic-embed-text`)
   - NATS JetStream durably queues these requests within the "JOBS" stream.

2. Model Compatibility Routing & Pull Consumption:
   - Workers connect to NATS JetStream and pull jobs only from subjects corresponding to their supported models.
   - For example, a worker supporting "llama3" and "clip" subscribes to `jobs.build.llama3` and `jobs.build.clip`.
   - NATS automatically delivers the job to the first available worker pulling from that model subject (competing consumer model).

3. Passive Worker Management:
   - Workers publish registration events to `workers.register` upon startup.
   - Workers send periodic status pings to `workers.heartbeat` (e.g., "free", "busy").
   - The Orchestrator subscribes to these subjects to passively monitor and track active nodes in the `WorkerManager`.

4. Results Submission:
   - Once a worker finishes generating embeddings, it publishes the result vector as a `JobResponse` to `jobs.results`.
   - The Orchestrator consumes from `jobs.results` to log performance, capture metadata, or update the main DB.
*/
