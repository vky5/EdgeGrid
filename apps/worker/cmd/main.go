package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/edgegrid/edgegrid/worker/internal/broker"
)

func main() {
	log.Println("🛠️ Starting build worker")

	workerID := generateWorkerID()

	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}

	wb, err := broker.NewWorkerBroker(natsURL)
	if err != nil {
		log.Fatalf("❌ Failed to initialize NATS: %v", err)
	}
	defer wb.Close()

	modelsEnv := os.Getenv("SUPPORTED_MODELS")
	var supportedModels []string
	if modelsEnv != "" {
		for _, m := range strings.Split(modelsEnv, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				supportedModels = append(supportedModels, m)
			}
		}
	}
	if len(supportedModels) == 0 {
		supportedModels = []string{"all-minilm"}
	}

	// Register with Coordinator over NATS
	err = wb.RegisterWorker(workerID, supportedModels)
	if err != nil {
		log.Fatalf("❌ Failed to register with NATS: %v", err)
	}
	log.Printf("✅ Registered with Coordinator over NATS. ID: %s, Models: %v", workerID, supportedModels)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start heartbeat routine
	go wb.StartHeartbeat(ctx, workerID, 10*time.Second)
	log.Println("💓 Started heartbeat routine")

	// Start pull listeners for each model
	for _, model := range supportedModels {
		go wb.StartJobListener(ctx, workerID, model)
	}

	// Keep running
	select {}
}

func generateWorkerID() string {
	// Check if WORKER_ID is provided via env
	workerID := os.Getenv("WORKER_ID")
	if workerID != "" {
		return workerID
	}

	// Use container hostname (unique per container) + timestamp + random suffix
	hostname, _ := os.Hostname()
	timestamp := time.Now().UnixNano()
	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes) // generate 4 random bytes
	randHex := hex.EncodeToString(randBytes)

	return fmt.Sprintf("worker-%s-%d-%s", hostname, timestamp, randHex)
}