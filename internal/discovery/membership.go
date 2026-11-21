package discovery

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"time"

	"tailscale.com/client/local"
)

// another EdgeGrid node on the tailnet, as Tailscale sees it
type Peer struct {
	ID       string     // Tailscale's stable device ID, not node.Node.NodeID()
	Hostname string     // e.g. alpha5-297b
	DNSName  string     // MagicDNS name, ends with a dot, e.g. alpha5-297b.<tailnet>.ts.net.
	IP       netip.Addr // e.g. 100.x.y.z

	Online   bool
	LastSeen time.Time // only meaningful when !Online

	// how we reach the peer right now, straight from Tailscale (Route sums them up)
	CurAddr   string // a direct ip:port, when a direct path is up
	Relay     string // DERP relay region, when going through one
	PeerRelay string // a peer relay's address, when going through one
}

// how traffic to this peer goes right now: "direct", "relay", "peer relay", or "" if there's no path
// e.g. an idle peer can read "relay" until traffic flows, then switch to "direct"
// a relay is much slower than direct, so it's worth showing
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

// lists every EdgeGrid node on the tailnet except this one, offline ones too
// it only asks Tailscale, so a node that just started sees its peers before talking to any
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

	// sort by ID so the order is the same every time
	sort.Slice(peers, func(i, j int) bool { return peers[i].ID < peers[j].ID })
	return peers, nil
}
