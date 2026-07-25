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

// nats-server's own cluster route dialer has no way to route through tsnet's
// in-process userspace network stack (confirmed: server.ClusterOpts exposes
// no custom-dialer hook) — it only ever does plain OS-level TCP. These two
// functions bridge that gap ourselves, entirely inside our own process, so
// nats-server never needs to know tsnet exists.

// bridgeInboundCluster accepts connections arriving on this node's tsnet
// interface at clusterPort and relays them to the embedded NATS server's
// real cluster listener on localhost. Lets another coordinator's outbound
// route (via bridgeOutboundRoutes) actually reach this one over the tailnet.
func bridgeInboundCluster(ts *tsnet.Server, clusterPort int) {
	addr := fmt.Sprintf(":%d", clusterPort)
	ln, err := ts.Listen("tcp", addr)
	if err != nil {
		log.Printf("cluster bridge: tsnet listen on %s failed: %v", addr, err)
		return
	}
	localAddr := fmt.Sprintf("127.0.0.1:%d", clusterPort)
	log.Printf("cluster bridge: relaying tsnet%s -> %s", addr, localAddr)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Printf("cluster bridge: accept on tsnet%s failed: %v", addr, err)
				return
			}
			go func() {
				local, err := net.Dial("tcp", localAddr)
				if err != nil {
					log.Printf("cluster bridge: dial local NATS cluster port failed: %v", err)
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
