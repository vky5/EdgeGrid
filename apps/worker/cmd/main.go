package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/edgegrid/edgegrid/worker/internal/agent"
)

func main() {
	log.Println("🛠️ Starting build worker agent")

	// 1. Initialize the worker agent
	a, err := agent.NewAgent()
	if err != nil {
		log.Fatalf("❌ Failed to initialize agent: %v", err)
	}
	defer a.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 2. Setup OS signal catching for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 3. Start the agent components (listeners, heartbeats, registry)
	if err := a.Start(ctx); err != nil {
		log.Fatalf("❌ Failed to start agent: %v", err)
	}

	// 4. Block until termination signal
	sig := <-sigChan
	log.Printf("👋 Received termination signal %v. Gracefully shutting down worker agent...", sig)
}