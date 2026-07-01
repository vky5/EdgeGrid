// Package natsserver starts an embedded NATS server inside the coordinator
// process so operators don't need to install or manage a separate NATS binary.
// JetStream is enabled and state is persisted to a configurable directory so
// that stream and KV data survive coordinator restarts.
package natsserver

import (
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

type EmbeddedServer struct {
	ns *server.Server
}

// Start launches an embedded NATS server with JetStream enabled.
// port is the NATS client port (default 4222).
// storeDir is where JetStream persists streams and KV data.
// replicas controls the JetStream replication factor — must be 1 for a
// single embedded node (clustering is not supported in embedded mode).
func Start(port int, storeDir string) (*EmbeddedServer, error) {
	opts := &server.Options{
		Port:      port,
		JetStream: true,
		StoreDir:  storeDir,
		// Disable the NATS HTTP monitoring port to keep the process footprint small.
		HTTPPort:  -1,
		NoLog:     false,
		NoSigs:    true, // EdgeGrid handles signals, not NATS
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
