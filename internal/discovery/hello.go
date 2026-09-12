package discovery

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

// maxHelloSize bounds how much a peer can make us allocate for one hello,
// so a garbled or hostile length prefix can't make ReadHello try to read
// gigabytes.
const maxHelloSize = 64 * 1024

// helloTimeout bounds one hello exchange, so a slow or wedged peer can't
// hang a goroutine forever. See Server.HelloTimeout to override it in tests.
const helloTimeout = 5 * time.Second

// Hello is what a node says about itself on connect — minimal by design,
// just enough to prove the exchange works. It's a different kind of fact
// than WhoIs: WhoIs is Tailscale's control plane, unspoofable; Hello is the
// peer's own self-report, so NodeID is EdgeGrid's identity
// (node.Node.NodeID), not a claim about anyone else.
type Hello struct {
	NodeID string `json:"node_id"`
}

// WriteHello encodes h as length-prefixed JSON: a 4-byte big-endian length
// followed by that many bytes of JSON.
func WriteHello(w io.Writer, h Hello) error {
	body, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("discovery: encode hello: %w", err)
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	if _, err := w.Write(prefix[:]); err != nil {
		return fmt.Errorf("discovery: write hello length: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("discovery: write hello body: %w", err)
	}
	return nil
}

// ReadHello decodes one length-prefixed hello from r, rejecting an
// oversized length before allocating a body for it.
func ReadHello(r io.Reader) (Hello, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return Hello{}, fmt.Errorf("discovery: read hello length: %w", err)
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n > maxHelloSize {
		return Hello{}, fmt.Errorf("discovery: hello length %d exceeds max %d", n, maxHelloSize)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return Hello{}, fmt.Errorf("discovery: read hello body: %w", err)
	}
	var h Hello
	if err := json.Unmarshal(body, &h); err != nil {
		return Hello{}, fmt.Errorf("discovery: decode hello: %w", err)
	}
	return h, nil
}

// ExchangeAsDialer sends self, then waits for the peer's hello — the
// dialer speaks first, so both sides never write into a full buffer at
// once. timeout of 0 uses helloTimeout.
func ExchangeAsDialer(conn net.Conn, self Hello, timeout time.Duration) (Hello, error) {
	if err := setHelloDeadline(conn, timeout); err != nil {
		return Hello{}, err
	}
	defer conn.SetDeadline(time.Time{}) //nolint:errcheck // best-effort clear

	if err := WriteHello(conn, self); err != nil {
		return Hello{}, err
	}
	return ReadHello(conn)
}

// ExchangeAsListener waits for the peer's hello, then responds with self.
func ExchangeAsListener(conn net.Conn, self Hello, timeout time.Duration) (Hello, error) {
	if err := setHelloDeadline(conn, timeout); err != nil {
		return Hello{}, err
	}
	defer conn.SetDeadline(time.Time{}) //nolint:errcheck // best-effort clear

	peer, err := ReadHello(conn)
	if err != nil {
		return Hello{}, err
	}
	if err := WriteHello(conn, self); err != nil {
		return Hello{}, err
	}
	return peer, nil
}

func setHelloDeadline(conn net.Conn, timeout time.Duration) error {
	if timeout == 0 {
		timeout = helloTimeout
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("discovery: set hello deadline: %w", err)
	}
	return nil
}
