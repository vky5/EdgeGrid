package agent

import (
	"fmt"
	"log"

	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/joinmgr"
	"github.com/edgegrid/edgegrid/internal/natsserver"
	"github.com/edgegrid/edgegrid/internal/nodeident"
)

// resolveNATSCredential resolves this node's NATS username/password: primary
// (self-generated), non-primary (--join, approved), or worker (--join,
// approved) — exactly one applies. clusterSecret/Routes are coordinator-only;
// a worker never joins as a route peer.
func resolveNATSCredential(cfg *config.Config, ident *nodeident.Identity) (natsserver.NodeCred, string, []string, error) {
	// set by exactly one branch below.
	var natsCred natsserver.NodeCred
	var clusterSecret string
	var clusterRoutes []string
	var err error

	// coordinator: join+approve if non-primary, else self-generate.
	if cfg.EmbedNATS {
		if cfg.JoinURL != "" {
			// non-primary: join, wait for approval. (for secondary coorddinator or secondary coordiantor + worker)
			joinResult, err := requestAndWaitForApproval(cfg, ident, joinmgr.RoleServer)
			if err != nil {
				return natsserver.NodeCred{}, "", nil, err
			}
			clusterSecret = joinResult.ClusterSecret
			clusterRoutes = joinResult.ClusterRoutes
			// save token as coord cred.
			if err := nodeident.SaveToken(cfg.DataDir, "node.token", joinResult.Token); err != nil {
				log.Printf("warning: could not save node token: %v", err)
			}
			natsCred = natsserver.NodeCred{Username: ident.NodeID, Password: joinResult.Token} // nodeID as username, received token as password
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

	// worker: join if no token yet.
	if !cfg.EmbedNATS && cfg.Client.Enabled {
		if cfg.JoinURL != "" {
			token := nodeident.LoadToken(cfg.DataDir, "node.token")
			if token == "" {
				joinResult, err := requestAndWaitForApproval(cfg, ident, joinmgr.RoleWorker)
				if err != nil {
					return natsserver.NodeCred{}, "", nil, err
				}
				token = joinResult.Token
				if err := nodeident.SaveToken(cfg.DataDir, "node.token", token); err != nil {
					log.Printf("warning: could not save node token: %v", err)
				}
				// update NATS URL if given.
				if joinResult.CoordURL != "" {
					cfg.NatsURL = joinResult.CoordURL
				}
			}
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
		cfg.AdvertiseHost)
	if err != nil {
		return nil, fmt.Errorf("failed to start embedded NATS: %w", err)
	}
	cfg.NatsURL = embeddedNATS.ClientURL() // real address, replaces config.go placeholder
	return embeddedNATS, nil
}
