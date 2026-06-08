package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/edgegrid/edgegrid/internal/coordinator"
	"github.com/edgegrid/edgegrid/internal/worker/agent"
	"github.com/joho/godotenv"
)

func main() {
	// 1. Load dotenv file optionally
	if err := godotenv.Load(); err != nil {
		// It's fine if there's no .env file
		log.Println("ℹ️ No .env file found; using system environment variables.")
	}

	// 2. Define CLI Flags
	roleServer := flag.Bool("server", false, "Start the coordinator server")
	roleClient := flag.Bool("client", false, "Start the worker client agent")
	natsURL := flag.String("nats", "", "NATS Connection URL")
	apiPort := flag.String("port", "", "Coordinator HTTP API Port")
	supportedModels := flag.String("models", "", "Comma-separated list of supported models (worker only)")
	workerID := flag.String("worker-id", "", "Custom worker ID (worker only)")

	flag.Parse()

	// 3. Determine running mode
	// If neither server nor client is set, run both (unified development mode)
	runServer := *roleServer
	runClient := *roleClient
	if !runServer && !runClient {
		log.Println("ℹ️ Neither -server nor -client flag specified. Running both in Unified mode.")
		runServer = true
		runClient = true
	}

	// 4. Resolve environment/flag values
	finalNatsURL := *natsURL
	if finalNatsURL == "" {
		finalNatsURL = os.Getenv("NATS_URL")
		if finalNatsURL == "" {
			finalNatsURL = "nats://localhost:4222"
		}
	}

	finalPort := *apiPort
	if finalPort == "" {
		finalPort = os.Getenv("PORT")
		if finalPort == "" {
			finalPort = "8080"
		}
	}
	if !strings.HasPrefix(finalPort, ":") {
		finalPort = ":" + finalPort
	}

	// Overwrite environment variables for worker if flags were provided
	if *supportedModels != "" {
		os.Setenv("SUPPORTED_MODELS", *supportedModels)
	}
	if *workerID != "" {
		os.Setenv("WORKER_ID", *workerID)
	}
	os.Setenv("NATS_URL", finalNatsURL)

	// 5. Setup context cancellation for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("🚀 Bootstrapping EdgeGrid P2P Node...")

	// 6. Launch coordinator if enabled
	var coord *coordinator.Coordinator
	if runServer {
		var err error
		coord, err = coordinator.NewCoordinator(finalNatsURL)
		if err != nil {
			log.Fatalf("❌ Failed to initialize Coordinator: %v", err)
		}
		defer coord.Close()

		go func() {
			if err := coord.Start(ctx, finalPort); err != nil {
				log.Printf("❌ Coordinator stopped with error: %v", err)
				stop()
			}
		}()
	}

	// 7. Launch worker agent if enabled
	var workerAgent *agent.Agent
	if runClient {
		var err error
		workerAgent, err = agent.NewAgent()
		if err != nil {
			log.Fatalf("❌ Failed to initialize Worker Agent: %v", err)
		}
		defer workerAgent.Close()

		go func() {
			if err := workerAgent.Start(ctx); err != nil {
				log.Printf("❌ Worker Agent stopped with error: %v", err)
				stop()
			}
		}()
	}

	// 8. Block until signal or error cancellation
	<-ctx.Done()
	log.Println("👋 Received shutdown signal. Stopping EdgeGrid services...")
}
