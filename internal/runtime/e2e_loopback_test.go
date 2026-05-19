package runtime

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"tun/internal/core"
	"tun/internal/engine"
	"tun/internal/transport"
	"tun/internal/tun"
)

func TestClientServerLoopbackDataPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientConn, serverConn := netPipe()
	listener := &oneShotListener{stream: serverConn}

	curve := ecdh.X25519()
	serverStaticPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate server static key: %v", err)
	}
	serverStaticPub := serverStaticPriv.PublicKey().Bytes()
	var sid [16]byte
	copy(sid[:], []byte("server-id-000000"))
	var cid [16]byte
	copy(cid[:], []byte("client-id-000000"))

	clientTun := newLoopTun("ctun0", 1400)
	serverTun := newLoopTun("stun0", 1400)

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- RunServer(
			ctx,
			func(_ context.Context) (tun.Device, error) { return serverTun, nil },
			listener,
			func(stream transport.Stream) (*core.Session, error) {
				return core.ServerHandshakeWithOptions(stream, sid, serverStaticPriv, false)
			},
			ClientOptions{
				Sleep: func(_ context.Context, _ time.Duration) error { return nil },
			},
		)
	}()

	clientErrCh := make(chan error, 1)
	go func() {
		clientErrCh <- RunClient(
			ctx,
			func(_ context.Context) (tun.Device, error) { return clientTun, nil },
			func(_ context.Context) (transport.Stream, error) { return clientConn, nil },
			func(stream transport.Stream) (*core.Session, error) {
				return core.ClientHandshakeWithOptions(stream, cid, serverStaticPub, false)
			},
			ClientOptions{
				Sleep: func(_ context.Context, _ time.Duration) error { return nil },
			},
		)
	}()

	up := []byte{0x45, 0x00, 0x00, 0x2c, 0x11}
	clientTun.injectRead(up)
	if got, ok := serverTun.waitWrite(3 * time.Second); !ok || !bytesEqual(got, up) {
		t.Fatalf("client->server packet mismatch")
	}

	down := []byte{0x60, 0x00, 0x00, 0x00, 0x22}
	serverTun.injectRead(down)
	if got, ok := clientTun.waitWrite(3 * time.Second); !ok || !bytesEqual(got, down) {
		t.Fatalf("server->client packet mismatch")
	}

	cancel()
	clientErr := <-clientErrCh
	serverErr := <-serverErrCh
	if !errors.Is(clientErr, context.Canceled) && !errors.Is(clientErr, engine.ErrTransportClosed) {
		t.Fatalf("unexpected client error: %v", clientErr)
	}
	if !errors.Is(serverErr, context.Canceled) && !errors.Is(serverErr, engine.ErrTransportClosed) {
		t.Fatalf("unexpected server error: %v", serverErr)
	}
}

type oneShotListener struct {
	stream transport.Stream
	used   bool
}

func (l *oneShotListener) Accept(ctx context.Context) (transport.Stream, error) {
	if l.used {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	l.used = true
	return l.stream, nil
}

func (l *oneShotListener) Close() error { return nil }

type pipeConn struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (p *pipeConn) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeConn) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeConn) Close() error {
	_ = p.r.Close()
	return p.w.Close()
}

func netPipe() (transport.Stream, transport.Stream) {
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()
	return &pipeConn{r: r1, w: w2}, &pipeConn{r: r2, w: w1}
}

type loopTun struct {
	name string
	mtu  int

	readCh chan []byte
	mu     sync.Mutex
	writes [][]byte

	closeOnce sync.Once
	closed    chan struct{}
}

func newLoopTun(name string, mtu int) *loopTun {
	return &loopTun{
		name:   name,
		mtu:    mtu,
		readCh: make(chan []byte, 32),
		closed: make(chan struct{}),
	}
}

func (d *loopTun) Read(p []byte) (int, error) {
	select {
	case <-d.closed:
		return 0, io.EOF
	case b := <-d.readCh:
		n := copy(p, b)
		return n, nil
	}
}

func (d *loopTun) Write(p []byte) (int, error) {
	select {
	case <-d.closed:
		return 0, io.EOF
	default:
	}
	cp := append([]byte{}, p...)
	d.mu.Lock()
	d.writes = append(d.writes, cp)
	d.mu.Unlock()
	return len(p), nil
}

func (d *loopTun) Close() error {
	d.closeOnce.Do(func() { close(d.closed) })
	return nil
}

func (d *loopTun) Name() string      { return d.name }
func (d *loopTun) MTU() (int, error) { return d.mtu, nil }

func (d *loopTun) injectRead(b []byte) {
	d.readCh <- append([]byte{}, b...)
}

func (d *loopTun) waitWrite(timeout time.Duration) ([]byte, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		if len(d.writes) > 0 {
			b := d.writes[0]
			d.writes = d.writes[1:]
			d.mu.Unlock()
			return b, true
		}
		d.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	return nil, false
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
