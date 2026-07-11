// This file is the HTTP layer's front door: middleware, auth gating, and the
// route table. It deliberately contains no NATS/JetStream/KV calls itself —
// every route delegates to a handler in one of the sibling packages
// (jobsapi, workersapi, joinapi, usersapi), which is where that logic lives.
package coordinator

import (
	"crypto/subtle"
	"log"
	"net/http"
	"strings"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/coordinator/jobsapi"
	"github.com/edgegrid/edgegrid/internal/coordinator/joinapi"
	"github.com/edgegrid/edgegrid/internal/coordinator/usersapi"
	"github.com/edgegrid/edgegrid/internal/coordinator/workerman"
	"github.com/edgegrid/edgegrid/internal/coordinator/workersapi"
	"github.com/edgegrid/edgegrid/internal/joinmgr"
	"github.com/edgegrid/edgegrid/internal/usermgr"
)

// corsMiddleware adds CORS headers for preflight requests. In normal operation
// the browser never talks to the coordinator directly — all dashboard traffic
// is proxied through the Next.js backend — so this only smooths over any stray
// same-machine tooling during development.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireGateway authenticates every request against the shared backend token
// before it reaches any handler. The token proves the request came from the
// trusted Next.js backend, which has already authenticated the end user (GitHub
// session) and applied per-user/admin authorization. The coordinator is not
// meant to be reached directly by browsers or arbitrary network clients.
//
// A small set of bootstrap endpoints stay open because the caller has no
// credential yet: /health, and the node join submit/poll endpoints a
// not-yet-approved node uses before it receives its NATS token.
func requireGateway(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || isOpenPath(r) {
			next.ServeHTTP(w, r)
			return
		}
		if token == "" {
			http.Error(w, "coordinator API token not configured", http.StatusServiceUnavailable)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isOpenPath reports whether a request may proceed without the backend token.
// These are the node bootstrap endpoints (a joining node has no credential yet)
// plus the health check.
func isOpenPath(r *http.Request) bool {
	p := r.URL.Path
	if p == "/health" {
		return true
	}
	// POST /join — a node submits a join request before it has any credential.
	if p == "/join" && r.Method == http.MethodPost {
		return true
	}
	// GET /join/{nodeID} — a pending node polls for its approval status.
	// Excludes POST /join/claim/{nodeID}, which is called by the trusted backend.
	if r.Method == http.MethodGet && strings.HasPrefix(p, "/join/") && !strings.HasPrefix(p, "/join/claim/") {
		return true
	}
	return false
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func StartHTTPServer(addr string, jsBroker *broker.Broker, manager *workerman.WorkerManager, jm *joinmgr.Manager, um *usermgr.Manager, dataDir string, adminToken string) {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", handleHealth)

	// GET /workers — list all registered workers and their current state
	mux.HandleFunc("/workers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		workersapi.List(w, r, manager)
	})

	// GET /jobs  — list all jobs (strips request_proto from response, too large)
	// POST /jobs — submit a training job
	mux.HandleFunc("/jobs", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			jobsapi.List(w, r, jsBroker)
		case http.MethodPost:
			jobsapi.Submit(w, r, jsBroker, manager)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	// /jobs/{id}           → GET job status
	// /jobs/{id}/upload    → POST dataset upload
	// /jobs/{id}/artifact  → GET checkpoint download
	mux.HandleFunc("/jobs/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/jobs/")
		parts := strings.SplitN(path, "/", 2)

		jobID := parts[0]
		if jobID == "" {
			http.Error(w, "job_id is required", http.StatusBadRequest)
			return
		}

		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				jobsapi.Status(w, r, jsBroker, jobID)
			case http.MethodDelete:
				jobsapi.Cancel(w, r, jsBroker, jobID)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}

		switch parts[1] {
		case "upload":
			jobsapi.Upload(w, r, jsBroker, manager, jobID)
		case "artifact":
			jobsapi.Download(w, r, jsBroker, jobID)
		case "logs":
			jobsapi.Logs(w, r, jsBroker, jobID)
		case "approve":
			jobsapi.Decision(w, r, jsBroker, jobID, "approve")
		case "reject":
			jobsapi.Decision(w, r, jsBroker, jobID, "reject")
		default:
			http.NotFound(w, r)
		}
	})

	// POST /join          — submit a join request (no auth required)
	// GET  /join/{nodeID} — poll join status (no auth required)
	mux.HandleFunc("/join", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		joinapi.Submit(w, r, jm)
	})
	mux.HandleFunc("/join/", func(w http.ResponseWriter, r *http.Request) {
		nodeID := strings.TrimPrefix(r.URL.Path, "/join/")
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		joinapi.Status(w, nodeID, jm)
	})

	// POST /admin/join/{nodeID}/approve|reject
	// Admin-ness is enforced by the Next.js backend (GitHub session + isAdmin);
	// the backend token gate on all routes keeps this unreachable otherwise.
	mux.HandleFunc("/admin/join/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/admin/join/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 {
			http.Error(w, "path must be /admin/join/{nodeID}/approve|reject", http.StatusBadRequest)
			return
		}
		nodeID, action := parts[0], parts[1]
		switch action {
		case "approve":
			joinapi.Approve(w, r, nodeID, jm, um, jsBroker, dataDir)
		case "reject":
			joinapi.Reject(w, nodeID, jm)
		default:
			http.NotFound(w, r)
		}
	})

	// GET  /admin/users               — list everyone with dashboard access
	// POST /admin/users/{username}/approve — grant dashboard access directly, no node required
	mux.HandleFunc("/admin/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		usersapi.List(w, r, um)
	})
	mux.HandleFunc("/admin/users/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/admin/users/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[1] != "approve" {
			http.NotFound(w, r)
			return
		}
		usersapi.Approve(w, parts[0], um)
	})

	// GET /users/{username}/status — does this GitHub user have dashboard access?
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/users/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[1] != "status" || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		usersapi.Status(w, parts[0], um)
	})

	// GET /admin/join — list all join requests
	mux.HandleFunc("/admin/join", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		joinapi.List(w, r, jm)
	})

	// POST /join/{nodeID}/claim — link a GitHub username to a pending join request
	mux.HandleFunc("/join/claim/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		nodeID := strings.TrimPrefix(r.URL.Path, "/join/claim/")
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		joinapi.Claim(w, r, nodeID, jm, um)
	})

	log.Printf("starting HTTP job API on %s", addr)
	if err := http.ListenAndServe(addr, corsMiddleware(requireGateway(adminToken, mux))); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server failed: %v", err)
	}
}
