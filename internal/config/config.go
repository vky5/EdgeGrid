package config

import (
	"flag"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	NatsURL    string
	EmbedNATS  bool   // true when coordinator should start the embedded NATS server
	NATSPort   int    // port for embedded NATS (default 4222)
	NATSStore  string // JetStream persistence directory for embedded NATS
	Replicas   int    // NATS JetStream replication factor: 1=dev, 3=prod
	Server     ServerConfig
	Client     ClientConfig
}

type ServerConfig struct {
	Enabled bool
	Port    string
}

type ClientConfig struct {
	Enabled         bool
	WorkerID        string
	Executor        string
	RequireApproval bool
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func LoadConfig() *Config {
	roleServer    := flag.Bool("server", false, "Start the coordinator server")
	roleClient    := flag.Bool("client", false, "Start the worker client agent")
	natsURL       := flag.String("nats", "", "NATS server URL (omit to auto-start embedded NATS when running as coordinator)")
	natsPort      := flag.Int("nats-port", 0, "Port for the embedded NATS server (default 4222)")
	natsStore     := flag.String("nats-store", "", "Directory for embedded NATS JetStream persistence (default ./data/nats)")
	apiPort       := flag.String("port", "", "Coordinator HTTP API port (default 8080)")
	workerID      := flag.String("worker-id", "", "Custom worker ID (worker only)")
	executorType  := flag.String("executor", "", "Executor backend: mock or training (default mock)")
	requireApproval := flag.Bool("require-approval", false, "Worker must approve each job before running it")
	replicas      := flag.Int("replicas", 0, "NATS JetStream replication factor (0 = auto-detect from env)")

	flag.Parse()

	runServer := *roleServer
	runClient := *roleClient
	if !runServer && !runClient {
		runServer = true
		runClient = true
	}

	// Determine NATS URL and whether to embed.
	// Rule: if no --nats flag and no NATS_URL env var, and we're running as
	// coordinator, start the embedded NATS server automatically.
	explicitNatsURL := *natsURL
	if explicitNatsURL == "" {
		explicitNatsURL = os.Getenv("NATS_URL")
	}

	embedNATS := false
	finalNatsURL := explicitNatsURL
	if finalNatsURL == "" {
		if runServer {
			// Coordinator mode with no external NATS specified → embed.
			embedNATS = true
			finalNatsURL = "nats://localhost:4222" // overwritten after server starts
		} else {
			// Worker-only with no URL — fail loudly at startup.
			finalNatsURL = "nats://localhost:4222"
		}
	}

	finalNATSPort := *natsPort
	if finalNATSPort == 0 {
		finalNATSPort = envInt("NATS_PORT", 4222)
	}

	finalNATSStore := *natsStore
	if finalNATSStore == "" {
		finalNATSStore = envStr("NATS_STORE_DIR", "./data/nats")
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

	finalWorkerID := *workerID
	if finalWorkerID == "" {
		finalWorkerID = os.Getenv("WORKER_ID")
	}

	finalExecutor := *executorType
	if finalExecutor == "" {
		finalExecutor = os.Getenv("EXECUTOR")
		if finalExecutor == "" {
			finalExecutor = "mock"
		}
	}

	finalReplicas := *replicas
	if finalReplicas == 0 {
		finalReplicas = envInt("NATS_REPLICAS", 1)
	}
	if finalReplicas < 1 {
		finalReplicas = 1
	}

	return &Config{
		NatsURL:   finalNatsURL,
		EmbedNATS: embedNATS,
		NATSPort:  finalNATSPort,
		NATSStore: finalNATSStore,
		Replicas:  finalReplicas,
		Server: ServerConfig{
			Enabled: runServer,
			Port:    finalPort,
		},
		Client: ClientConfig{
			Enabled:         runClient,
			WorkerID:        finalWorkerID,
			Executor:        finalExecutor,
			RequireApproval: *requireApproval,
		},
	}
}
