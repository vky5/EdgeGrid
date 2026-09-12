package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

// fakeWhoIsTransport intercepts local.Client's WhoIs HTTP call and records
// the "addr" it was asked about, so tests can assert the real remote
// address was used, not a value the peer sent.
type fakeWhoIsTransport struct {
	response  *apitype.WhoIsResponse
	notFound  bool
	gotAddrCh chan string // best-effort recording, buffered
}

func (f *fakeWhoIsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	addr, err := url.QueryUnescape(req.URL.Query().Get("addr"))
	if err == nil && f.gotAddrCh != nil {
		select {
		case f.gotAddrCh <- addr:
		default:
		}
	}
	if f.notFound {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
	}
	body, err := json.Marshal(f.response)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
}

func testServer(t *testing.T, transport http.RoundTripper) (*Server, net.Listener) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	lc := &local.Client{Transport: transport}
	return NewServer(ln, lc), ln
}

func TestServerIdentifiesConnectionByItsRealRemoteAddr(t *testing.T) {
	who := &apitype.WhoIsResponse{Node: &tailcfg.Node{
		Name:         "peer-b.tail2225ba.ts.net.",
		ComputedName: "peer-b",
		StableID:     "peer-b-id",
	}}
	transport := &fakeWhoIsTransport{response: who, gotAddrCh: make(chan string, 1)}
	srv, ln := testServer(t, transport)

	identified := make(chan *apitype.WhoIsResponse, 1)
	srv.OnPeer = func(w *apitype.WhoIsResponse, conn net.Conn) {
		identified <- w
		conn.Close()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- srv.Serve(ctx) }()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	clientLocalAddr := client.LocalAddr().String() // this becomes the server's RemoteAddr

	select {
	case gotAddr := <-transport.gotAddrCh:
		if gotAddr != clientLocalAddr {
			t.Errorf("WhoIs was asked about %q, want the real connection address %q", gotAddr, clientLocalAddr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for WhoIs to be called")
	}

	select {
	case w := <-identified:
		if w.Node.StableID != "peer-b-id" {
			t.Errorf("OnPeer got StableID %q, want peer-b-id", w.Node.StableID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OnPeer")
	}

	cancel()
	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Errorf("Serve returned %v after cancel, want nil (clean shutdown)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after context cancellation")
	}
}

func TestServerClosesConnectionWhenOnPeerIsNil(t *testing.T) {
	who := &apitype.WhoIsResponse{Node: &tailcfg.Node{Name: "peer-b", StableID: "peer-b-id"}}
	transport := &fakeWhoIsTransport{response: who}
	srv, ln := testServer(t, transport) // OnPeer left nil

	go srv.Serve(t.Context())

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	_, err = client.Read(buf)
	if err != io.EOF {
		t.Errorf("expected the server to close the connection (EOF), got %v", err)
	}
}

func TestServerClosesConnectionWhenPeerNotFound(t *testing.T) {
	transport := &fakeWhoIsTransport{notFound: true}
	srv, ln := testServer(t, transport)

	var loggedNotFound bool
	logDone := make(chan struct{}, 1)
	srv.Logf = func(format string, args ...any) {
		loggedNotFound = true
		select {
		case logDone <- struct{}{}:
		default:
		}
	}
	srv.OnPeer = func(w *apitype.WhoIsResponse, conn net.Conn) {
		t.Error("OnPeer should not be called for an unidentifiable connection")
	}

	go srv.Serve(t.Context())

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	select {
	case <-logDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the not-found path to log")
	}
	if !loggedNotFound {
		t.Error("expected a log line for the unidentifiable connection")
	}

	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if _, err := client.Read(buf); err != io.EOF {
		t.Errorf("expected connection closed (EOF) after failed identification, got %v", err)
	}
}
