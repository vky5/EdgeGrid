package discovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
)

// the TCP port every EdgeGrid node listens on, 9797
// fixed, since a node can't learn a peer's port without already talking to it
const Port = 9797

// asks Tailscale who is on the other end of the connection
// nothing the peer says is trusted here, so a peer can't claim to be someone else
func IdentifyPeer(ctx context.Context, lc *local.Client, conn net.Conn) (*apitype.WhoIsResponse, error) {
	return lc.WhoIs(ctx, conn.RemoteAddr().String())
}

// takes connections from peers, asks Tailscale who each one is, and swaps hellos
type Server struct {
	ln   net.Listener  // to .accept() TCP connection
	lc   *local.Client // to ask tailscale who peer is (IdentityPeer())
	self Hello         // message node says about itself

	// runs after a peer is identified and hellos are swapped; it must close conn
	// if nil, the connection is closed right after the hello
	OnPeer func(who *apitype.WhoIsResponse, hello Hello, conn net.Conn)

	Logf func(format string, args ...any)

	// replaces the 5s hello wait when set, so a test of a silent peer doesn't wait 5s
	HelloTimeout time.Duration
}

// wraps a listener that's already open, e.g. from node.Node.Listen
// self is the hello this node sends; closing ln is what stops Serve
func NewServer(ln net.Listener, lc *local.Client, self Hello) *Server {
	return &Server{ln: ln, lc: lc, self: self, Logf: func(string, ...any) {}}
}

// takes connections until ln is closed or ctx is cancelled; both are a clean stop, not an error
func (s *Server) Serve(ctx context.Context) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			s.ln.Close()
		case <-done:
		}
	}()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("discovery: accept: %w", err)
		}
		go s.handle(ctx, conn) // no matter how many conn comes will be handle in multiple goroutine in handle()
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	who, err := IdentifyPeer(ctx, s.lc, conn)
	if err != nil {
		s.Logf("discovery: could not identify %s: %v", conn.RemoteAddr(), err)
		conn.Close()
		return
	}

	name := who.Node.ComputedName
	if name == "" {
		name = who.Node.Name
	}
	s.Logf("discovery: connection from %s (%s)", name, who.Node.StableID)

	hello, err := ExchangeAsListener(conn, s.self, s.HelloTimeout)
	if err != nil {
		s.Logf("discovery: hello exchange with %s failed: %v", name, err)
		conn.Close()
		return
	}
	s.Logf("discovery: %s self-reports node_id=%s", name, hello.NodeID)

	if s.OnPeer != nil {
		s.OnPeer(who, hello, conn)
		return
	}
	conn.Close()
}
