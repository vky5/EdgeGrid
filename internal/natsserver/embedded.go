// Package natsserver starts an embedded NATS server inside the coordinator
// process so operators don't need to install or manage a separate NATS binary.
// JetStream is enabled and state is persisted to a configurable directory so
// that stream and KV data survive coordinator restarts.
package natsserver

import (
	"fmt"
	"log"
	"net/url"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

// ClusterConfig holds optional intra-cluster settings. Leave Routes empty for
// a single-node (dev) deployment; populate it for a multi-server cluster.
type ClusterConfig struct {
	Name   string   // must be identical on every node in the cluster
	Port   int      // gossip port, default 6222
	Routes []string // seed URLs, e.g. ["nats://blacktree.in:6222"]
}

type EmbeddedServer struct {
	ns *server.Server
}

// Start launches an embedded NATS server with JetStream enabled.
// port is the NATS client port (default 4222).
// storeDir is where JetStream persists streams and KV data.
// cluster is optional; if Routes is non-empty the server joins a cluster.
func Start(port int, storeDir string, cluster ClusterConfig) (*EmbeddedServer, error) {
	opts := &server.Options{
		Port:      port,
		JetStream: true,
		StoreDir:  storeDir,
		HTTPPort:  -1,
		NoLog:     false,
		NoSigs:    true,
	}

	if len(cluster.Routes) > 0 {
		clusterPort := cluster.Port
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
				return nil, fmt.Errorf("invalid route URL %q: %w", r, err)
			}
			routes = append(routes, u)
		}

		opts.Cluster = server.ClusterOpts{
			Name: clusterName,
			Port: clusterPort,
		}
		opts.Routes = routes
		log.Printf("NATS cluster %q enabled on port %d, routes: %v", clusterName, clusterPort, cluster.Routes)
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
	return &EmbeddedServer{ns: ns}, nil
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
