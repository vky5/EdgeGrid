package discovery

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestWriteReadHelloRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	want := Hello{NodeID: "node-a"}
	if err := WriteHello(&buf, want); err != nil {
		t.Fatalf("WriteHello: %v", err)
	}
	got, err := ReadHello(&buf)
	if err != nil {
		t.Fatalf("ReadHello: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestReadHelloRejectsOversizedLength(t *testing.T) {
	var buf bytes.Buffer
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], maxHelloSize+1)
	buf.Write(prefix[:])
	// No body — an oversized length must be rejected before ReadHello ever
	// tries to read (and block on) a body that isn't there.

	if _, err := ReadHello(&buf); err == nil {
		t.Error("expected an error for a length prefix over maxHelloSize, got nil")
	}
}

func TestExchangeOverRealConnection(t *testing.T) {
	dialerConn, listenerConn := net.Pipe()
	defer dialerConn.Close()
	defer listenerConn.Close()

	dialerHello := Hello{NodeID: "node-a"}
	listenerHello := Hello{NodeID: "node-b"}

	listenerResult := make(chan Hello, 1)
	listenerErr := make(chan error, 1)
	go func() {
		got, err := ExchangeAsListener(listenerConn, listenerHello, time.Second)
		listenerResult <- got
		listenerErr <- err
	}()

	got, err := ExchangeAsDialer(dialerConn, dialerHello, time.Second)
	if err != nil {
		t.Fatalf("ExchangeAsDialer: %v", err)
	}
	if got.NodeID != listenerHello.NodeID {
		t.Errorf("dialer got %+v, want %+v", got, listenerHello)
	}

	select {
	case err := <-listenerErr:
		if err != nil {
			t.Fatalf("ExchangeAsListener: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ExchangeAsListener")
	}
	if lg := <-listenerResult; lg.NodeID != dialerHello.NodeID {
		t.Errorf("listener got %+v, want %+v", lg, dialerHello)
	}
}
