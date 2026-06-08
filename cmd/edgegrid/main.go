package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/edgegrid/edgegrid/internal/agent"
	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/joho/godotenv"
)

func main() {
	// 1. Load dotenv file optionally
	if err := godotenv.Load(); err != nil {
		// It's fine if there's no .env file
		log.Println("ℹ️ No .env file found; using system environment variables.")
	}

	// 2. Parse configuration from flags/environment
	cfg := config.LoadConfig()

	// 3. Setup context cancellation for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 4. Initialize and start the unified agent
	nodeAgent, err := agent.NewAgent(cfg)
	if err != nil {
		log.Fatalf("❌ Failed to initialize EdgeGrid Agent: %v", err)
	}
	defer nodeAgent.Close()

	go func() {
		if err := nodeAgent.Start(ctx); err != nil {
			log.Printf("❌ EdgeGrid Agent stopped with error: %v", err)
			stop()
		}
	}()

	// 5. Block until signal or cancellation
	<-ctx.Done()
	log.Println("👋 Received shutdown signal. Stopping EdgeGrid services...")
}
