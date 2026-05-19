package runtime

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"sync"
	"testing"
	"time"

	"tun/internal/core"
	"tun/internal/engine"
	"tun/internal/transport"
	"tun/internal/transport/tlsstream"
	"tun/internal/tun"
)

func TestSoakTLSStreamWithFaultInjection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	certPath, keyPath := makeTempCertPair(t)
	cfg, err := tlsstream.ServerConfig(certPath, keyPath)
	if err != nil {
		t.Fatalf("server tls config: %v", err)
	}
	ln, err := tlsstream.Listen("127.0.0.1:0", cfg)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	curve := ecdh.X25519()
	serverStaticPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate static server key: %v", err)
	}
	serverStaticPub := serverStaticPriv.PublicKey().Bytes()
	var sid [16]byte
	copy(sid[:], []byte("srv-soak-0000000"))
	var cid [16]byte
	copy(cid[:], []byte("cli-soak-0000000"))

	serverTun := newLoopTun("ssoak0", 1400)
	clientTun := newLoopTun("csoak0", 1400)

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- RunServer(
			ctx,
			func(_ context.Context) (tun.Device, error) { return serverTun, nil },
			ln,
			func(stream transport.Stream) (*core.Session, error) {
				return core.ServerHandshakeWithOptions(stream, sid, serverStaticPriv, false)
			},
			ClientOptions{
				Sleep: func(_ context.Context, _ time.Duration) error { return nil },
			},
		)
	}()

	dialer := &tlsstream.Dialer{
		TLSConfig: tlsstream.ClientConfig("localhost", true),
		Timeout:   5 * time.Second,
	}
	addr := ln.Addr().String()

	var reconnects int
	var recMu sync.Mutex
	clientErrCh := make(chan error, 1)
	go func() {
		clientErrCh <- RunClient(
			ctx,
			func(_ context.Context) (tun.Device, error) { return clientTun, nil },
			func(c context.Context) (transport.Stream, error) {
				s, err := dialer.Dial(c, addr)
				if err != nil {
					return nil, err
				}
				// Inject latency and periodic resets from client side.
				return newFaultyStream(s, 4*time.Millisecond, 220*time.Millisecond), nil
			},
			func(stream transport.Stream) (*core.Session, error) {
				return core.ClientHandshakeWithOptions(stream, cid, serverStaticPub, false)
			},
			ClientOptions{
				Sleep: func(_ context.Context, _ time.Duration) error { return nil },
				OnEvent: func(e Event) {
					if e.State == StateReconnecting {
						recMu.Lock()
						reconnects++
						recMu.Unlock()
					}
				},
			},
		)
	}()

	// Drive traffic both ways during the soak window.
	go pulsePackets(ctx, clientTun, []byte{0x45, 0x00, 0x00, 0x34, 0x11}, 20*time.Millisecond)
	go pulsePackets(ctx, serverTun, []byte{0x60, 0x00, 0x00, 0x00, 0x22}, 20*time.Millisecond)

	clientErr := <-clientErrCh
	serverErr := <-serverErrCh

	if !errors.Is(clientErr, context.DeadlineExceeded) && !errors.Is(clientErr, context.Canceled) && !errors.Is(clientErr, engine.ErrTransportClosed) {
		t.Fatalf("unexpected client err: %v", clientErr)
	}
	if !errors.Is(serverErr, context.DeadlineExceeded) && !errors.Is(serverErr, context.Canceled) && !errors.Is(serverErr, engine.ErrTransportClosed) {
		t.Fatalf("unexpected server err: %v", serverErr)
	}

	recMu.Lock()
	totalReconnects := reconnects
	recMu.Unlock()
	if totalReconnects == 0 {
		t.Fatalf("expected at least one reconnect event")
	}
	if !serverTun.hasWrites() {
		t.Fatalf("expected server to receive packets")
	}
	if !clientTun.hasWrites() {
		t.Fatalf("expected client to receive packets")
	}
}

func pulsePackets(ctx context.Context, d *loopTun, packet []byte, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.injectRead(packet)
		}
	}
}

type faultyStream struct {
	inner transport.Stream
	delay time.Duration
	kill  <-chan time.Time
	once  sync.Once
}

func newFaultyStream(inner transport.Stream, ioDelay time.Duration, killAfter time.Duration) transport.Stream {
	fs := &faultyStream{
		inner: inner,
		delay: ioDelay,
		kill:  time.After(killAfter),
	}
	go func() {
		<-fs.kill
		fs.once.Do(func() { _ = fs.inner.Close() })
	}()
	return fs
}

func (f *faultyStream) Read(p []byte) (int, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.inner.Read(p)
}

func (f *faultyStream) Write(p []byte) (int, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.inner.Write(p)
}

func (f *faultyStream) Close() error {
	var err error
	f.once.Do(func() { err = f.inner.Close() })
	return err
}

func (d *loopTun) hasWrites() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.writes) > 0
}
