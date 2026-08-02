package agent

import (
	"fmt"
	"log"

	"github.com/edgegrid/edgegrid/internal/config"
	"github.com/edgegrid/edgegrid/internal/joinmgr"
	"github.com/edgegrid/edgegrid/internal/natsserver"
	"github.com/edgegrid/edgegrid/internal/nodeident"
	"github.com/edgegrid/edgegrid/internal/nodelog"
	"tailscale.com/tsnet"
)

// resolves this node's NATS username/password credential.
// - primary coordinator (cfg.EmbedNATS && cfg.JoinURL == ""): natsCred: __coord__/coordSecret
// - secondary coordinator (cfg.EmbedNATS && cfg.JoinURL != ""):
//     - perform join+wait for approval as RoleServer -> joinResult
//     - natsCred: joinResult.Token (username=ident.NodeID)
//     - persisted: node.token
// - worker (cfg.EmbedNATS == false && cfg.Client.Enabled):
//     - if node.token exists, use it as natsCred (username=ident.NodeID)
//     - else if cfg.JoinURL != "", perform join+wait for approval as RoleWorker -> joinResult
//         - natsCred: joinResult.Token (username=ident.NodeID)
//         - persisted: node.token, nats.url (if joinResult.CoordURL != "")
func resolveNATSCredential(
	ts *tsnet.Server,
	cfg *config.Config,
	ident *nodeident.Identity,
) (natsserver.NodeCred, error) {
	// set by exactly one branch below.
	var natsCred natsserver.NodeCred
	var err error

	// ! COORDINATOR
	if cfg.EmbedNATS {
		if cfg.JoinURL != "" { // ? Secondary Coordinator: Load from disk
			token := nodeident.LoadToken(cfg.DataDir, "node.token")

			if token != "" {
				natsCred = natsserver.NodeCred{
					Username: ident.NodeID,
					Password: token,
				}

			} else { // ? Secondary Coordinator: Perform join+wait for approval, then save token to disk
				joinResult, err := requestAndWaitForApproval(ts, cfg, ident, joinmgr.RoleServer)
				if err != nil {
					return natsserver.NodeCred{}, err
				}

				// save token as coord cred.
				if err := nodeident.SaveToken(cfg.DataDir, "node.token", joinResult.Token); err != nil {
					log.Printf("warning: could not save node token: %v", err)
				}
				natsCred = natsserver.NodeCred{
					Username: ident.NodeID,
					Password: joinResult.Token,
				} // nodeID as username, received token as password
			}
		} else { // ? Primary Coordinator: generate if missing.
			coordSecret := nodeident.LoadToken(cfg.DataDir, "coord.secret") // equivalent to node.token
			if coordSecret == "" {
				coordSecret, err = nodeident.RandomToken(32)
				if err != nil {
					return natsserver.NodeCred{}, fmt.Errorf("generate coordinator secret: %w", err)
				}
				if err := nodeident.SaveToken(cfg.DataDir, "coord.secret", coordSecret); err != nil {
					return natsserver.NodeCred{}, fmt.Errorf("save coordinator secret: %w", err)
				}
			}
			natsCred = natsserver.NodeCred{
				Username: "__coord__",
				Password: coordSecret,
			}
		}
	}

	// ! WORKER
	if !cfg.EmbedNATS && cfg.Client.Enabled {
		if savedNatsURL := nodeident.LoadToken(cfg.DataDir, "nats.url"); savedNatsURL != "" {
			cfg.NatsURL = savedNatsURL
		}

		token := nodeident.LoadToken(cfg.DataDir, "node.token")
		if token == "" && cfg.JoinURL != "" {
			joinResult, err := requestAndWaitForApproval(ts, cfg, ident, joinmgr.RoleWorker)
			if err != nil {
				return natsserver.NodeCred{}, err
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
			natsCred = natsserver.NodeCred{
				Username: ident.NodeID,
				Password: token,
			}
		}
	}

	return natsCred, nil
}

// startEmbeddedNATS boots this node's own NATS/JetStream server. Coordinator-only.
func startEmbeddedNATS(
	cfg *config.Config,
	natsCred natsserver.NodeCred,
) (*natsserver.EmbeddedServer, error) {
	embeddedNATS, err := natsserver.Start(
		cfg.NATSPort,
		cfg.NATSStore,
		natsCred,
		cfg.AdvertiseHost,
		cfg.TailscaleHostname,
		nodelog.Path(cfg.DataDir))
	if err != nil {
		return nil, fmt.Errorf("failed to start embedded NATS: %w", err)
	}
	cfg.NatsURL = embeddedNATS.ClientURL()
	return embeddedNATS, nil
}
