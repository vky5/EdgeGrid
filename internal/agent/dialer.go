package agent

import (
	"context"
	"net"

	"tailscale.com/tsnet"
)

type tsnetDialer struct {
	ts *tsnet.Server
}

func (d tsnetDialer) Dial(network, address string) (net.Conn, error) {
	return d.ts.Dial(context.Background(), network, address)
}
