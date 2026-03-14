package echo

import (
	"context"
	"io"
	"net"
	"time"

	"tun/internal/transport"
)

// In-memory echo transport for quick tests.

type Dialer struct {
	Addr    string
	Timeout time.Duration
}

func (d *Dialer) Dial(ctx context.Context, addr string) (transport.Stream, error) {
	return net.Dial("tcp", addr)
}

// RunEchoServer starts a simple echo server on addr.
func RunEchoServer(addr string, stop <-chan struct{}) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer ln.Close()

	for {
		select {
		case <-stop:
			return nil
		default:
		}
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go func(c net.Conn) {
			defer c.Close()
			_, _ = io.Copy(c, c)
		}(conn)
	}
}
