package config

import (
	"flag"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	NatsURL  string
	Replicas int // NATS JetStream replication factor: 1=dev, 3=prod
	Server   ServerConfig
	Client   ClientConfig
}

type ServerConfig struct {
	Enabled bool
	Port    string
}

type ClientConfig struct {
	Enabled         bool
	SupportedModels []string
	WorkerID        string
	Executor        string
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func LoadConfig() *Config {
	roleServer := flag.Bool("server", false, "Start the coordinator server")
	roleClient := flag.Bool("client", false, "Start the worker client agent")
	natsURL := flag.String("nats", "", "NATS Connection URL")
	apiPort := flag.String("port", "", "Coordinator HTTP API Port")
	supportedModels := flag.String("models", "", "Comma-separated list of supported models (worker only)")
	workerID := flag.String("worker-id", "", "Custom worker ID (worker only)")
	executorType := flag.String("executor", "", "Executor backend (mock or training)")
	replicas := flag.Int("replicas", 0, "NATS JetStream replication factor (0 = auto-detect from env)")

	flag.Parse()

	runServer := *roleServer
	runClient := *roleClient
	if !runServer && !runClient {
		runServer = true
		runClient = true
	}

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

	modelsStr := *supportedModels
	if modelsStr == "" {
		modelsStr = os.Getenv("SUPPORTED_MODELS")
	}
	var models []string
	if modelsStr != "" {
		for _, m := range strings.Split(modelsStr, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				models = append(models, m)
			}
		}
	}
	// No default models — workers must explicitly declare what they support

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
		NatsURL:  finalNatsURL,
		Replicas: finalReplicas,
		Server: ServerConfig{
			Enabled: runServer,
			Port:    finalPort,
		},
		Client: ClientConfig{
			Enabled:         runClient,
			SupportedModels: models,
			WorkerID:        finalWorkerID,
			Executor:        finalExecutor,
		},
	}
}
