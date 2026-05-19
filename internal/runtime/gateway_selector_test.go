package runtime

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"tun/internal/transport"
)

func boolPtr(v bool) *bool { return &v }

func TestNewGatewayFailoverDialerRejectsEmpty(t *testing.T) {
	_, err := NewGatewayFailoverDialer(nil, func(context.Context, GatewayEndpoint) (transport.Stream, error) {
		return nil, nil
	}, GatewaySelectorOptions{})
	if err == nil {
		t.Fatalf("expected error for empty candidates")
	}
}

func TestGatewayFailoverDialerRanksAndSkipsDrain(t *testing.T) {
	cands := []GatewayEndpoint{
		{GatewayID: "gw-drain", Addr: "10.0.0.1:443", Health: "drain", Priority: 100},
		{GatewayID: "gw-b", Addr: "10.0.0.2:443", Health: "healthy", Priority: 5, LoadScore: 5, RTTScore: 5},
		{GatewayID: "gw-a", Addr: "10.0.0.3:443", Health: "healthy", Priority: 10, LoadScore: 60, RTTScore: 40},
	}
	d, err := NewGatewayFailoverDialer(cands, func(context.Context, GatewayEndpoint) (transport.Stream, error) {
		return &noopGatewayStream{}, nil
	}, GatewaySelectorOptions{})
	if err != nil {
		t.Fatalf("new dialer: %v", err)
	}
	ranked := d.RankedEndpoints()
	if len(ranked) != 2 {
		t.Fatalf("expected 2 usable endpoints, got %d", len(ranked))
	}
	if ranked[0].GatewayID != "gw-a" {
		t.Fatalf("expected gw-a ranked first, got %q", ranked[0].GatewayID)
	}
}

func TestGatewayFailoverDialerRespectsStickyAndCooldown(t *testing.T) {
	now := time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC)
	cands := []GatewayEndpoint{
		{GatewayID: "gw-1", Addr: "10.0.0.1:443", Priority: 10},
		{GatewayID: "gw-2", Addr: "10.0.0.2:443", Priority: 9},
	}
	var got []string
	d, err := NewGatewayFailoverDialer(cands, func(_ context.Context, ep GatewayEndpoint) (transport.Stream, error) {
		got = append(got, ep.GatewayID)
		return &noopGatewayStream{}, nil
	}, GatewaySelectorOptions{
		Now:                func() time.Time { return now },
		AutoSelectEnabled:  boolPtr(true),
		StickyDuration:     30 * time.Second,
		FailureCooldownMin: 10 * time.Second,
		FailureCooldownMax: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("new dialer: %v", err)
	}
	if _, err := d.Dial(context.Background()); err != nil {
		t.Fatalf("first dial: %v", err)
	}
	d.ReportDialResult(errors.New("fail gw-1"))
	if _, err := d.Dial(context.Background()); err != nil {
		t.Fatalf("second dial: %v", err)
	}
	if got[0] != "gw-1" || got[1] != "gw-2" {
		t.Fatalf("expected cooldown failover to gw-2, got %v", got)
	}
	snap := d.Snapshot()
	if snap.GatewaySwitches < 1 {
		t.Fatalf("expected at least one gateway switch, got %d", snap.GatewaySwitches)
	}
	if snap.GatewayCooldownSkips < 1 {
		t.Fatalf("expected cooldown skip count, got %d", snap.GatewayCooldownSkips)
	}
}

func TestGatewayFailoverDialerForceGatewayAndNoAutoSelect(t *testing.T) {
	cands := []GatewayEndpoint{
		{GatewayID: "gw-1", Addr: "10.0.0.1:443", Priority: 10},
		{GatewayID: "gw-2", Addr: "10.0.0.2:443", Priority: 9},
	}
	var got []string
	d, err := NewGatewayFailoverDialer(cands, func(_ context.Context, ep GatewayEndpoint) (transport.Stream, error) {
		got = append(got, ep.GatewayID)
		return &noopGatewayStream{}, nil
	}, GatewaySelectorOptions{
		AutoSelectEnabled: boolPtr(false),
		ForceGatewayID:    "gw-2",
	})
	if err != nil {
		t.Fatalf("new dialer: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := d.Dial(context.Background()); err != nil {
			t.Fatalf("dial: %v", err)
		}
		d.ReportDialResult(errors.New("simulate fail"))
	}
	for i, id := range got {
		if id != "gw-2" {
			t.Fatalf("dial %d expected forced gw-2, got %q", i, id)
		}
	}
	snap := d.Snapshot()
	if snap.GatewayAutoSelect {
		t.Fatalf("expected auto-select=false in snapshot")
	}
}

func TestGatewayFailoverDialerReturnsDialErrors(t *testing.T) {
	cands := []GatewayEndpoint{
		{GatewayID: "gw-1", Addr: "10.0.0.1:443", Priority: 1},
	}
	d, err := NewGatewayFailoverDialer(cands, func(_ context.Context, _ GatewayEndpoint) (transport.Stream, error) {
		return nil, errors.New("dial failed")
	}, GatewaySelectorOptions{})
	if err != nil {
		t.Fatalf("new dialer: %v", err)
	}
	_, err = d.Dial(context.Background())
	if err == nil || err.Error() != "dial failed" {
		t.Fatalf("expected dial failed error, got %v", err)
	}
}

func TestGatewayFailoverDialerSwitchHysteresisKeepsCurrentGateway(t *testing.T) {
	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	cands := []GatewayEndpoint{
		{GatewayID: "gw-1", Addr: "10.0.0.1:443", Priority: 10, LoadScore: 50, RTTScore: 50},
		{GatewayID: "gw-2", Addr: "10.0.0.2:443", Priority: 10, LoadScore: 48, RTTScore: 49},
	}
	var got []string
	d, err := NewGatewayFailoverDialer(cands, func(_ context.Context, ep GatewayEndpoint) (transport.Stream, error) {
		got = append(got, ep.GatewayID)
		return &noopGatewayStream{}, nil
	}, GatewaySelectorOptions{
		Now:                func() time.Time { return now },
		AutoSelectEnabled:  boolPtr(true),
		StickyDuration:     1 * time.Second,
		FailureCooldownMin: 10 * time.Second,
		FailureCooldownMax: 10 * time.Second,
		SwitchHysteresis:   20,
	})
	if err != nil {
		t.Fatalf("new dialer: %v", err)
	}

	if _, err := d.Dial(context.Background()); err != nil {
		t.Fatalf("dial #1: %v", err)
	}
	d.ReportDialResult(errors.New("fail gw-2"))

	if _, err := d.Dial(context.Background()); err != nil {
		t.Fatalf("dial #2: %v", err)
	}
	d.ReportDialResult(nil)

	now = now.Add(11 * time.Second)
	if _, err := d.Dial(context.Background()); err != nil {
		t.Fatalf("dial #3: %v", err)
	}
	d.ReportDialResult(nil)

	if len(got) != 3 || got[0] != "gw-2" || got[1] != "gw-1" || got[2] != "gw-1" {
		t.Fatalf("expected hysteresis to keep gw-1 on third dial, got %v", got)
	}
	snap := d.Snapshot()
	if snap.GatewayHysteresisKeeps < 1 {
		t.Fatalf("expected hysteresis keep counter > 0, got %+v", snap)
	}
}

func TestGatewayFailoverDialerSwitchHysteresisAllowsLargeImprovement(t *testing.T) {
	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	cands := []GatewayEndpoint{
		{GatewayID: "gw-1", Addr: "10.0.0.1:443", Priority: 10, LoadScore: 95, RTTScore: 95},
		{GatewayID: "gw-2", Addr: "10.0.0.2:443", Priority: 10, LoadScore: 10, RTTScore: 10},
	}
	var got []string
	d, err := NewGatewayFailoverDialer(cands, func(_ context.Context, ep GatewayEndpoint) (transport.Stream, error) {
		got = append(got, ep.GatewayID)
		return &noopGatewayStream{}, nil
	}, GatewaySelectorOptions{
		Now:                func() time.Time { return now },
		AutoSelectEnabled:  boolPtr(true),
		StickyDuration:     1 * time.Second,
		FailureCooldownMin: 5 * time.Second,
		FailureCooldownMax: 5 * time.Second,
		SwitchHysteresis:   20,
	})
	if err != nil {
		t.Fatalf("new dialer: %v", err)
	}

	// Force gw-2 cooldown so gw-1 becomes selected.
	if _, err := d.Dial(context.Background()); err != nil {
		t.Fatalf("dial #1: %v", err)
	}
	d.ReportDialResult(errors.New("fail gw-2"))

	if _, err := d.Dial(context.Background()); err != nil {
		t.Fatalf("dial #2: %v", err)
	}
	d.ReportDialResult(nil)

	now = now.Add(7 * time.Second)
	if _, err := d.Dial(context.Background()); err != nil {
		t.Fatalf("dial #3: %v", err)
	}
	d.ReportDialResult(nil)

	if len(got) != 3 || got[0] != "gw-2" || got[1] != "gw-1" || got[2] != "gw-2" {
		t.Fatalf("expected large score delta to switch back to gw-2, got %v", got)
	}
	snap := d.Snapshot()
	if snap.GatewayHysteresisKeeps != 0 {
		t.Fatalf("expected no hysteresis keep on large delta, got %+v", snap)
	}
}

type noopGatewayStream struct{}

func (n *noopGatewayStream) Read(_ []byte) (int, error)  { return 0, io.EOF }
func (n *noopGatewayStream) Write(p []byte) (int, error) { return len(p), nil }
func (n *noopGatewayStream) Close() error                { return nil }
