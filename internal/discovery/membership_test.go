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

// fakeStatusTransport intercepts local.Client's HTTP call and returns a
// canned status, so Snapshot can be tested against the real client type
// with no live tsnet.Server or tailscaled socket involved.
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

// nodeKey returns a fresh random public key, distinct on every call — good
// enough as a map key for tests, which only need uniqueness, not identity.
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

	// Online peer.
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

	// Offline peer carries LastSeen.
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
