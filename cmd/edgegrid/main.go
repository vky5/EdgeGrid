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
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found; using environment variables")
	}

	cfg := config.LoadConfig()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	nodeAgent, err := agent.NewAgent(cfg)
	if err != nil {
		log.Fatalf("failed to initialize EdgeGrid agent: %v", err)
	}
	defer nodeAgent.Close()

	go func() {
		if err := nodeAgent.Start(ctx); err != nil {
			log.Printf("EdgeGrid agent stopped: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Println("received shutdown signal")
}
