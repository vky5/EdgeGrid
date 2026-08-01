// Package natsserver starts an embedded NATS server inside the coordinator process
// so operators don't need to install or manage a separate NATS binary.
// JetStream is enabled and state is persisted to a configurable directory so
// that stream and KV data survive coordinator restarts.
package natsserver

import (
	"fmt"
	"log"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// ClusterUsername is the fixed username every cluster route connection
// authenticates as — shared with agent.go's self-route default so both
// agree on it rather than each hardcoding the same literal.
const ClusterUsername = "cluster"

// ClusterConfig holds optional intra-cluster settings.
type ClusterConfig struct {
	Name   string   // must match the cluster name of other nodes
	Port   int      // coordinator own port to connect to
	Secret string   // shared password for cluster route connections
	Routes []string // seed URLs, e.g. ["nats://blacktree.in:6222"]
}

// NodeCred is a username/password pair for one approved node.
type NodeCred struct {
	Username string
	Password string
}

type EmbeddedServer struct {
	mu            sync.Mutex
	ns            *server.Server
	baseOpts      *server.Options // base options kept for reload
	advertiseHost string          // externally-reachable host, if configured; raw, no port
}

// Start launches an embedded NATS server with JetStream enabled.
// coordCred is the coordinator's own NATS credential (always allowed).
// cluster is optional; if Routes is non-empty the server joins a cluster.
// advertiseHost, if set, is what this server tells clients/peers to use
// instead of its own bind address — see AdvertiseHost. serverName must be
// unique across the cluster — JetStream requires it once clustering is
// configured at all (opts.Cluster is always set now that the cluster
// listener always binds, even for a lone node with no routes yet). logFile,
// if set, is where nats-server's own internal logger writes — it has its
// own logging system entirely separate from Go's log package, so without
// this it writes straight to stdout regardless of nodelog.Setup, which
// both loses it from `edgegrid logs`/`/logs` and corrupts a bubbletea
// alt-screen TUI's rendering (two things fighting to control the terminal).
func Start(port int, storeDir string, coordCred NodeCred, cluster ClusterConfig, advertiseHost, serverName, logFile string) (*EmbeddedServer, error) {
	opts := buildOpts(port, storeDir, coordCred, cluster, advertiseHost, serverName, nil)
	if logFile != "" {
		opts.LogFile = logFile
	}

	ns, err := server.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedded NATS server: %w", err)
	}

	ns.ConfigureLogger()
	go ns.Start()

	if !ns.ReadyForConnections(10 * time.Second) {
		return nil, fmt.Errorf("embedded NATS server did not become ready within 10s")
	}

	log.Printf("embedded NATS server started on port %d (store: %s)", port, storeDir)
	if len(cluster.Routes) > 0 {
		log.Printf("NATS cluster %q listening on port %d for inbound routes", opts.Cluster.Name, opts.Cluster.Port)
		log.Printf("NATS cluster %q joining routes: %v", cluster.Name, cluster.Routes)
	} else {
		log.Printf("running standalone (no cluster routes) — JetStream runs in single-node mode")
	}

	return &EmbeddedServer{ns: ns, baseOpts: opts, advertiseHost: advertiseHost}, nil
}

// AdvertiseHost returns the externally-reachable host configured for this
// server (empty if none was set) — the single source of truth for what
// address to hand out to joining nodes, so callers don't keep their own
// separate copy of the same value.
func (e *EmbeddedServer) AdvertiseHost() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.advertiseHost
}

// ClientPort returns the port this server's own NATS clients connect to —
// the single source of truth for join responses instead of assuming the
// default (see joinapi.Approve, which used to hardcode 4222).
func (e *EmbeddedServer) ClientPort() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.baseOpts.Port
}

// ClusterPort returns the cluster route port, or 0 if clustering isn't
// configured on this server (see joinapi.Approve, which used to hardcode
// 6222).
func (e *EmbeddedServer) ClusterPort() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.baseOpts.Cluster.Port
}

// AddUser adds a new approved node credential and hot-reloads the NATS server.
// Safe to call concurrently.
func (e *EmbeddedServer) AddUser(cred NodeCred) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Copy and append the new user.
	newUsers := make([]*server.User, len(e.baseOpts.Users), len(e.baseOpts.Users)+1)
	copy(newUsers, e.baseOpts.Users)
	newUsers = append(newUsers, &server.User{
		Username: cred.Username,
		Password: cred.Password,
	})

	newOpts := *e.baseOpts
	newOpts.Users = newUsers

	if err := e.ns.ReloadOptions(&newOpts); err != nil {
		return fmt.Errorf("NATS reload after adding user %s: %w", cred.Username, err)
	}
	e.baseOpts = &newOpts
	log.Printf("NATS: added credential for node %s", cred.Username)
	return nil
}

// SetUsers replaces the full approved user list and hot-reloads NATS.
// Used on startup to restore previously approved nodes from KV.
func (e *EmbeddedServer) SetUsers(coordCred NodeCred, nodeCreds []NodeCred) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	users := credsToUsers(coordCred, nodeCreds)
	newOpts := *e.baseOpts
	newOpts.Users = users

	if err := e.ns.ReloadOptions(&newOpts); err != nil {
		return fmt.Errorf("NATS reload (set users): %w", err)
	}
	e.baseOpts = &newOpts
	return nil
}

// Shutdown gracefully stops the embedded NATS server.
func (e *EmbeddedServer) Shutdown() {
	if e.ns != nil {
		e.ns.Shutdown()
		log.Println("embedded NATS server stopped")
	}
}

// ClientURL returns the address this same process uses to connect back to
// its own just-started embedded server (workers instead get theirs from a
// join/approve response's coordURL, built from AdvertiseHost — see
// joinapi.go). nats-server's own ClientURL() reflects the bind address,
// which defaults to the 0.0.0.0 wildcard — never a valid target to
// connect *to*, and specifically fragile here since every dial in this
// codebase is routed through tsnet's userspace network stack, which has
// to fall through to a plain OS dial for a non-tailscale address like
// that. Loopback is always correct for a same-process self-connection.
func (e *EmbeddedServer) ClientURL() string {
	u := e.ns.ClientURL()
	if parsed, err := url.Parse(u); err == nil {
		host, port, splitErr := net.SplitHostPort(parsed.Host)
		if splitErr == nil && (host == "0.0.0.0" || host == "") {
			parsed.Host = net.JoinHostPort("127.0.0.1", port)
			return parsed.String()
		}
	}
	return u
}

func buildOpts(
	port int,
	storeDir string,
	coordCred NodeCred,
	cluster ClusterConfig,
	advertiseHost, serverName string,
	extraUsers []*server.User,
) *server.Options {
	users := credsToUsers(coordCred, nil)
	if len(extraUsers) > 0 {
		users = append(users, extraUsers...)
	}

	opts := &server.Options{
		Port:       port,
		ServerName: serverName,
		JetStream:  true,
		StoreDir:   storeDir,
		HTTPPort:   -1,
		NoSigs:     true,
		Users:      users,
	}
	if advertiseHost != "" { // when set, tells workers (connect to this address)
		opts.ClientAdvertise = advertiseHost // applies even without clustering
	}

	// Cluster.Port==0 is what nats-server's standAloneMode() checks — only
	// set it when there's an actual peer to route to. JetStream refuses to
	// do anything useful (including R1 stream/KV creation) once clustered
	// with zero real peers: a route pointing at yourself satisfies the
	// static "configuredRoutes() > 0" check but nats-server always closes
	// a self-route as a duplicate at runtime, so routing (and JetStream)
	// never stabilizes. A lone coordinator must stay standalone.
	if len(cluster.Routes) > 0 {
		clusterPort := cluster.Port
		if clusterPort == 0 {
			clusterPort = 6222
		}
		clusterName := cluster.Name
		if clusterName == "" {
			clusterName = "edgegrid"
		}
		opts.Cluster = server.ClusterOpts{
			Name:     clusterName,
			Port:     clusterPort,
			Username: ClusterUsername,
			Password: cluster.Secret,
		}
		if advertiseHost != "" {
			opts.Cluster.Advertise = advertiseHost // what other coordinators should connect to on exchange of INFO
		}

		routes := make([]*url.URL, 0, len(cluster.Routes))
		for _, r := range cluster.Routes {
			u, err := url.Parse(r)
			if err != nil {
				log.Printf("NATS: invalid route URL %q, skipping: %v", r, err)
				continue
			}
			routes = append(routes, u)
		}
		opts.Routes = routes // initial seed routes for cluster discovery
	}

	return opts
}

// NodeCred -> Server.user(nats type)
func credsToUsers(coordCred NodeCred, nodeCreds []NodeCred) []*server.User {
	users := []*server.User{
		{Username: coordCred.Username, Password: coordCred.Password},
	}
	for _, c := range nodeCreds {
		users = append(users, &server.User{Username: c.Username, Password: c.Password})
	}
	return users
}
