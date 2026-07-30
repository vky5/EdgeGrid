package config

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/edgegrid/edgegrid/internal/profile"
)

type Config struct {
	NatsURL       string
	EmbedNATS     bool     // true when coordinator should start the embedded NATS server
	NATSPort      int      // port for embedded NATS (default 4222)
	NATSStore     string   // JetStream persistence directory for embedded NATS
	DataDir       string   // directory for node identity and token files (default ./data)
	Replicas      int      // NATS JetStream replication factor: 1=dev, 3=prod
	ClusterName   string   // NATS cluster name (all nodes must match)
	ClusterPort   int      // intra-cluster gossip port (default 6222)
	Routes        []string // seed route URLs, e.g. nats://blacktree.in:6222
	JoinURL       string   // coordinator HTTP URL to send a join request to (non-primary nodes)
	AdvertiseHost string   // externally-reachable host for this coordinator's embedded NATS (optional)

	TailscaleAuthKey  string // tsnet auth key for joining the tailnet (optional; falls back to interactive login)
	TailscaleHostname string // hostname this node presents on the tailnet (default: os.Hostname())

	Server ServerConfig
	Client ClientConfig
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

// ResolveDataDir applies the flag → DATA_DIR env → active profile → ./data
// fallback used everywhere a data dir is needed (node startup, the TUI, the
// plain `logs` CLI) — one implementation so all of them agree on where a
// node's state lives. The flag and env var stay first so they remain an
// explicit escape hatch even once a profile is active.
func ResolveDataDir(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("DATA_DIR"); v != "" {
		return v
	}
	if dir := profile.Dir(); dir != "" {
		return dir
	}
	return "./data"
}

var (
	loadOnce  sync.Once
	loadedCfg *Config
)

// LoadConfig parses flags/env into a Config exactly once per process and
// caches the result — registering the same flag name on the global
// flag.CommandLine twice panics ("flag redefined"), and this now has more
// than one call site that can legitimately run more than once in the same
// process (e.g. the TUI's onboarding wizard can be entered more than once
// via "/onboard"). Every call after the first just returns the cached
// Config; flags/env are read once, at process start, same as before.
func LoadConfig() *Config {
	loadOnce.Do(func() {
		loadedCfg = loadConfigOnce()
	})
	return loadedCfg
}

func loadConfigOnce() *Config {
	roleServer := flag.Bool("server", false, "Start the coordinator server")
	roleClient := flag.Bool("client", false, "Start the worker client agent")
	natsURL := flag.String("nats", "", "NATS server URL (omit to auto-start embedded NATS when running as coordinator)")
	natsPort := flag.Int("nats-port", 0, "Port for the embedded NATS server (default 4222)")
	natsStore := flag.String("nats-store", "", "Directory for embedded NATS JetStream persistence (default ./data/nats)")
	apiPort := flag.String("port", "", "Coordinator HTTP API port (default 8080)")
	workerID := flag.String("worker-id", "", "Custom worker ID (worker only)")
	executorType := flag.String("executor", "", "Executor backend: mock or training (default training — runs real Python + NATS logs)")
	requireApproval := flag.Bool("require-approval", false, "Worker must approve each job before running it")
	replicas := flag.Int("replicas", 0, "NATS JetStream replication factor (0 = auto-detect from env)")
	clusterName := flag.String("cluster-name", "", "NATS cluster name — all server nodes must use the same name (default edgegrid)")
	clusterPort := flag.Int("cluster-port", 0, "Intra-cluster gossip port for embedded NATS (default 6222)")
	routes := flag.String("routes", "", "Comma-separated seed route URLs for clustering, e.g. nats://blacktree.in:6222")
	joinURL := flag.String("join", "", "Coordinator HTTP URL to request cluster/worker join approval, e.g. http://blacktree.in:8080")
	dataDir := flag.String("data-dir", "", "Directory for node identity and credential files (default ./data)")
	advertiseHost := flag.String("advertise-host", "", "Externally-reachable host for this coordinator's embedded NATS, e.g. blacktree.in (default: none — join responses fall back to localhost)")
	tsAuthKey := flag.String("ts-authkey", "", "tsnet auth key for joining the tailnet (default: interactive login, or TS_AUTHKEY env)")
	tsHostname := flag.String("ts-hostname", "", "hostname to present on the tailnet (default: os.Hostname())")

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

	finalDataDir := ResolveDataDir(*dataDir)

	finalNATSStore := *natsStore
	if finalNATSStore == "" {
		// Derived from the resolved data dir (not a literal "./data/nats")
		// so JetStream storage moves with the data dir/profile instead of
		// always landing in the same place regardless of which is active.
		finalNATSStore = envStr("NATS_STORE_DIR", filepath.Join(finalDataDir, "nats"))
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
			// training runs the submitted script and publishes stdout to
			// jobs.logs.* so the dashboard can stream logs. mock is for
			// tests/CI without Python: EXECUTOR=mock or --executor mock.
			finalExecutor = "training"
		}
	}

	finalReplicas := *replicas
	if finalReplicas == 0 {
		finalReplicas = envInt("NATS_REPLICAS", 1)
	}
	if finalReplicas < 1 {
		finalReplicas = 1
	}

	finalClusterName := *clusterName
	if finalClusterName == "" {
		finalClusterName = envStr("NATS_CLUSTER_NAME", "edgegrid")
	}

	finalClusterPort := *clusterPort
	if finalClusterPort == 0 {
		finalClusterPort = envInt("NATS_CLUSTER_PORT", 6222)
	}

	var finalRoutes []string
	routeStr := *routes
	if routeStr == "" {
		routeStr = os.Getenv("NATS_ROUTES")
	}
	if routeStr != "" {
		for _, r := range strings.Split(routeStr, ",") {
			if r = strings.TrimSpace(r); r != "" {
				finalRoutes = append(finalRoutes, r)
			}
		}
	}

	finalJoinURL := *joinURL
	if finalJoinURL == "" {
		finalJoinURL = os.Getenv("JOIN_URL")
	}

	finalAdvertiseHost := *advertiseHost
	if finalAdvertiseHost == "" {
		finalAdvertiseHost = os.Getenv("ADVERTISE_HOST")
	}

	finalTailscaleAuthKey := *tsAuthKey
	if finalTailscaleAuthKey == "" {
		finalTailscaleAuthKey = os.Getenv("TS_AUTHKEY")
	}

	finalTailscaleHostname := *tsHostname
	if finalTailscaleHostname == "" {
		finalTailscaleHostname = os.Getenv("TS_HOSTNAME")
	}
	if finalTailscaleHostname == "" {
		finalTailscaleHostname, _ = os.Hostname()
	}

	return &Config{
		NatsURL:       finalNatsURL,
		EmbedNATS:     embedNATS,
		NATSPort:      finalNATSPort,
		NATSStore:     finalNATSStore,
		DataDir:       finalDataDir,
		Replicas:      finalReplicas,
		ClusterName:   finalClusterName,
		ClusterPort:   finalClusterPort,
		Routes:        finalRoutes,
		JoinURL:       finalJoinURL,
		AdvertiseHost: finalAdvertiseHost,

		TailscaleAuthKey:  finalTailscaleAuthKey,
		TailscaleHostname: finalTailscaleHostname,

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
