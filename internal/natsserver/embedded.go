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

// NodeCred is a username/password pair for one approved node's client connection (opts.Users).
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

// Start launches an embedded NATS server with JetStream enabled, always standalone.
// coordCred is the coordinator's own NATS credential (always allowed).
// advertiseHost, if set, is what this server tells clients/peers to use
func Start(
	port int,
	storeDir string,
	coordCred NodeCred,
	advertiseHost,
	serverName, logFile string,
) (*EmbeddedServer, error) {
	opts := buildOpts(port, storeDir, coordCred, advertiseHost, serverName, nil)
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

	log.Printf("running standalone (no cluster routes) — JetStream runs in single-node mode")

	return &EmbeddedServer{ns: ns, baseOpts: opts, advertiseHost: advertiseHost}, nil
}

// AdvertiseHost returns the externally-reachable host configured for this server (empty if none was set)
func (e *EmbeddedServer) AdvertiseHost() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.advertiseHost
}

// Port returns the actual bound client port
func (e *EmbeddedServer) Port() int {
	if tcpAddr, ok := e.ns.Addr().(*net.TCPAddr); ok {
		return tcpAddr.Port
	}
	return 0
}

// AdvertisedClientURL is the NATS URL other nodes should dial this server on:
// the configured advertise host (falling back to localhost) plus the real
// bound port. Single source of truth for what we hand out to joiners and peers.
func (e *EmbeddedServer) AdvertisedClientURL() string {
	host := e.AdvertiseHost()
	if host == "" {
		host = "localhost"
	}
	port := e.Port()
	if port == 0 {
		port = 4222
	}
	return fmt.Sprintf("nats://%s:%d", host, port)
}

// AddUser adds a new approved node credential and hot-reloads the NATS server.
// Safe to call concurrently. (Used to add newly approved nodes from watchApprovedNodes.	)
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

// Shutdown gracefully stops the embedded NATS server.
func (e *EmbeddedServer) Shutdown() {
	if e.ns != nil {
		e.ns.Shutdown()
		log.Println("embedded NATS server stopped")
	}
}

func (e *EmbeddedServer) ClientURL() string {
	// by default opts.host is 0.0.0.0 and we are not setting it up anywhere
	// other discovers the coordinator url using AdvertiseHost when they first join
	// But for worker in the same process we need to connect to the embedded nats server using loopback address (127.0.0.1)
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

// buildOpts always builds standalone options — coordinators never cluster
// their NATS servers (see docs/coordinator-peer-mesh-plan.md). JetStream KV's
// meta layer is Raft (CP); EdgeGrid needs each coordinator to stay
// independently useful (AP), so no Cluster options are ever set.
func buildOpts(
	port int,
	storeDir string,
	coordCred NodeCred,
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
		opts.ClientAdvertise = advertiseHost
	}

	return opts
}

// NodeCred -> Server.user(nats type)
func credsToUsers(coordCred NodeCred, nodeCreds []NodeCred) []*server.User {
	users := []*server.User{
		{
			Username: coordCred.Username,
			Password: coordCred.Password,
		},
	}
	for _, c := range nodeCreds {
		users = append(users, &server.User{Username: c.Username, Password: c.Password})
	}
	return users
}
