package tlsstream

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"tun/internal/transport"
)

type Dialer struct {
	TLSConfig *tls.Config
	Timeout   time.Duration
}

func (d *Dialer) Dial(ctx context.Context, addr string) (transport.Stream, error) {
	var dialer net.Dialer
	if d.Timeout > 0 {
		dialer.Timeout = d.Timeout
	}
	conn, err := tls.DialWithDialer(&dialer, "tcp", addr, d.TLSConfig)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

type Listener struct {
	ln net.Listener
}

func Listen(addr string, cfg *tls.Config) (*Listener, error) {
	ln, err := tls.Listen("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	return &Listener{ln: ln}, nil
}

func (l *Listener) Accept(ctx context.Context) (transport.Stream, error) {
	if deadline, ok := ctx.Deadline(); ok {
		if dl, ok := l.ln.(interface{ SetDeadline(time.Time) error }); ok {
			_ = dl.SetDeadline(deadline)
		}
	}
	conn, err := l.ln.Accept()
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (l *Listener) Close() error {
	return l.ln.Close()
}

func (l *Listener) Addr() net.Addr {
	return l.ln.Addr()
}
