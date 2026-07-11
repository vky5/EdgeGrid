package coordinator

import (
	"context"
	"fmt"
	"log"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/coordinator/workerman"
	"github.com/edgegrid/edgegrid/internal/joinmgr"
	"github.com/edgegrid/edgegrid/internal/natsserver"
	"github.com/edgegrid/edgegrid/internal/usermgr"
	"github.com/nats-io/nats.go"
)

type Coordinator struct {
	jsBroker   *broker.Broker           // Broker with nats.conn, jetstream and replicas
	manager    *workerman.WorkerManager // nats KV store
	joinMgr    *joinmgr.Manager
	userMgr    *usermgr.Manager
	natsServer *natsserver.EmbeddedServer
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

	jm, err := joinmgr.New(jsBroker)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize join manager: %w", err)
	}

	um, err := usermgr.New(jsBroker)
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

	// Watch approved node credentials and live-apply them to embedded NATS.
	if c.natsServer != nil {
		if err := c.watchApprovedNodes(ctx); err != nil {
			log.Printf("warning: could not start node_auth watch: %v", err)
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

// watchApprovedNodes applies every node_auth entry — past and future — to
// this coordinator's embedded NATS, so approvals from sibling coordinators
// propagate live instead of only at restart.
func (c *Coordinator) watchApprovedNodes(ctx context.Context) error {
	kv, err := c.jsBroker.GetOrCreateKV("node_auth", 0) // no TTL — permanent
	if err != nil {
		return err
	}

	watcher, err := kv.WatchAll()
	if err != nil {
		return fmt.Errorf("watch node_auth: %w", err)
	}

	go func() {
		defer watcher.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case entry, ok := <-watcher.Updates():
				if !ok {
					return
				}
				if entry == nil {
					continue // marks end of initial replay, nothing to apply
				}
				if entry.Operation() != nats.KeyValuePut {
					continue // deletion/purge: no revoke support yet
				}
				cred := natsserver.NodeCred{Username: entry.Key(), Password: string(entry.Value())}
				if err := c.natsServer.AddUser(cred); err != nil {
					log.Printf("warning: could not apply approved credential for %s: %v", entry.Key(), err)
				}
			}
		}
	}()

	log.Println("watching node_auth for approved node credentials")
	return nil
}
