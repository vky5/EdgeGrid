package coordinator

import (
	"context"
	"fmt"
	"log"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/coordinator/workerman"
	"github.com/edgegrid/edgegrid/internal/joinmgr"
	"github.com/edgegrid/edgegrid/internal/natsserver"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"github.com/edgegrid/edgegrid/internal/usermgr"
	"github.com/nats-io/nats.go"
)

type Coordinator struct {
	jsBroker   *broker.Broker // Broker with nats.conn, jetstream and replicas
	manager    *workerman.WorkerManager // nats KV store
	joinMgr    *joinmgr.Manager
	userMgr    *usermgr.Manager
	natsServer *natsserver.EmbeddedServer // nil for non-primary coordinators
	dataDir    string
	adminToken string
}

func NewCoordinatorWithConn(nc *nats.Conn, replicas int, ns *natsserver.EmbeddedServer) (*Coordinator, error) {
	jsBroker, err := broker.NewBroker(nc, replicas)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize shared broker: %w", err)
	}

	manager, err := workerman.NewWorkerManager(jsBroker)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize worker manager: %w", err)
	}

	jm, err := joinmgr.New(jsBroker.JS)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize join manager: %w", err)
	}

	um, err := usermgr.New(jsBroker.JS)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize user manager: %w", err)
	}

	return &Coordinator{
		jsBroker:   jsBroker,
		manager:    manager,
		joinMgr:    jm,
		userMgr:    um,
		natsServer: ns,
	}, nil
}

func (c *Coordinator) SetDataDir(dir string) {
	c.dataDir = dir
}

func (c *Coordinator) SetAdminToken(token string) {
	c.adminToken = token
}

func (c *Coordinator) EnsureStream() error {
	return c.jsBroker.EnsureStream()
}

func (c *Coordinator) Start(ctx context.Context, apiAddr string) error {
	log.Println("starting coordinator")

	if err := c.jsBroker.EnsureStream(); err != nil {
		return fmt.Errorf("failed to verify/ensure NATS Stream: %w", err)
	}

	// Restore previously approved node credentials into embedded NATS.
	if c.natsServer != nil {
		if err := c.restoreApprovedNodes(); err != nil {
			log.Printf("warning: could not restore approved nodes into NATS: %v", err)
		}
	}

	log.Println("worker manager initialized")

	if err := c.SubscribeToWorkerEvents(ctx); err != nil { // registration and heartbeat
		return fmt.Errorf("failed to subscribe to worker NATS events: %w", err)
	}

	if err := c.SubscribeToResults(ctx); err != nil {
		return fmt.Errorf("failed to subscribe to job results: %w", err)
	}

	if err := c.SubscribeToRejections(ctx); err != nil {
		return fmt.Errorf("failed to subscribe to worker rejections: %w", err)
	}

	if err := c.SubscribeToWorkerStats(); err != nil {
		return fmt.Errorf("failed to subscribe to worker stats: %w", err)
	}

	go c.StartStaleJobRecovery(ctx)
	go StartHTTPServer(apiAddr, c.jsBroker, c.manager, c.joinMgr, c.userMgr, c.natsServer, c.dataDir, c.adminToken)

	<-ctx.Done()
	log.Println("shutting down coordinator")
	return nil
}

// restoreApprovedNodes reads the node_auth KV and loads all previously approved
// node credentials back into the embedded NATS server so workers can reconnect
// after a coordinator restart without needing re-approval.
func (c *Coordinator) restoreApprovedNodes() error {
	kv, err := c.jsBroker.GetOrCreateKV("node_auth", 0) // no TTL — permanent
	if err != nil {
		return err
	}

	keys, err := kv.Keys()
	if err != nil {
		return nil // empty KV is fine
	}

	coordSecret := nodeident.LoadToken(c.dataDir, "coord.secret")
	coordCred := natsserver.NodeCred{Username: "__coord__", Password: coordSecret}

	var nodeCreds []natsserver.NodeCred
	for _, key := range keys {
		entry, err := kv.Get(key)
		if err != nil {
			continue
		}
		nodeCreds = append(nodeCreds, natsserver.NodeCred{
			Username: key,
			Password: string(entry.Value()),
		})
	}

	if err := c.natsServer.SetUsers(coordCred, nodeCreds); err != nil {
		return err
	}
	log.Printf("restored %d approved node credential(s) into NATS", len(nodeCreds))
	return nil
}
