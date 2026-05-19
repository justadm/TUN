package runtime

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"tun/internal/core"
	"tun/internal/engine"
	"tun/internal/transport"
	"tun/internal/tun"
)

func TestRunServerEstablishesAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	l := &singleListener{stream: serverConn}
	openTun := func(_ context.Context) (tun.Device, error) {
		return &noopTun{}, nil
	}
	handshake := func(stream transport.Stream) (*core.Session, error) {
		_ = stream
		return core.NewSession(core.AEADChaCha20Poly1305, fillBytes(0x31, 32), fillBytes(0x32, 32)), nil
	}
	runCalls := 0
	runEngine := func(_ context.Context, _ tun.Device, _ transport.Stream, _ *core.Session, opts engine.Options) error {
		runCalls++
		if opts.OutDirection != 0x01 || opts.InDirection != 0x00 {
			t.Fatalf("expected server directions 0x01/0x00, got 0x%x/0x%x", opts.OutDirection, opts.InDirection)
		}
		cancel()
		return context.Canceled
	}

	err := RunServer(ctx, openTun, l, handshake, ClientOptions{
		RunEngine: runEngine,
		Sleep: func(_ context.Context, _ time.Duration) error {
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if runCalls != 1 {
		t.Fatalf("expected one engine run, got %d", runCalls)
	}
}

func TestRunServerRetriesOnAcceptError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	l := &scriptedListener{
		steps: []acceptStep{
			{err: errors.New("temporary accept failure")},
			{stream: serverConn},
		},
	}
	openTun := func(_ context.Context) (tun.Device, error) {
		return &noopTun{}, nil
	}
	handshake := func(_ transport.Stream) (*core.Session, error) {
		return core.NewSession(core.AEADChaCha20Poly1305, fillBytes(0x41, 32), fillBytes(0x42, 32)), nil
	}
	var waits []time.Duration
	runEngine := func(_ context.Context, _ tun.Device, _ transport.Stream, _ *core.Session, _ engine.Options) error {
		cancel()
		return context.Canceled
	}

	err := RunServer(ctx, openTun, l, handshake, ClientOptions{
		RunEngine: runEngine,
		Sleep: func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if len(waits) != 1 {
		t.Fatalf("expected one retry wait, got %d", len(waits))
	}
}

type singleListener struct {
	stream transport.Stream
	used   bool
}

func (s *singleListener) Accept(ctx context.Context) (transport.Stream, error) {
	if s.used {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s.used = true
	return s.stream, nil
}

func (s *singleListener) Close() error { return nil }

type acceptStep struct {
	stream transport.Stream
	err    error
}

type scriptedListener struct {
	mu    sync.Mutex
	steps []acceptStep
}

func (s *scriptedListener) Accept(ctx context.Context) (transport.Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steps) == 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	if step.err != nil {
		return nil, step.err
	}
	return step.stream, nil
}

func (s *scriptedListener) Close() error { return nil }
