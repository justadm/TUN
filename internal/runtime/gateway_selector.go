package runtime

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"tun/internal/transport"
)

type GatewayEndpoint struct {
	GatewayID  string
	Region     string
	Addr       string
	ServerName string
	Health     string
	Priority   int
	LoadScore  int
	RTTScore   int
}

type GatewayDialFunc func(ctx context.Context, endpoint GatewayEndpoint) (transport.Stream, error)

type GatewaySelectorOptions struct {
	Now                func() time.Time
	AutoSelectEnabled  *bool
	ForceGatewayID     string
	StickyDuration     time.Duration
	FailureCooldownMin time.Duration
	FailureCooldownMax time.Duration
	SwitchHysteresis   int
}

type GatewaySelectionSnapshot struct {
	SelectedGatewayID      string
	SelectedGatewayAddr    string
	GatewaySelections      int
	GatewaySwitches        int
	GatewayCooldownSkips   int
	GatewayHysteresisKeeps int
	GatewayAutoSelect      bool
}

type endpointRuntimeState struct {
	endpoint      GatewayEndpoint
	failures      int
	cooldownUntil time.Time
}

type GatewayFailoverDialer struct {
	mu sync.Mutex

	candidates []endpointRuntimeState
	dialFn     GatewayDialFunc
	opts       GatewaySelectorOptions

	lastSelected int
	lastDialed   int
	stickyUntil  time.Time

	selections      int
	switches        int
	cooldownSkips   int
	hysteresisKeeps int
}

func NewGatewayFailoverDialer(candidates []GatewayEndpoint, dialFn GatewayDialFunc, opts GatewaySelectorOptions) (*GatewayFailoverDialer, error) {
	if dialFn == nil {
		return nil, errors.New("runtime: gateway dial function is required")
	}
	ranked := rankGatewayEndpoints(candidates)
	if len(ranked) == 0 {
		return nil, errors.New("runtime: no valid gateway endpoints")
	}
	fillGatewaySelectorDefaults(&opts)
	states := make([]endpointRuntimeState, 0, len(ranked))
	for _, ep := range ranked {
		states = append(states, endpointRuntimeState{endpoint: ep})
	}
	return &GatewayFailoverDialer{
		candidates:   states,
		dialFn:       dialFn,
		opts:         opts,
		lastSelected: -1,
		lastDialed:   -1,
	}, nil
}

func (d *GatewayFailoverDialer) Dial(ctx context.Context) (transport.Stream, error) {
	ep, idx := d.pickEndpoint(d.opts.Now())
	d.mu.Lock()
	d.lastDialed = idx
	d.selections++
	if d.lastSelected >= 0 && d.lastSelected != idx {
		d.switches++
	}
	d.lastSelected = idx
	d.mu.Unlock()
	return d.dialFn(ctx, ep)
}

func (d *GatewayFailoverDialer) ReportDialResult(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lastDialed < 0 || d.lastDialed >= len(d.candidates) {
		return
	}
	cur := &d.candidates[d.lastDialed]
	if err == nil {
		cur.failures = 0
		cur.cooldownUntil = time.Time{}
		if d.opts.StickyDuration > 0 {
			d.stickyUntil = d.opts.Now().Add(d.opts.StickyDuration)
		}
		return
	}
	cur.failures++
	cooldown := d.opts.FailureCooldownMin
	for i := 1; i < cur.failures; i++ {
		cooldown = cooldown * 2
		if cooldown >= d.opts.FailureCooldownMax {
			cooldown = d.opts.FailureCooldownMax
			break
		}
	}
	if cooldown <= 0 {
		cooldown = d.opts.FailureCooldownMin
	}
	cur.cooldownUntil = d.opts.Now().Add(cooldown)
}

func (d *GatewayFailoverDialer) RankedEndpoints() []GatewayEndpoint {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]GatewayEndpoint, 0, len(d.candidates))
	for _, c := range d.candidates {
		out = append(out, c.endpoint)
	}
	return out
}

func (d *GatewayFailoverDialer) Snapshot() GatewaySelectionSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := GatewaySelectionSnapshot{
		GatewaySelections:      d.selections,
		GatewaySwitches:        d.switches,
		GatewayCooldownSkips:   d.cooldownSkips,
		GatewayHysteresisKeeps: d.hysteresisKeeps,
		GatewayAutoSelect:      gatewayAutoSelectEnabled(d.opts),
	}
	if d.lastSelected >= 0 && d.lastSelected < len(d.candidates) {
		out.SelectedGatewayID = d.candidates[d.lastSelected].endpoint.GatewayID
		out.SelectedGatewayAddr = d.candidates[d.lastSelected].endpoint.Addr
	}
	return out
}

func (d *GatewayFailoverDialer) pickEndpoint(now time.Time) (GatewayEndpoint, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.candidates) == 0 {
		return GatewayEndpoint{}, -1
	}
	matchesForce := func(ep GatewayEndpoint) bool {
		forceID := strings.TrimSpace(d.opts.ForceGatewayID)
		if forceID == "" {
			return true
		}
		return ep.GatewayID == forceID
	}
	allowed := make([]int, 0, len(d.candidates))
	for i := range d.candidates {
		if matchesForce(d.candidates[i].endpoint) {
			allowed = append(allowed, i)
		}
	}
	if len(allowed) == 0 {
		for i := range d.candidates {
			allowed = append(allowed, i)
		}
	}
	if !gatewayAutoSelectEnabled(d.opts) {
		idx := allowed[0]
		return d.candidates[idx].endpoint, idx
	}
	if d.lastSelected >= 0 && d.lastSelected < len(d.candidates) && now.Before(d.stickyUntil) && matchesForce(d.candidates[d.lastSelected].endpoint) {
		return d.candidates[d.lastSelected].endpoint, d.lastSelected
	}
	firstAvailable := -1
	for _, idx := range allowed {
		if now.Before(d.candidates[idx].cooldownUntil) {
			d.cooldownSkips++
			continue
		}
		firstAvailable = idx
		break
	}
	if firstAvailable >= 0 {
		if d.opts.SwitchHysteresis > 0 &&
			d.lastSelected >= 0 &&
			d.lastSelected < len(d.candidates) &&
			d.lastSelected != firstAvailable &&
			matchesForce(d.candidates[d.lastSelected].endpoint) &&
			!now.Before(d.candidates[d.lastSelected].cooldownUntil) {
			bestScore := gatewayScore(d.candidates[firstAvailable].endpoint)
			curScore := gatewayScore(d.candidates[d.lastSelected].endpoint)
			if bestScore-curScore < d.opts.SwitchHysteresis {
				d.hysteresisKeeps++
				return d.candidates[d.lastSelected].endpoint, d.lastSelected
			}
		}
		return d.candidates[firstAvailable].endpoint, firstAvailable
	}
	return d.candidates[allowed[0]].endpoint, allowed[0]
}

func rankGatewayEndpoints(candidates []GatewayEndpoint) []GatewayEndpoint {
	usable := make([]GatewayEndpoint, 0, len(candidates))
	for _, c := range candidates {
		c.GatewayID = strings.TrimSpace(c.GatewayID)
		c.Region = strings.TrimSpace(c.Region)
		c.Addr = strings.TrimSpace(c.Addr)
		c.ServerName = strings.TrimSpace(c.ServerName)
		c.Health = strings.ToLower(strings.TrimSpace(c.Health))
		if c.Addr == "" {
			continue
		}
		if c.GatewayID == "" {
			c.GatewayID = "gateway"
		}
		if c.Health == "drain" {
			continue
		}
		c.LoadScore = clampInt(c.LoadScore, 0, 100)
		c.RTTScore = clampInt(c.RTTScore, 0, 100)
		usable = append(usable, c)
	}
	sort.SliceStable(usable, func(i, j int) bool {
		si := gatewayScore(usable[i])
		sj := gatewayScore(usable[j])
		if si != sj {
			return si > sj
		}
		if usable[i].GatewayID != usable[j].GatewayID {
			return usable[i].GatewayID < usable[j].GatewayID
		}
		return usable[i].Addr < usable[j].Addr
	})
	return usable
}

func gatewayScore(c GatewayEndpoint) int {
	healthBias := 0
	if c.Health == "degraded" {
		healthBias = -50
	}
	return (c.Priority * 1000) + ((100 - c.RTTScore) * 10) + (100 - c.LoadScore) + healthBias
}

func fillGatewaySelectorDefaults(opts *GatewaySelectorOptions) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.StickyDuration <= 0 {
		opts.StickyDuration = 10 * time.Minute
	}
	if opts.FailureCooldownMin <= 0 {
		opts.FailureCooldownMin = 3 * time.Second
	}
	if opts.FailureCooldownMax <= 0 {
		opts.FailureCooldownMax = 2 * time.Minute
	}
	if opts.FailureCooldownMax < opts.FailureCooldownMin {
		opts.FailureCooldownMax = opts.FailureCooldownMin
	}
	if opts.SwitchHysteresis < 0 {
		opts.SwitchHysteresis = 0
	}
}

func gatewayAutoSelectEnabled(opts GatewaySelectorOptions) bool {
	if opts.AutoSelectEnabled == nil {
		return true
	}
	return *opts.AutoSelectEnabled
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
