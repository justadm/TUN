//go:build linux

package runtime

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"tun/internal/core"
	"tun/internal/engine"
	"tun/internal/transport"
	"tun/internal/tun"
)

func TestRunClientWithRealTunHarness(t *testing.T) {
	if _, err := os.Stat("/dev/net/tun"); err != nil {
		t.Skipf("skip: /dev/net/tun unavailable: %v", err)
	}
	if os.Geteuid() != 0 {
		t.Skip("skip: requires root privileges")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	openTun := func(_ context.Context) (tun.Device, error) {
		dev, err := tun.Open(tun.OpenOptions{})
		if err != nil {
			// Some environments expose /dev/net/tun but still block TUNSETIFF.
			t.Skipf("skip: unable to open real tun device: %v", err)
		}
		return dev, nil
	}
	dial := func(_ context.Context) (transport.Stream, error) {
		return &noopStream{}, nil
	}
	handshake := func(_ transport.Stream) (*core.Session, error) {
		return core.NewSession(core.AEADChaCha20Poly1305, bytes(0xAA, 32), bytes(0xBB, 32)), nil
	}
	runEngine := func(runCtx context.Context, dev tun.Device, _ transport.Stream, _ *core.Session, _ engine.Options) error {
		defer dev.Close()
		if dev.Name() == "" {
			t.Fatalf("expected non-empty tun name")
		}
		if _, err := dev.MTU(); err != nil {
			t.Fatalf("expected mtu query to succeed, got %v", err)
		}
		cancel()
		return runCtx.Err()
	}

	err := RunClient(ctx, openTun, dial, handshake, ClientOptions{
		MaxRetries: 1,
		Sleep: func(_ context.Context, _ time.Duration) error {
			return nil
		},
		RunEngine: runEngine,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}
