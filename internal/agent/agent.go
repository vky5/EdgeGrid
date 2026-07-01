package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/coordinator"
	"github.com/edgegrid/edgegrid/internal/joinmgr"
	"github.com/edgegrid/edgegrid/internal/natsserver"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"github.com/edgegrid/edgegrid/internal/worker"
	"github.com/edgegrid/edgegrid/internal/worker/executor"
	"github.com/nats-io/nats.go"
)

type Agent struct {
	cfg         *config.Config
	natsConn    *nats.Conn
	natsServer  *natsserver.EmbeddedServer
	coordinator *coordinator.Coordinator
	worker      *worker.Worker
}

func NewAgent(cfg *config.Config) (*Agent, error) {
	// Load or generate persistent node identity.
	ident, err := nodeident.LoadOrCreate(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("node identity: %w", err)
	}

	// Determine NATS credentials for this node.
	// - Primary coordinators generate their own cred and start NATS immediately.
	// - Non-primary nodes (--join flag) wait for admin approval first,
	//   then receive their cred and optionally cluster config.
	var coordCred natsserver.NodeCred
	var clusterSecret string
	var clusterRoutes []string

	if cfg.EmbedNATS {
		if cfg.JoinURL != "" {
			// Non-primary server: request join approval, wait for it, then start NATS.
			joinResult, err := requestAndWaitForApproval(cfg, ident, joinmgr.RoleServer)
			if err != nil {
				return nil, err
			}
			clusterSecret = joinResult.ClusterSecret
			clusterRoutes = joinResult.ClusterRoutes
			// Save the received token as this node's coordinator cred.
			if err := nodeident.SaveToken(cfg.DataDir, "node.token", joinResult.Token); err != nil {
				log.Printf("warning: could not save node token: %v", err)
			}
			coordCred = natsserver.NodeCred{Username: ident.NodeID, Password: joinResult.Token}
		} else {
			// Primary coordinator: generate coordinator credential if not present.
			coordSecret := nodeident.LoadToken(cfg.DataDir, "coord.secret")
			if coordSecret == "" {
				coordSecret, err = nodeident.RandomToken(32)
				if err != nil {
					return nil, fmt.Errorf("generate coordinator secret: %w", err)
				}
				if err := nodeident.SaveToken(cfg.DataDir, "coord.secret", coordSecret); err != nil {
					return nil, fmt.Errorf("save coordinator secret: %w", err)
				}
			}
			clusterSecret = nodeident.LoadToken(cfg.DataDir, "cluster.secret")
			if clusterSecret == "" {
				clusterSecret, err = nodeident.RandomToken(32)
				if err != nil {
					return nil, fmt.Errorf("generate cluster secret: %w", err)
				}
				if err := nodeident.SaveToken(cfg.DataDir, "cluster.secret", clusterSecret); err != nil {
					return nil, fmt.Errorf("save cluster secret: %w", err)
				}
			}
			coordCred = natsserver.NodeCred{Username: "__coord__", Password: coordSecret}
			clusterRoutes = cfg.Routes
		}
	}

	// Start embedded NATS (coordinator mode only).
	var embeddedNATS *natsserver.EmbeddedServer
	if cfg.EmbedNATS {
		embeddedNATS, err = natsserver.Start(cfg.NATSPort, cfg.NATSStore, coordCred, natsserver.ClusterConfig{
			Name:   cfg.ClusterName,
			Port:   cfg.ClusterPort,
			Secret: clusterSecret,
			Routes: clusterRoutes,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to start embedded NATS: %w", err)
		}
		cfg.NatsURL = embeddedNATS.ClientURL()
	}

	// Worker-only nodes that have no token yet must request join approval.
	var natsUserInfo nats.Option
	if !cfg.EmbedNATS && cfg.Client.Enabled {
		if cfg.JoinURL != "" {
			token := nodeident.LoadToken(cfg.DataDir, "node.token")
			if token == "" {
				joinResult, err := requestAndWaitForApproval(cfg, ident, joinmgr.RoleWorker)
				if err != nil {
					return nil, err
				}
				token = joinResult.Token
				if err := nodeident.SaveToken(cfg.DataDir, "node.token", token); err != nil {
					log.Printf("warning: could not save node token: %v", err)
				}
				// Also update the NATS URL from the join result if provided.
				if joinResult.CoordURL != "" {
					cfg.NatsURL = joinResult.CoordURL
				}
			}
			natsUserInfo = nats.UserInfo(ident.NodeID, token)
		}
	}

	// Connect to NATS.
	log.Printf("connecting to NATS at %s (node: %s)", cfg.NatsURL, ident.NodeID)
	connectOpts := []nats.Option{
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * nats.DefaultReconnectWait),
	}
	if natsUserInfo != nil {
		connectOpts = append(connectOpts, natsUserInfo)
	} else if cfg.EmbedNATS {
		// Coordinator connects with its own credential.
		connectOpts = append(connectOpts, nats.UserInfo(coordCred.Username, coordCred.Password))
	}

	nc, err := nats.Connect(cfg.NatsURL, connectOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	var coord *coordinator.Coordinator
	if cfg.Server.Enabled {
		coord, err = coordinator.NewCoordinatorWithConn(nc, cfg.Replicas, embeddedNATS)
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("failed to initialize coordinator: %w", err)
		}
		coord.SetDataDir(cfg.DataDir)

		// Load or generate the admin token used to protect admin HTTP endpoints.
		adminToken := nodeident.LoadToken(cfg.DataDir, "admin.token")
		if adminToken == "" {
			adminToken, err = nodeident.RandomToken(32)
			if err != nil {
				nc.Close()
				return nil, fmt.Errorf("generate admin token: %w", err)
			}
			if err := nodeident.SaveToken(cfg.DataDir, "admin.token", adminToken); err != nil {
				log.Printf("warning: could not save admin token: %v", err)
			}
			fmt.Printf("\n[edgegrid] ✦ admin token (save this): %s\n\n", adminToken)
		} else {
			fmt.Printf("[edgegrid] admin token loaded from %s/admin.token\n", cfg.DataDir)
		}
		coord.SetAdminToken(adminToken)

		if err := coord.EnsureStream(); err != nil {
			nc.Close()
			return nil, fmt.Errorf("failed to initialize coordinator stream: %w", err)
		}
	}

	var workerAgent *worker.Worker
	if cfg.Client.Enabled {
		var execInstance executor.Executor
		switch cfg.Client.Executor {
		case "training":
			js, err := nc.JetStream()
			if err != nil {
				nc.Close()
				return nil, fmt.Errorf("failed to init JetStream for log publishing: %w", err)
			}
			execInstance = executor.NewTrainingExecutor(func(jobID, line string) {
				if _, err := js.Publish("jobs.logs."+jobID, []byte(line)); err != nil {
					log.Printf("failed to publish log line for job %s: %v", jobID, err)
				}
			})
		case "mock", "":
			execInstance = executor.NewMockExecutor()
		default:
			nc.Close()
			return nil, fmt.Errorf("unknown executor type %q — valid options: training, mock", cfg.Client.Executor)
		}

		workerAgent, err = worker.NewWorkerWithConn(
			nc,
			cfg.Client.WorkerID,
			execInstance,
			cfg.Replicas,
			cfg.Client.RequireApproval,
		)
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("failed to initialize worker: %w", err)
		}
	}

	return &Agent{
		cfg:         cfg,
		natsConn:    nc,
		natsServer:  embeddedNATS,
		coordinator: coord,
		worker:      workerAgent,
	}, nil
}

// requestAndWaitForApproval submits a join request to the coordinator and polls
// until the admin approves or rejects it. Blocks until resolved.
func requestAndWaitForApproval(cfg *config.Config, ident *nodeident.Identity, role string) (*joinmgr.JoinRequest, error) { //nolint:unparam
	hostname, _ := os.Hostname()
	reqBody, _ := json.Marshal(map[string]string{
		"node_id":  ident.NodeID,
		"role":     role,
		"hostname": hostname,
	})

	joinURL := cfg.JoinURL
	submitURL := joinURL + "/join"
	pollURL := fmt.Sprintf("%s/join/%s", joinURL, ident.NodeID)

	// Submit the join request.
	resp, err := http.Post(submitURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("join request to %s failed: %w", submitURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != 409 { // 409 = already pending, OK
		return nil, fmt.Errorf("join request rejected with status %d", resp.StatusCode)
	}

	fmt.Printf("\n[edgegrid] join request submitted\n")
	fmt.Printf("  node id : %s\n", ident.NodeID)
	fmt.Printf("  role    : %s\n", role)
	if cfg.DashboardURL != "" {
		fmt.Printf("\n  ➜  claim your node (link GitHub account):\n")
		fmt.Printf("     %s/claim/%s\n", strings.TrimRight(cfg.DashboardURL, "/"), ident.NodeID)
	}
	fmt.Printf("\n  waiting for admin approval...\n\n")

	// Poll until approved or rejected.
	for {
		time.Sleep(5 * time.Second)

		r, err := http.Get(pollURL)
		if err != nil {
			log.Printf("polling join status: %v (retrying...)", err)
			continue
		}

		var result joinmgr.JoinRequest
		if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
			r.Body.Close()
			continue
		}
		r.Body.Close()

		switch result.Status {
		case joinmgr.StatusApproved:
			fmt.Printf("[edgegrid] join approved — connecting...\n")
			return &result, nil
		case joinmgr.StatusRejected:
			return nil, fmt.Errorf("join request rejected by admin")
		default:
			fmt.Printf("[edgegrid] still pending approval...\n")
		}
	}
}

func (a *Agent) Start(ctx context.Context) error {
	log.Println("starting EdgeGrid services")

	if a.coordinator != nil {
		go func() {
			if err := a.coordinator.Start(ctx, a.cfg.Server.Port); err != nil {
				log.Printf("coordinator stopped: %v", err)
			}
		}()
	}

	if a.worker != nil {
		go func() {
			if err := a.worker.Start(ctx); err != nil {
				log.Printf("worker stopped: %v", err)
			}
		}()
	}

	<-ctx.Done()
	return nil
}

func (a *Agent) Close() {
	log.Println("shutting down EdgeGrid services")
	if a.worker != nil {
		a.worker.Close()
	}
	if a.natsConn != nil {
		a.natsConn.Close()
		log.Println("closed NATS connection")
	}
	if a.natsServer != nil {
		a.natsServer.Shutdown()
	}
}
