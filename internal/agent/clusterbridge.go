package agent

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"tailscale.com/tsnet"
)

// nats-server only does plain OS-level TCP — tsnet peers can't reach it
// directly. These functions bridge that gap in our own process.

// bridgeInboundPort relays tsnet-arriving connections at port to the
// embedded NATS server's real localhost listener on that port. Used for
// both the client port and the cluster route port.
func bridgeInboundPort(ts *tsnet.Server, port int) {
	addr := fmt.Sprintf(":%d", port)
	ln, err := ts.Listen("tcp", addr)
	if err != nil {
		log.Printf("port bridge: tsnet listen on %s failed: %v", addr, err)
		return
	}
	localAddr := fmt.Sprintf("127.0.0.1:%d", port)
	log.Printf("port bridge: relaying tsnet%s -> %s", addr, localAddr)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Printf("port bridge: accept on tsnet%s failed: %v", addr, err)
				return
			}
			go func() {
				local, err := net.Dial("tcp", localAddr)
				if err != nil {
					log.Printf("port bridge: dial local NATS port failed: %v", err)
					conn.Close()
					return
				}
				relay(conn, local)
			}()
		}
	}()
}

// relay pipes bytes both directions between two already-established
// connections until either side closes.
func relay(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(b, a) //nolint:errcheck
		b.Close()
	}()
	go func() {
		defer wg.Done()
		io.Copy(a, b) //nolint:errcheck
		a.Close()
	}()
	wg.Wait()
}
