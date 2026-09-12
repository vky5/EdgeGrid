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

// Port is the fixed, well-known TCP port every EdgeGrid node listens on for
// peer discovery. Fixed, not configurable: a node would need to already be
// talking to a peer to learn a dynamic port, which is circular.
const Port = 9797

// IdentifyPeer resolves who's on the other end of conn via Tailscale's
// local WhoIs API. Nothing conn sends is trusted here — identity comes from
// the tailnet's control plane, not from what the remote side claims.
func IdentifyPeer(ctx context.Context, lc *local.Client, conn net.Conn) (*apitype.WhoIsResponse, error) {
	return lc.WhoIs(ctx, conn.RemoteAddr().String())
}

// Server accepts connections on a discovery listener, identifies each one
// via WhoIs, and exchanges a Hello with it.
type Server struct {
	ln   net.Listener
	lc   *local.Client
	self Hello

	// OnPeer is called once a connection is identified and the hello
	// exchange succeeds. The handler owns conn and must close it. If nil,
	// the connection is closed immediately after the exchange.
	OnPeer func(who *apitype.WhoIsResponse, hello Hello, conn net.Conn)

	// Logf defaults to a no-op.
	Logf func(format string, args ...any)

	// HelloTimeout overrides helloTimeout when non-zero — for tests that
	// want the "peer never sent hello" path without the production wait.
	HelloTimeout time.Duration
}

// NewServer wraps an already-open listener (from node.Node.Listen) and a
// Tailscale local client (from node.Node.LocalClient). self is what this
// node reports during the hello exchange (see Hello), normally
// node.Node.NodeID(). Server does not open the listener itself, so closing
// ln from the outside is what makes Serve's Accept loop return.
func NewServer(ln net.Listener, lc *local.Client, self Hello) *Server {
	return &Server{ln: ln, lc: lc, self: self, Logf: func(string, ...any) {}}
}

// Serve accepts connections until ln is closed or ctx is cancelled — both
// are a clean shutdown, not a failure.
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
		go s.handle(ctx, conn)
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
