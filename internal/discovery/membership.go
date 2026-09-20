package discovery

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"time"

	"tailscale.com/client/local"
)

// Peer is one other EdgeGrid node visible on the tailnet — a filtered, typed
// view of ipnstate.PeerStatus.
type Peer struct {
	ID       string // Tailscale's stable device ID, not node.Node.NodeID()
	Hostname string
	DNSName  string // MagicDNS name, ends with a dot
	IP       netip.Addr

	Online   bool
	LastSeen time.Time // only meaningful when !Online

	// How this node is currently reaching the peer, straight from Tailscale's
	// status. See Route for what they add up to.
	CurAddr   string // a direct ip:port, when a direct path is up
	Relay     string // DERP relay region, when going through one
	PeerRelay string // a peer relay's address, when going through one
}

// Route says how traffic to this peer is travelling right now: "direct",
// "relay" (a DERP server) or "peer relay", or "" when it's offline or no path
// has been established yet.
//
// This is the current path, not a promise. Tailscale starts a connection via
// a relay and upgrades to a direct path once it has one, so a peer nothing
// has been sent to lately can read "relay" until traffic flows. A relayed
// path is much slower than a direct one, which is why it's worth showing.
func (p Peer) Route() string {
	switch {
	case !p.Online:
		return ""
	case p.CurAddr != "":
		return "direct"
	case p.PeerRelay != "":
		return "peer relay"
	case p.Relay != "":
		return "relay"
	default:
		return ""
	}
}

// Snapshot lists every tailnet device tagged as an EdgeGrid node (see Tag),
// excluding this one. No dial, no handshake — Status() already reflects
// what Tailscale's control plane knows, offline peers included, so a
// just-restarted node sees its whole peer list before exchanging a byte.
func Snapshot(ctx context.Context, lc *local.Client) ([]Peer, error) {
	st, err := lc.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("tailscale status: %w", err)
	}

	var peers []Peer
	for _, ps := range st.Peer {
		if ps.Tags == nil {
			continue
		}
		tagged := false
		for _, t := range ps.Tags.All() {
			if t == Tag {
				tagged = true
				break
			}
		}
		if !tagged {
			continue
		}

		p := Peer{
			ID:       string(ps.ID),
			Hostname: ps.HostName,
			DNSName:  ps.DNSName,
			Online:   ps.Online,
			LastSeen: ps.LastSeen,

			CurAddr:   ps.CurAddr,
			Relay:     ps.Relay,
			PeerRelay: ps.PeerRelay,
		}
		if len(ps.TailscaleIPs) > 0 {
			p.IP = ps.TailscaleIPs[0]
		}
		peers = append(peers, p)
	}

	// Deterministic order for callers.
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	return peers, nil
}
