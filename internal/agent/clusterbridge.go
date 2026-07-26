package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
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

// bridgeOutboundRoutes takes this node's cluster seed routes (real remote
// tsnet addresses, e.g. from a join/approve response) and, for each one,
// starts a local proxy that dials the remote peer via ts.Dial. It returns
// the rewritten route list pointing at those local proxies — what actually
// gets handed to nats-server, which never learns tsnet was involved.
func bridgeOutboundRoutes(ctx context.Context, ts *tsnet.Server, routes []string) []string {
	rewritten := make([]string, 0, len(routes))
	for _, r := range routes {
		u, err := url.Parse(r)
		if err != nil {
			log.Printf("cluster bridge: invalid route URL %q, skipping: %v", r, err)
			continue
		}
		remoteAddr := u.Host

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			log.Printf("cluster bridge: local listen for route %s failed: %v", r, err)
			continue
		}
		localAddr := ln.Addr().String()
		log.Printf("cluster bridge: relaying %s -> tsnet %s", localAddr, remoteAddr)

		go func(ln net.Listener, remoteAddr string) {
			for {
				conn, err := ln.Accept()
				if err != nil {
					log.Printf("cluster bridge: accept on %s failed: %v", ln.Addr(), err)
					return
				}
				go func() {
					remote, err := ts.Dial(ctx, "tcp", remoteAddr)
					if err != nil {
						log.Printf("cluster bridge: tsnet dial %s failed: %v", remoteAddr, err)
						conn.Close()
						return
					}
					relay(conn, remote)
				}()
			}
		}(ln, remoteAddr)

		rewritten = append(rewritten, "nats://"+localAddr)
	}
	return rewritten
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
