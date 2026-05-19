# TUN Tunnel: Detailed Production Execution Plan

Date: 2026-04-13
Status: Proposed execution plan
Scope: `tun-rnd` -> production-ready tunnel runtime and operations

## 1) Goal and exit condition

Bring `tun-rnd` from R&D runtime to stable production tunnel core with:

- predictable cryptographic lifecycle (`full rekey v1`);
- resilient transport under packet loss/latency/jitter;
- stable multi-gateway behavior with controlled failover;
- measurable SLO/SLI and rollback-safe release gates.

Exit condition:

- 3 consecutive production-like rollout waves pass gates;
- no critical regressions in handshake/session/failover/security;
- SRE runbooks and rollback paths validated in drills.

## 2) Current baseline (as of 2026-04-13)

- Runtime/helper command plane is significantly expanded:
  - links read/actions, failover action, security evaluate/ingest/policy/audit.
- Gateway selection and failover basics exist.
- Security control plane and tenant rollout are implemented.
- Strategic tunnel gap remains:
  - `full rekey v1` not implemented yet (still reconnect-rotation model in practice).

## 3) Workstreams

### WS-A: Tunnel Protocol and Crypto Lifecycle

Objective:
- Implement and harden `full rekey v1` with no tunnel teardown for normal rotation.

Tasks:
- A1. Design rekey state machine:
  - `steady -> rekey_pending -> overlap -> cutover -> settled -> rollback(optional)`.
- A2. Add control messages for rekey negotiation/ack.
- A3. Add dual-key overlap window (old/new) for in-flight packet tolerance.
- A4. Add session lifetime policies:
  - max duration,
  - max bytes,
  - forced rekey threshold.
- A5. Add replay-window validation under key rollover.
- A6. Add interoperability fallback:
  - peer without rekey support -> controlled reconnect-rotation.

Deliverables:
- protocol spec update (`docs/contracts` or protocol note),
- runtime/client/server implementation,
- tests for happy path + rollback + mismatch.

Acceptance:
- rekey performed without disconnect in normal path,
- packet loss during overlap does not break session,
- deterministic fallback when peer cannot rekey.

---

### WS-B: Transport Resilience and Data Plane

Objective:
- Stabilize throughput/latency under real-world network variance.

Tasks:
- B1. PMTU strategy:
  - blackhole detection,
  - fallback MTU ladder.
- B2. Adaptive keepalive:
  - jittered intervals by link health class.
- B3. Retry tuning:
  - class-specific backoff caps,
  - burst reconnect damping.
- B4. Pump optimization:
  - buffer pool tuning,
  - batch semantics where safe.
- B5. Long soak validation:
  - 24h/72h scenarios on real links.

Deliverables:
- transport policy config defaults,
- soak test scripts and reports.

Acceptance:
- no reconnect storms under synthetic loss,
- stable memory/cpu under soak,
- failover time within SLO.

---

### WS-C: Multi-Gateway Runtime Behavior

Objective:
- Predictable gateway selection and graceful switch behavior.

Tasks:
- C1. Gateway quality score inputs:
  - RTT, recent dial success, cooldown state, health signal.
- C2. Sticky and cooldown revalidation:
  - prevent flapping between close scores.
- C3. Controlled failover semantics:
  - fast path on hard errors,
  - conservative path on ambiguous degradation.
- C4. Policy hooks:
  - forced gateway,
  - no-auto-switch tenant/profile modes.

Deliverables:
- selector algorithm note,
- runtime metrics for switch reason taxonomy.

Acceptance:
- bounded switch frequency,
- improved success rate vs baseline under partial outages.

---

### WS-D: Observability, SRE, and Release Safety

Objective:
- Gate releases by measured tunnel behavior, not process liveness only.

Tasks:
- D1. SLI/metrics model:
  - handshake latency p50/p95,
  - reconnect rate,
  - rekey success rate,
  - failover duration,
  - packet loss estimate.
- D2. Health model:
  - link-state-driven readiness,
  - degraded/failed state transitions.
- D3. Release gates:
  - canary gate,
  - wave gate,
  - global stop condition.
- D4. Rollback drills:
  - scripted rollback on each contour.
- D5. Incident runbooks:
  - “reconnect storm”,
  - “gateway flap”,
  - “rekey mismatch”.

Deliverables:
- gate scripts updates,
- runbook docs,
- validated rollback scripts.

Acceptance:
- failed canary auto-stops wave,
- rollback completes inside target RTO.

---

### WS-E: Security Hardening Around Tunnel Runtime

Objective:
- Close remaining runtime/control-plane security gaps.

Tasks:
- E1. Rate limits/throttles for helper mutating endpoints.
- E2. Signed bootstrap/runtime policy bundles (integrity + key id).
- E3. Key rotation runbook (`active/next` window).
- E4. Audit completeness checks:
  - action -> event -> snapshot correlation.
- E5. Optional mTLS pinning policy for management channels.

Deliverables:
- security policy doc,
- tests for abuse and replay classes.

Acceptance:
- no unaudited mutating action path,
- deterministic reject for tampered/signed-invalid configs.

## 4) Milestones and timeline (2-week execution window)

### Week 1 (Protocol + core runtime)

- M1: Rekey v1 design + protocol draft frozen.
- M2: Client/server runtime rekey implementation (feature-flagged).
- M3: Unit/integration tests for rekey + fallback.
- M4: Initial soak (24h) on lab links.

Gate:
- all tests green,
- no critical defects in rekey flow.

### Week 2 (Resilience + rollout safety)

- M5: PMTU + adaptive keepalive + retry tuning.
- M6: Gateway scoring stabilization + anti-flap.
- M7: SLI dashboards + release gates + rollback drills.
- M8: Canary rollout on selected mesh and report.

Gate:
- SLO targets met in canary,
- rollback drill passed.

## 5) Implementation map by code areas

- `internal/core`
  - protocol controls for rekey negotiation and key transition.
- `internal/engine`
  - overlap key handling in data/control path,
  - replay/window behavior under key switch.
- `internal/runtime`
  - session lifecycle and rekey scheduler,
  - transport policy + metrics emission.
- `cmd/runtime-client`, `cmd/runtime-server`, `cmd/runtime-server-systemd`
  - flags/config for rekey policy and observability.
- `cmd/runtime-helper`, `cmd/runtime-helperctl`
  - control actions/telemetry for rekey and resilience operations.
- `scripts/`
  - soak, canary, rollback, release gates.

## 6) Test matrix (must-have)

1. Rekey happy path:
- stable session, traffic preserved, counters monotonic.

2. Rekey overlap under packet disorder:
- no false replay reject in overlap window.

3. Rekey rollback path:
- invalid ack/mismatch -> safe fallback.

4. Legacy peer compatibility:
- fallback reconnect-rotation without deadlock.

5. Gateway failover under impairment:
- forced packet loss/latency; bounded failover time.

6. Long soak:
- 24h mandatory; 72h before broad rollout.

7. Security and abuse:
- replay attempts, malformed control frames, endpoint rate abuse.

## 7) SLO targets for production decision

- Handshake success rate: >= 99.5%
- Rekey success rate: >= 99.0%
- Median reconnect recovery: <= 3s
- Failover completion (p95): <= 8s
- Crash-free runtime uptime in soak: >= 99.9%

## 8) Rollout strategy

1. Stage-0 (lab):
- feature flag on, synthetic fault injection.

2. Stage-1 (single contour canary):
- limited tenant subset.

3. Stage-2 (multi-contour wave):
- expand by gateway group.

4. Stage-3 (default-on):
- keep kill-switch and rollback.

Stop conditions:
- critical crypto mismatch,
- reconnect storm,
- failover p95 regression above threshold.

## 9) Immediate backlog (ordered execution list)

1. Draft and freeze `full rekey v1` protocol note.
2. Implement runtime rekey scheduler + dual-key overlap.
3. Add helper endpoints for rekey status/action (`read/start/force`).
4. Add test suite for rekey lifecycle and replay edge cases.
5. Add PMTU blackhole fallback and adaptive keepalive.
6. Add gateway anti-flap scoring adjustments.
7. Update gate scripts with rekey/failover SLI checks.
8. Execute canary + soak and publish report.

## 10) Risks and mitigation

- Risk: protocol complexity causes rare session desync.
  - Mitigation: overlap window + deterministic rollback + heavy fault injection.
- Risk: over-sensitive failover scoring causes flapping.
  - Mitigation: hysteresis and cooldown floors.
- Risk: new controls overload ops.
  - Mitigation: clear runbooks + staged rollout + one-button rollback scripts.

## 11) Definition of Done

Workstream is done when:

- code merged and tests green,
- operational runbook updated,
- release gate updated,
- canary evidence attached (metrics + incident-free window).
