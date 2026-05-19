# JsTun

JsTun is an access platform. This repository contains the platform architecture, edge/network operations artifacts, and the R&D track for a custom transport core in Go.

## Product Scope

The repository is organized into three domains:

- `control-plane`: portal, admin, provisioning, control API, billing integration, operational docs
- `edge/datapath`: edge topology, routing, geo-sync, uplinks, failover, deployment/ops scripts
- `tun-rnd`: custom transport/core protocol research and implementation in Go

Current product position:

- production direction: access platform built around WireGuard-based ingress and managed edges
- R&D direction: custom TUN transport as a future uplink/transport option, not the current product baseline

## Repository layout

- `cmd/client`: `tun-rnd` client entrypoint
- `cmd/runtime-client`: `tun-rnd` runtime entrypoint (state machine + packet loop)
- `cmd/runtime-helper`: local helper IPC/API process exposing `start/stop/status/health/collectSupportBundle`
- `cmd/runtime-helperctl`: CLI client for `/v1/helper/*` with schema validation and auth/unix-socket support
- `cmd/runtime-preflight`: environment preflight checker for runtime TUN startup
- `cmd/runtime-server`: `tun-rnd` runtime server entrypoint (listener + state machine + packet loop)
- `cmd/runtime-server-systemd`: systemd-oriented runtime server entrypoint with sd_notify support
- `cmd/support-bundle-verify`: ingestion-time verifier for support bundle envelopes and signing key rotation policy
- `cmd/server`: `tun-rnd` server entrypoint
- `internal/core`: `tun-rnd` protocol core
- `internal/transport`: `tun-rnd` transport interfaces and adapters
- `internal/tun`: `tun-rnd` TUN abstraction
- `docs/`: current product, app, and planning documents intended for active use
- `.docs/`: legacy/internal planning artifacts and protocol-spec archive
- `deploy/systemd`: runtime systemd unit and env templates

Runtime launcher flags (client/server/systemd variants):

- `-health-addr`: optional health endpoint (`/live`, `/ready`, `/status`)
- `-tun-mtu`: optional TUN MTU applied on open (`0` leaves system default)
- `-tun-skip-up`: skip automatic link-up (`IFF_UP`) on TUN open
- `-tun-addresses`: optional comma-separated interface CIDRs to apply on open (`ip addr replace`)
- `-tun-routes`: optional comma-separated route CIDRs (or `default`) to apply on open (`ip route replace`)
- `-tun-config-mode`: address/route apply mode, `replace` (default) or `add`
- `-tun-cleanup-on-close`: remove configured addresses/routes on device close (best-effort)
- `-event-json-log`: optional JSON lines event log sink
- `-event-log-rotate-bytes`: size-based event log rotation threshold
- `-event-log-rotate-interval`: time-based event log rotation interval
- `-event-log-max-backups`: max rotated event log backups retained
- `-support-bundle-out`: optional support bundle JSON export on process exit
- `-support-ring`: optional deployment ring in support bundle
- `-support-host-id`: optional host id in support bundle
- `-runtime-version`: runtime version metadata for support bundle
- `-build-info`: build metadata for support bundle
- `-support-signing-key-file`: optional HMAC key file for signed support-bundle envelope
- `-support-signing-key-id`: optional key id included in support-bundle envelope

Support bundle output is emitted as an envelope with:

- bundle checksum (`sha256`)
- optional HMAC signature (`sha256`) when signing key is configured

Linux runtime preflight:

- runtime launchers fail fast before startup when TUN preflight fails
- checks include:
  - root or `CAP_NET_ADMIN`
  - accessible `/dev/net/tun`
  - `ip` command availability when address/route config is requested

Standalone preflight (JSON report + exit code):

```bash
go run ./cmd/runtime-preflight \
  -tun-name tun0 \
  -tun-mtu 1420 \
  -tun-addresses 10.66.0.1/24 \
  -tun-routes 10.66.0.0/24 \
  -tun-config-mode replace
```

Helper API (desktop/mobile integration contract pilot):

- `POST /start` with `profileBootstrap` and `deviceID`
- `POST /stop`
- `GET /status`
- `GET /health`
- `POST /collectSupportBundle`
- helper-architecture aliases:
  - `POST /bridge.startup`
  - `POST /bridge.shutdown`
  - `POST /bridge.reconcile`
  - `POST /bridge.autopilot`
  - `POST /bridge.autopilot.once`
  - `POST /bridge.autopilot.daemon`
  - `POST /bridge.autopilot.daemon.stream`
  - `GET /bridge.status.stream`
  - `POST /lease.acquire`
  - `POST /lease.renew`
  - `POST /lease.heartbeat`
  - `POST /lease.takeover`
  - `POST /lease.release`
  - `GET /lease.status`
  - `POST /bootstrap.validate`
  - `POST /bootstrap.apply`
  - `POST /tunnel.start`
  - `POST /tunnel.stop`
  - `POST /tunnel.refresh`
  - `GET /stats.read`
  - `POST /diagnostics.export`
- versioned API (preferred for new integrations):
  - `GET /v1/helper/schema`
  - `POST /v1/helper/bridge.startup`
  - `POST /v1/helper/bridge.shutdown`
  - `POST /v1/helper/bridge.reconcile`
  - `POST /v1/helper/bridge.autopilot`
  - `POST /v1/helper/bridge.autopilot.once`
  - `POST /v1/helper/bridge.autopilot.daemon`
  - `POST /v1/helper/bridge.autopilot.daemon.stream`
  - `GET /v1/helper/bridge.status.stream`
  - `POST /v1/helper/lease.acquire`
  - `POST /v1/helper/lease.renew`
  - `POST /v1/helper/lease.heartbeat`
  - `POST /v1/helper/lease.takeover`
  - `POST /v1/helper/lease.release`
  - `GET /v1/helper/lease.status`
  - `POST /v1/helper/bootstrap.validate`
  - `POST /v1/helper/bootstrap.apply`
  - `POST /v1/helper/tunnel.start`
  - `POST /v1/helper/tunnel.stop`
  - `POST /v1/helper/tunnel.refresh`
  - `GET /v1/helper/status`
  - `GET /v1/helper/health`
  - `GET /v1/helper/stats.read`
  - `GET /v1/helper/wait?state=established&timeout=20s`
  - `GET /v1/helper/events` (SSE stream)
  - `POST /v1/helper/diagnostics.export`

Helper transport:

- default: `127.0.0.1:19090` (TCP loopback)
- recommended for desktop helper IPC: `-unix-socket /path/to/helper.sock` (socket mode `0600`)
- optional persistent helper state: `-state-file /path/runtime-helper-state.json`
- optional helper API auth: `-auth-token-file /path/helper.token`
  - send token via `Authorization: Bearer <token>` or `X-Helper-Token: <token>`

Helper CLI examples:

```bash
# Schema validation and status read over unix socket with token.
go run ./cmd/runtime-helperctl \
  -action status \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token

# Apply bootstrap payload (JSON file).
go run ./cmd/runtime-helperctl \
  -action lease.acquire \
  -lease-owner desktop-bridge \
  -lease-ttl 60s \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token

go run ./cmd/runtime-helperctl \
  -action bootstrap.validate \
  -lease-id <lease-id-from-lease.acquire> \
  -request-id bootstrap-validate-001 \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token \
  -payload-file ./bootstrap.json

# Note: bootstrap.validate returns HTTP 200 with "ok:false" when payload is valid
# but preflight fails on current host/environment.

go run ./cmd/runtime-helperctl \
  -action bootstrap.apply \
  -lease-id <lease-id-from-lease.acquire> \
  -request-id bootstrap-001 \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token \
  -payload-file ./bootstrap.json

# Keep lease active while UI/bridge owns helper session.
# By default, helperctl sends lease.release on SIGINT/SIGTERM before exit.
go run ./cmd/runtime-helperctl \
  -action lease.keepalive \
  -lease-id <lease-id-from-lease.acquire> \
  -lease-ttl 60s \
  -lease-keepalive-interval 20s \
  -lease-keepalive-duration 2m \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token

# Controlled takeover (for JsTun bridge handoff): use current leaseId from status
# as -lease-prev-id to avoid unsafe blind takeover.
go run ./cmd/runtime-helperctl \
  -action lease.takeover \
  -lease-owner desktop-bridge-v2 \
  -lease-prev-id <current-lease-id-from-status> \
  -lease-ttl 60s \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token

# Bridge-safe automatic lease ensure (acquire or takeover based on status).
go run ./cmd/runtime-helperctl \
  -action lease.ensure \
  -lease-owner desktop-bridge \
  -lease-ttl 60s \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token

# One-shot bridge startup orchestration (JsTun integration path):
# lease.ensure -> bootstrap.validate -> bootstrap.apply -> tunnel.start -> wait.
go run ./cmd/runtime-helperctl \
  -action bridge.startup \
  -lease-owner desktop-bridge \
  -lease-ttl 60s \
  -bridge-wait=true \
  -wait-state established \
  -wait-timeout 20s \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token \
  -payload-file ./bootstrap.json

# One-shot bridge shutdown orchestration:
# lease.ensure -> tunnel.stop -> lease.release (best effort by default).
go run ./cmd/runtime-helperctl \
  -action bridge.shutdown \
  -lease-owner desktop-bridge \
  -lease-ttl 60s \
  -bridge-shutdown-best-effort=true \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token

# Bridge reconcile (for JsTun control-loop): computes required plan.
# plan: startup-needed | running-ok | restart-needed
go run ./cmd/runtime-helperctl \
  -action bridge.reconcile \
  -lease-owner desktop-bridge \
  -lease-ttl 60s \
  -bridge-reconcile-ensure-lease=true \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token

# Bridge autopilot (for JsTun): one control-loop command with retry budget.
# It reacts to reconcile plan and executes startup/restart policy automatically.
go run ./cmd/runtime-helperctl \
  -action bridge.autopilot \
  -lease-owner desktop-bridge \
  -lease-ttl 60s \
  -bridge-autopilot-max-steps 3 \
  -bridge-autopilot-allow-restart=true \
  -bridge-reconcile-ensure-lease=true \
  -bridge-wait=true \
  -wait-state established \
  -wait-timeout 20s \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token \
  -payload-file ./bootstrap.json

# Bridge autopilot daemon (for JsTun background controller):
# runs autopilot periodically until SIGINT/SIGTERM or configured duration.
go run ./cmd/runtime-helperctl \
  -action bridge.autopilot.daemon \
  -lease-owner desktop-bridge \
  -lease-ttl 60s \
  -bridge-autopilot-max-steps 3 \
  -bridge-autopilot-allow-restart=true \
  -bridge-autopilot-interval 20s \
  -bridge-autopilot-duration 10m \
  -bridge-autopilot-continue-on-error=true \
  -bridge-reconcile-ensure-lease=true \
  -bridge-wait=true \
  -wait-state established \
  -wait-timeout 20s \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token \
  -payload-file ./bootstrap.json

# Bridge autopilot daemon stream mode (prints SSE tick data lines).
go run ./cmd/runtime-helperctl \
  -action bridge.autopilot.daemon.stream \
  -lease-owner desktop-bridge \
  -lease-ttl 60s \
  -bridge-autopilot-max-steps 3 \
  -bridge-autopilot-allow-restart=true \
  -bridge-autopilot-interval 20s \
  -bridge-autopilot-duration 2m \
  -bridge-autopilot-continue-on-error=true \
  -bridge-reconcile-ensure-lease=true \
  -bridge-wait=true \
  -wait-state established \
  -wait-timeout 20s \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token \
  -payload-file ./bootstrap.json

# Unified bridge status stream (snapshot + runtime + daemon ticks).
go run ./cmd/runtime-helperctl \
  -action bridge.status.stream \
  -bridge-status-interval 5s \
  -bridge-status-duration 30s \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token

# Read current runtime + lease snapshot.
go run ./cmd/runtime-helperctl \
  -action status \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token

# Wait until runtime becomes established (or timeout).
go run ./cmd/runtime-helperctl \
  -action wait \
  -lease-id <lease-id-from-lease.acquire> \
  -wait-state established \
  -wait-timeout 20s \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token

# Stream helper runtime events (SSE) for 30 seconds.
go run ./cmd/runtime-helperctl \
  -action events \
  -lease-id <lease-id-from-lease.acquire> \
  -events-duration 30s \
  -unix-socket /var/run/tun/runtime-helper.sock \
  -token-file /etc/tun/runtime-helper.token
```

Helper API reliability contract:

- propagate `X-Request-ID` on all calls for traceability
- when helper lease is active, send `X-Helper-Lease-ID` (or `-lease-id` in helperctl) for mutating calls
- `POST` endpoints are idempotent when `X-Request-ID` is present
- error responses use JSON envelope with stable `error.code` and `error.requestId`

Helper deployment artifacts:

- `deploy/systemd/tun-runtime-helper.service`
- `deploy/systemd/runtime-helper.env.example`
- `deploy/systemd/tun-runtime-helper-autopilot.service`
- `deploy/systemd/runtime-helper-autopilot.env.example`

Helper smoke:

```bash
./scripts/runtime_helper_smoke.sh
```

Bridge autopilot daemon smoke:

```bash
./scripts/runtime_helper_bridge_autopilot_smoke.sh
```

Bridge autopilot daemon canary (against existing helper endpoint):

```bash
./scripts/runtime_helper_bridge_autopilot_canary.sh \
  --unix-socket /run/tun/runtime-helper.sock \
  --token-file /etc/tun/runtime-helper.token \
  --payload-file /etc/tun/bootstrap.json
```

Unified runtime-helper release gate:

```bash
./scripts/release_gate_runtime_helper.sh \
  --skip-support-bundle-gate \
  --skip-autopilot-canary

# Staging profile with prefilled canary paths/socket.
./scripts/release_gate_runtime_helper.sh \
  --profile staging \
  --skip-support-bundle-gate

# Full staging profile (runs support-bundle gate + autopilot canary).
./scripts/release_gate_runtime_helper.sh \
  --profile staging-full \
  --bundle /var/tmp/support-bundle.json \
  --active-key k2=/etc/tun/support-signing-k2.key

# Strict full staging profile (forbids skipping critical gates) + JSON report.
./scripts/release_gate_runtime_helper.sh \
  --profile staging-full-strict \
  --bundle /var/tmp/support-bundle.json \
  --active-key k2=/etc/tun/support-signing-k2.key \
  --report-file /var/tmp/runtime-helper-gate-report.json

# CI presets:
./scripts/release_gate_runtime_helper.sh --profile ci-fast
./scripts/release_gate_runtime_helper.sh --profile ci-full

# Shortcuts:
make gate-ci-fast
make gate-ci-full
make gate-bundle-local
just gate-ci-fast
just gate-ci-full
just gate-bundle-local
```

Bootstrap gate artifacts bundle (report + logs + optional support bundle copy):

```bash
./scripts/bootstrap_runtime_helper_gate_bundle.sh \
  --profile local \
  --out-dir ./artifacts/runtime-helper-gate \
  --gate-arg "--skip-support-bundle-gate" \
  --gate-arg "--skip-autopilot-canary"
```

Smoke now validates bridge-safe lease orchestration for `JsTun` style flows:

- `status`
- `bridge.startup` (includes `lease.ensure`, `validate`, `apply`, `start`, optional `wait`)
- `bridge.reconcile` (returns control-loop plan)
- `bridge.autopilot` / `bridge.autopilot.once` (executes reconcile-driven startup/restart policy)
- `bridge.autopilot.daemon` (periodic autopilot loop for background controller)
- `stats.read`
- `diagnostics.export`
- `bridge.shutdown` (includes `lease.ensure`, `tunnel.stop`, `lease.release`)

Ingestion verification helper:

```bash
go run ./cmd/support-bundle-verify \
  -in ./support-bundle.json \
  -require-signature true \
  -active-key k2=/etc/tun/support-signing-k2.key \
  -previous-key k1=/etc/tun/support-signing-k1.key \
  -retired-key-id k0
```

CI/ops gate wrapper:

```bash
scripts/support_bundle_ingest_gate.sh \
  --bundle ./support-bundle.json \
  --require-signature true \
  --active-key k2=/etc/tun/support-signing-k2.key \
  --previous-key k1=/etc/tun/support-signing-k1.key \
  --retired-key-id k0
```

Ubuntu 22 ops/security/SRE host baseline gate:

```bash
sudo ./scripts/ops_sre_ubuntu22_baseline_gate.sh \
  --support-key-file /etc/tun/support-signing-k2.key
```

## Local development

Requirements:

- Go 1.22+ (1.25 installed locally)

Commands:

```bash
go test ./...
```

## Quick local test (dev keys)

This generates a dev X25519 static keypair and a self-signed TLS cert in `.dev/`:

```bash
./scripts/gen_dev_keys.sh
```

Run the server:

```bash
go run ./cmd/server \
  -addr :8443 \
  -cert ./.dev/tls_cert.pem \
  -key ./.dev/tls_key.pem \
  -server-id 00112233445566778899aabbccddeeff \
  -server-static-priv "$(cat ./.dev/server_static_priv.b64)"
```

Run the client:

```bash
go run ./cmd/client \
  -addr 127.0.0.1:8443 \
  -server-name localhost \
  -insecure true \
  -client-id 0102030405060708090a0b0c0d0e0f10 \
  -server-static-pub "$(cat ./.dev/server_static_pub.b64)"
```

## Notes

Current product and app planning documents live under `docs/`.

Protocol specs and older planning artifacts for the `tun-rnd` track live under `.docs/spec/` and `.docs/`.
