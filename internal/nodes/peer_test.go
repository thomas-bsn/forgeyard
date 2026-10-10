package nodes

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc/peer"
)

func TestLanPeerIP(t *testing.T) {
	for addr, want := range map[string]string{
		"192.168.1.50:51234": "192.168.1.50",
		"10.0.0.7:4000":      "10.0.0.7",
		"172.17.0.1:4000":    "", // a Docker gateway, not the node
		"203.0.113.9:4000":   "", // over the Internet
	} {
		tcp, _ := net.ResolveTCPAddr("tcp", addr)
		ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: tcp})
		if got := lanPeerIP(ctx); got != want {
			t.Errorf("%s: %q, want %q", addr, got, want)
		}
	}
}
