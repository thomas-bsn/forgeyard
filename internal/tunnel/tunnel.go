// Package tunnel carries a TCP connection over a gRPC stream of byte chunks: the control plane reaches a
// node's Traefik through the agent's own outbound connection, so the node needs no inbound port.
package tunnel

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/thomas-bsn/forgeyard/internal/agentpb"
)

// chunkSize keeps each message well under gRPC's 4 MB default limit.
const chunkSize = 32 << 10

// Stream is either end of a Tunnel RPC.
type Stream interface {
	Send(*agentpb.TunnelChunk) error
	Recv() (*agentpb.TunnelChunk, error)
}

// Conn is a net.Conn over a Stream. Closing it calls the close function, which ends the stream.
type Conn struct {
	stream  Stream
	closeFn func()

	rmu  sync.Mutex
	rbuf []byte

	wmu  sync.Mutex
	once sync.Once
}

// New returns a Conn over a stream; first is data already received with the stream's first chunk.
func New(stream Stream, first []byte, closeFn func()) *Conn {
	return &Conn{stream: stream, rbuf: first, closeFn: closeFn}
}

func (c *Conn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for len(c.rbuf) == 0 {
		chunk, err := c.stream.Recv()
		if err != nil {
			return 0, io.EOF // the other end closed, or the stream broke: either way the connection is over
		}
		c.rbuf = chunk.GetData()
	}
	n := copy(p, c.rbuf)
	c.rbuf = c.rbuf[n:]
	return n, nil
}

func (c *Conn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	written := 0
	for len(p) > 0 {
		n := min(len(p), chunkSize)
		if err := c.stream.Send(&agentpb.TunnelChunk{Data: p[:n]}); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// Close ends the stream; a Read or Write in progress returns.
func (c *Conn) Close() error {
	c.once.Do(c.closeFn)
	return nil
}

func (c *Conn) LocalAddr() net.Addr  { return addr{} }
func (c *Conn) RemoteAddr() net.Addr { return addr{} }

// Deadlines are not supported: the HTTP client using the connection bounds it with its context.
func (c *Conn) SetDeadline(time.Time) error      { return nil }
func (c *Conn) SetReadDeadline(time.Time) error  { return nil }
func (c *Conn) SetWriteDeadline(time.Time) error { return nil }

type addr struct{}

func (addr) Network() string { return "tunnel" }
func (addr) String() string  { return "tunnel" }

// Pipe copies between a Conn and another connection until either side closes, then closes both.
func Pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		io.Copy(dst, src)
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	a.Close()
	b.Close()
	<-done
}
