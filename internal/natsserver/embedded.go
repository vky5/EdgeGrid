// Package natsserver starts an embedded NATS server inside the coordinator process
// so operators don't need to install or manage a separate NATS binary.
// JetStream is enabled and state is persisted to a configurable directory so
// that stream and KV data survive coordinator restarts.
package natsserver

import (
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// ClusterConfig holds optional intra-cluster settings.
type ClusterConfig struct {
	Name   string // must match the cluster name of other nodes
	Port   int // coordinator own port to connect to
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
// instead of its own bind address — see AdvertiseHost.
func Start(port int, storeDir string, coordCred NodeCred, cluster ClusterConfig, advertiseHost string) (*EmbeddedServer, error) {
	opts := buildOpts(port, storeDir, coordCred, cluster, advertiseHost, nil)

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
		log.Printf("NATS cluster %q joining routes: %v", cluster.Name, cluster.Routes)
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

// ClientURL returns the URL workers and the coordinator itself use to connect.
func (e *EmbeddedServer) ClientURL() string {
	return e.ns.ClientURL()
}

func buildOpts(
	port int,
	storeDir string,
	coordCred NodeCred,
	cluster ClusterConfig,
	advertiseHost string,
	extraUsers []*server.User,
) *server.Options {
	users := credsToUsers(coordCred, nil)
	if len(extraUsers) > 0 {
		users = append(users, extraUsers...)
	}

	opts := &server.Options{
		Port:      port,
		JetStream: true,
		StoreDir:  storeDir,
		HTTPPort:  -1,
		NoSigs:    true,
		Users:     users,
	}
	if advertiseHost != "" { // when set, tells workers (connect to this address)
		opts.ClientAdvertise = advertiseHost // applies even without clustering
	}

	if len(cluster.Routes) > 0 {
		clusterPort := cluster.Port // Server to server port
		if clusterPort == 0 {
			clusterPort = 6222
		}
		clusterName := cluster.Name
		if clusterName == "" {
			clusterName = "edgegrid"
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

		opts.Cluster = server.ClusterOpts{
			Name:     clusterName,
			Port:     clusterPort,
			Username: "cluster",
			Password: cluster.Secret,
		}
		if advertiseHost != "" {
			opts.Cluster.Advertise = advertiseHost // what other coordinators should connect to on exchange of INFO
		}
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
