package discovery

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

// what the peer wants after the hello, e.g. send a file or ask for a result
type IntentType string

const (
	IntentHello IntentType = "hello"

	// a file manifest follows on the same connection
	IntentBlob IntentType = "blob"

	IntentTask IntentType = "task"

	// a request for a task's result follows on the same connection
	IntentResult IntentType = "result"
)

// the biggest hello we'll read, 64 KB, so a bad length can't make us read gigabytes
const maxHelloSize = 64 * 1024

// how long one hello exchange may take, 5s, so a stuck peer can't hang us forever
// tests can change it with Server.HelloTimeout
const helloTimeout = 5 * time.Second

// what a node says about itself when it connects: its EdgeGrid ID and what it wants
// it's the peer's own word, unlike Tailscale's WhoIs, so it picks the handler but never decides what the peer is allowed to do
type Hello struct {
	NodeID string     `json:"node_id"`
	Intent IntentType `json:"intent"`
}

// sends a hello as a 4-byte length and then JSON
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

// reads one hello, refusing one that's too big before reading it
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

// sends our hello, then waits for the peer's; a timeout of 0 means 5s
// the dialer speaks first, so the two sides never both wait to write
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

// waits for the peer's hello, then answers with ours
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
