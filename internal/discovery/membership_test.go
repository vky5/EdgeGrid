package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/views"
)

// a fake Tailscale status reply, so Snapshot runs against the real client with no tailnet
type fakeStatusTransport struct{ status *ipnstate.Status }

func (f fakeStatusTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := json.Marshal(f.status)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func peerStatus(id, hostname string, ip string, online bool, lastSeen time.Time, tags ...string) *ipnstate.PeerStatus {
	var tv *views.Slice[string]
	if len(tags) > 0 {
		s := views.SliceOf(tags)
		tv = &s
	}
	ps := &ipnstate.PeerStatus{
		ID:       tailcfg.StableNodeID(id),
		HostName: hostname,
		DNSName:  hostname + ".tail2225ba.ts.net.",
		Online:   online,
		LastSeen: lastSeen,
		Tags:     tv,
	}
	if ip != "" {
		ps.TailscaleIPs = []netip.Addr{netip.MustParseAddr(ip)}
	}
	return ps
}

// a new random key on every call; tests only need it to be different each time
func nodeKey() key.NodePublic { return key.NewNode().Public() }

func TestSnapshotFiltersByTagAndSortsByID(t *testing.T) {
	st := &ipnstate.Status{
		Peer: map[key.NodePublic]*ipnstate.PeerStatus{
			nodeKey(): peerStatus("z-edge", "worker-z", "100.64.0.3", true, time.Time{}, Tag),
			nodeKey(): peerStatus("a-edge", "worker-a", "100.64.0.2", false, mustParseTime(t, "2026-09-01T00:00:00Z"), Tag),
			nodeKey(): peerStatus("untagged", "laptop", "100.64.0.9", true, time.Time{}), // no tag — must be excluded
			nodeKey(): peerStatus("other-tag", "phone", "100.64.0.5", true, time.Time{}, "tag:personal"),
		},
	}
	lc := &local.Client{Transport: fakeStatusTransport{status: st}}

	got, err := Snapshot(context.Background(), lc)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d peers, want 2 (untagged and other-tag peers should be excluded): %+v", len(got), got)
	}
	if got[0].ID != "a-edge" || got[1].ID != "z-edge" {
		t.Errorf("not sorted by ID: got %q, %q", got[0].ID, got[1].ID)
	}

	// online peer
	z := got[1]
	if !z.Online {
		t.Error("z-edge should be online")
	}
	if z.Hostname != "worker-z" {
		t.Errorf("Hostname = %q", z.Hostname)
	}
	if z.IP.String() != "100.64.0.3" {
		t.Errorf("IP = %q", z.IP)
	}

	// offline peer keeps its last-seen time
	a := got[0]
	if a.Online {
		t.Error("a-edge should be offline")
	}
	if a.LastSeen.IsZero() {
		t.Error("offline peer should carry LastSeen")
	}
}

func TestSnapshotEmptyWhenNoTaggedPeers(t *testing.T) {
	st := &ipnstate.Status{
		Peer: map[key.NodePublic]*ipnstate.PeerStatus{
			nodeKey(): peerStatus("laptop", "laptop", "100.64.0.9", true, time.Time{}),
		},
	}
	lc := &local.Client{Transport: fakeStatusTransport{status: st}}

	got, err := Snapshot(context.Background(), lc)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d peers, want 0", len(got))
	}
}

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestPeerRoute(t *testing.T) {
	cases := []struct {
		name string
		p    Peer
		want string
	}{
		{"direct path", Peer{Online: true, CurAddr: "192.168.1.5:41641"}, "direct"},
		{"derp relay", Peer{Online: true, Relay: "mum"}, "relay"},
		{"peer relay", Peer{Online: true, PeerRelay: "10.0.0.2:3478:1"}, "peer relay"},
		// direct wins over relay: both can show while Tailscale is switching, and direct carries the traffic
		{"direct beats relay", Peer{Online: true, CurAddr: "1.2.3.4:5", Relay: "mum"}, "direct"},
		{"no path yet", Peer{Online: true}, ""},
		// old path data on an offline peer must not show as live
		{"offline", Peer{Online: false, CurAddr: "1.2.3.4:5"}, ""},
	}
	for _, c := range cases {
		if got := c.p.Route(); got != c.want {
			t.Errorf("%s: Route() = %q, want %q", c.name, got, c.want)
		}
	}
}
