package config

import (
	"flag"
	"os"
	"strings"
)

type Config struct {
	NatsURL string
	Server  ServerConfig
	Client  ClientConfig
}

type ServerConfig struct {
	Enabled bool
	Port    string
}

type ClientConfig struct {
	Enabled         bool
	SupportedModels []string
	WorkerID        string
}

func LoadConfig() *Config {
	roleServer := flag.Bool("server", false, "Start the coordinator server")
	roleClient := flag.Bool("client", false, "Start the worker client agent")
	natsURL := flag.String("nats", "", "NATS Connection URL")
	apiPort := flag.String("port", "", "Coordinator HTTP API Port")
	supportedModels := flag.String("models", "", "Comma-separated list of supported models (worker only)")
	workerID := flag.String("worker-id", "", "Custom worker ID (worker only)")

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
	if len(models) == 0 {
		models = []string{"all-minilm"}
	}

	finalWorkerID := *workerID
	if finalWorkerID == "" {
		finalWorkerID = os.Getenv("WORKER_ID")
	}

	return &Config{
		NatsURL: finalNatsURL,
		Server: ServerConfig{
			Enabled: runServer,
			Port:    finalPort,
		},
		Client: ClientConfig{
			Enabled:         runClient,
			SupportedModels: models,
			WorkerID:        finalWorkerID,
		},
	}
}
