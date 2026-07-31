package agent

import (
	"fmt"
	"log"
	"strings"

	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/joinmgr"
	"github.com/edgegrid/edgegrid/internal/natsserver"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"github.com/edgegrid/edgegrid/internal/nodelog"
	"tailscale.com/tsnet"
)

// clusterRoutesSep joins/splits the cluster.routes file — a node only ever
// gets one seed route at approval time (see known-gaps.md #8), but the
// slice shape is kept for symmetry with joinResult.ClusterRoutes and in
// case that changes later.
const clusterRoutesSep = ","

// resolveNATSCredential resolves this node's NATS username/password: primary
// (self-generated), non-primary (--join, approved), or worker (--join,
// approved) — exactly one applies. clusterSecret/Routes are coordinator-only;
// a worker never joins as a route peer.
func resolveNATSCredential(ts *tsnet.Server, cfg *config.Config, ident *nodeident.Identity) (natsserver.NodeCred, string, []string, error) {
	// set by exactly one branch below.
	var natsCred natsserver.NodeCred
	var clusterSecret string
	var clusterRoutes []string
	var err error

	// coordinator: join+approve if non-primary, else self-generate.
	if cfg.EmbedNATS {
		if cfg.JoinURL != "" {
			// Reuse saved credentials if this node has already been
			// approved — a coordinator-role restart used to unconditionally
			// re-request approval every time, which meant it couldn't come
			// back up at all unless the coordinator it originally joined
			// happened to be reachable at that exact moment (and, before
			// requestAndWaitForApproval had a submit timeout, could hang
			// indefinitely with no error at all). A worker already skips
			// re-joining the same way once it has a token — this brings the
			// coordinator branch in line with that, using cluster.routes
			// (new: persisted alongside node.token/cluster.secret) to avoid
			// needing a live join round-trip just to learn the seed peer
			// again.
			token := nodeident.LoadToken(cfg.DataDir, "node.token")
			savedClusterSecret := nodeident.LoadToken(cfg.DataDir, "cluster.secret")
			if token != "" && savedClusterSecret != "" {
				clusterSecret = savedClusterSecret
				if savedRoutes := nodeident.LoadToken(cfg.DataDir, "cluster.routes"); savedRoutes != "" {
					clusterRoutes = strings.Split(savedRoutes, clusterRoutesSep)
				}
				natsCred = natsserver.NodeCred{Username: ident.NodeID, Password: token}
			} else {
				// non-primary: join, wait for approval. (for secondary coorddinator or secondary coordiantor + worker)
				joinResult, err := requestAndWaitForApproval(ts, cfg, ident, joinmgr.RoleServer)
				if err != nil {
					return natsserver.NodeCred{}, "", nil, err
				}
				clusterSecret = joinResult.ClusterSecret
				clusterRoutes = joinResult.ClusterRoutes
				// save token as coord cred.
				if err := nodeident.SaveToken(cfg.DataDir, "node.token", joinResult.Token); err != nil {
					log.Printf("warning: could not save node token: %v", err)
				}
				if err := nodeident.SaveToken(cfg.DataDir, "cluster.secret", joinResult.ClusterSecret); err != nil {
					log.Printf("warning: could not save cluster secret: %v", err)
				}
				if err := nodeident.SaveToken(cfg.DataDir, "cluster.routes", strings.Join(joinResult.ClusterRoutes, clusterRoutesSep)); err != nil {
					log.Printf("warning: could not save cluster routes: %v", err)
				}
				natsCred = natsserver.NodeCred{Username: ident.NodeID, Password: joinResult.Token} // nodeID as username, received token as password
			}
		} else {
			// primary: generate if missing.
			coordSecret := nodeident.LoadToken(cfg.DataDir, "coord.secret")
			if coordSecret == "" {
				coordSecret, err = nodeident.RandomToken(32)
				if err != nil {
					return natsserver.NodeCred{}, "", nil, fmt.Errorf("generate coordinator secret: %w", err)
				}
				if err := nodeident.SaveToken(cfg.DataDir, "coord.secret", coordSecret); err != nil {
					return natsserver.NodeCred{}, "", nil, fmt.Errorf("save coordinator secret: %w", err)
				}
			}
			clusterSecret = nodeident.LoadToken(cfg.DataDir, "cluster.secret")
			if clusterSecret == "" {
				clusterSecret, err = nodeident.RandomToken(32)
				if err != nil {
					return natsserver.NodeCred{}, "", nil, fmt.Errorf("generate cluster secret: %w", err)
				}
				if err := nodeident.SaveToken(cfg.DataDir, "cluster.secret", clusterSecret); err != nil {
					return natsserver.NodeCred{}, "", nil, fmt.Errorf("save cluster secret: %w", err)
				}
			}
			natsCred = natsserver.NodeCred{Username: "__coord__", Password: coordSecret}
			clusterRoutes = cfg.Routes
		}
	}

	// worker: load credentials or join if missing.
	if !cfg.EmbedNATS && cfg.Client.Enabled {
		if savedNatsURL := nodeident.LoadToken(cfg.DataDir, "nats.url"); savedNatsURL != "" {
			cfg.NatsURL = savedNatsURL
		}

		token := nodeident.LoadToken(cfg.DataDir, "node.token")
		if token == "" && cfg.JoinURL != "" {
			joinResult, err := requestAndWaitForApproval(ts, cfg, ident, joinmgr.RoleWorker)
			if err != nil {
				return natsserver.NodeCred{}, "", nil, err
			}
			token = joinResult.Token
			if err := nodeident.SaveToken(cfg.DataDir, "node.token", token); err != nil {
				log.Printf("warning: could not save node token: %v", err)
			}
			if joinResult.CoordURL != "" {
				cfg.NatsURL = joinResult.CoordURL
				if err := nodeident.SaveToken(cfg.DataDir, "nats.url", joinResult.CoordURL); err != nil {
					log.Printf("warning: could not save NATS URL: %v", err)
				}
			}
		}
		if token != "" {
			natsCred = natsserver.NodeCred{Username: ident.NodeID, Password: token}
		}
	}

	return natsCred, clusterSecret, clusterRoutes, nil
}

// startEmbeddedNATS boots this node's own NATS/JetStream server. Coordinator-only.
func startEmbeddedNATS(cfg *config.Config, natsCred natsserver.NodeCred, clusterSecret string, clusterRoutes []string) (*natsserver.EmbeddedServer, error) {
	embeddedNATS, err := natsserver.Start(
		cfg.NATSPort,
		cfg.NATSStore,
		natsCred,
		natsserver.ClusterConfig{
			Name:   cfg.ClusterName,
			Port:   cfg.ClusterPort,
			Secret: clusterSecret, // shared across all peers
			Routes: clusterRoutes, // seed peer(s) for cluster routing
		},
		cfg.AdvertiseHost,
		cfg.TailscaleHostname, // must be unique across the cluster; JetStream requires it once Cluster is set
		nodelog.Path(cfg.DataDir))
	if err != nil {
		return nil, fmt.Errorf("failed to start embedded NATS: %w", err)
	}
	cfg.NatsURL = embeddedNATS.ClientURL() // real address, replaces config.go placeholder
	return embeddedNATS, nil
}
