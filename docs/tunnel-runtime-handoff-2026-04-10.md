# Tunnel Runtime Handoff

Date: 2026-04-10

## Purpose

Этот файл фиксирует передачу всех дальнейших правок кода самого tunnel/runtime другому боту-разработчику.

Я дальше не меняю `tun-rnd` runtime/engine/helper код, а только:

- анализирую
- проектирую monitoring/control system
- фиксирую требования и замечания сюда

Исполнитель по tunnel/runtime должен:

- прочитать этот файл целиком
- сделать нужные изменения в коде
- прогнать тесты
- дописать сюда же, что именно было сделано и чем подтверждено

## Scope owned by the tunnel/runtime bot

Следующие зоны теперь принадлежат tunnel/runtime боту:

- `internal/core`
- `internal/engine`
- `internal/runtime`
- `internal/tun`
- `cmd/runtime-client`
- `cmd/runtime-server`
- `cmd/runtime-server-systemd`
- `cmd/runtime-helper`
- `cmd/runtime-helperctl`
- `cmd/runtime-preflight`
- `cmd/support-bundle-verify`

## Current analyst assessment

Базовая Phase 1 идея верная:

- link должен стать первичным operational object
- helper должен отдавать `links[]`, а не только process-level `status`
- runtime snapshot должен нести `link_id`, `session_id` и базовые traffic markers

Но дальнейшую доводку и кодовую чистку должен делать отдельный tunnel/runtime исполнитель.

## Required outcomes from the tunnel/runtime bot

### 1. Normalize link telemetry contract

Нужно довести и закрепить минимальный контракт:

- `link_id`
- `session_id`
- `last_handshake_at`
- `last_rx_at`
- `last_tx_at`
- `rx_bytes`
- `tx_bytes`

Контракт должен быть согласован в:

- runtime events
- helper status/link endpoints
- JSON event logs
- support bundle

### 2. Keep runtime logic coherent

Нельзя допустить, чтобы link telemetry:

- создавала race-prone state
- ломала существующую retry/failover semantics
- расходилась между client/server/helper
- подменяла transport facts guessed timestamps

Особенно важно проверить:

- где именно рождается `session_id`
- как различать новый handshake и reuse/refresh
- что происходит при reconnect burst
- как обновляются `rx/tx` counters при runtime stop/start

### 3. Validate helper API shape

Минимальный read contract acceptable for monitoring:

- `GET /v1/helper/links`
- `GET /v1/helper/links/<linkID>`

Нужно проверить:

- consistent JSON schema
- stable behavior before first event
- stable behavior after stop
- meaningful `health`
- backward compatibility with existing `status`, `health`, `stats.read`

### 4. Add or fix tests where needed

Нужны targeted tests на:

- helper `links` list/detail
- link status before runtime start
- link status after runtime established
- reconnect producing new `session_id`
- traffic counters increasing on real data path
- event/support bundle carrying link telemetry

### 5. Implement helper link action endpoints for monitoring command plane

Monitoring backend now has command queue and optional dispatcher. To make it operational, helper must expose:

- `POST /v1/helper/links/<linkID>/reconnect`
- `POST /v1/helper/links/<linkID>/drain`
- `POST /v1/helper/links/<linkID>/resume`
- `POST /v1/helper/links/<linkID>/gateway.select`

Minimal behavior contract:

1. Validate `linkID` and return `404` with stable error envelope when link is unknown.
2. For supported actions return `200` with JSON payload `{ "ok": true, ... }`.
3. For unsupported runtime state return `409` with stable machine-readable error code.
4. `gateway.select` accepts JSON body: `{ "gatewayID": "<id>" }` and validates non-empty `gatewayID`.
5. Actions must be idempotent and safe under retries from dispatcher.
6. Action results should be reflected in `/v1/helper/links` snapshot/event timeline quickly enough for next poll cycle.

## Open technical questions

Эти вопросы нужно решить в коде, не в теории:

1. `session_id`:
нужно ли генерировать в runtime lifecycle или в handshake layer

2. `link_id`:
должен ли он быть purely local (`client:<device>:<tun>`) или control-plane-derived stable identity

3. traffic timestamps:
можно ли считать их только по DATA transfer, или нужен отдельный keepalive/control signal

4. `health`:
должен ли helper сам вычислять coarse health или только отдавать raw telemetry для внешнего evaluator

5. server side:
как link identity будет выглядеть для listener/accepted session path на `runtime-server`

## Acceptance criteria

Работа tunnel/runtime бота считается завершенной, когда:

- telemetry contract стабилен и не ломает текущие тесты
- helper отдает usable link model
- helper implements link action endpoints above with tests
- добавлены/обновлены тесты
- приведен список файлов, где менялся код
- сюда же добавлен отчет о выполнении

## Response format for the tunnel/runtime bot

Нужно дописать в конец этого файла секцию:

```md
## Runtime Bot Update

Date:

### What changed

- ...

### Files changed

- ...

### Tests run

- ...

### Remaining risks

- ...
```

## Runtime Bot Update

Date: 2026-04-12

### What changed

- Added helper link action command-plane endpoints under `POST /v1/helper/links/<linkID>/...`:
  - `reconnect`
  - `drain`
  - `resume`
  - `gateway.select`
- Enforced action behavior contract:
  - unknown link returns `404` with stable envelope/code `link_not_found`
  - unsupported state returns `409` with machine-readable codes (`runtime_not_running`, `runtime_not_configured`, `gateway_pool_not_configured`, `gateway_not_found`)
  - invalid `gatewayID` payload returns `400` with code `gateway_id_required`
  - successful actions return `200` with `{ "ok": true, "action": "...", "link": {...} }`
- Wired gateway selection persistence to bootstrap policy (`profileBootstrap.gatewayPolicy.forceGatewayID`) and helper state file; selection is reflected in link snapshot even before the next runtime event.
- Extended helper schema endpoint catalog to include the new link action endpoints (versioned and legacy aliases).
- Extended `runtime-helperctl` with action routing and payload support:
  - `link.reconnect`
  - `link.drain`
  - `link.resume`
  - `link.gateway.select` (`-gateway-id` or payload file)
- Tightened `session_id` ownership to runtime lifecycle source (removed duplicate helper-side session id generation/override in `runRuntimeClient`).
- Added targeted tests for:
  - link action endpoints and conflict/error envelopes
  - helperctl route/payload coverage for new link actions
  - reconnect producing a new `session_id`
  - support bundle carrying required link telemetry contract fields

### Files changed

- `cmd/runtime-helper/main.go`
- `cmd/runtime-helper/main_test.go`
- `cmd/runtime-helperctl/main.go`
- `cmd/runtime-helperctl/main_test.go`
- `internal/runtime/client_test.go`
- `internal/runtime/diagnostics_test.go`

### Tests run

- `go test ./cmd/runtime-helper ./cmd/runtime-helperctl ./internal/runtime ./internal/engine`

### Remaining risks

- `drain/resume/reconnect` semantics are process-level for now (single-link helper model). For future multi-link runtime, action semantics will need per-link runtime ownership instead of single manager process transitions.
- `gateway.select` currently applies by persisted `forceGatewayID` + optional refresh when running. If runtime is in a noisy reconnect burst, next-event reflection timing still depends on runtime loop scheduling.

### Follow-up update (same day)

- Added `runtime-helperctl` high-level action `link.failover`:
  - performs `gateway.select` then `reconnect` in one workflow
  - uses deterministic sub-request ids (`<rid>-failover-select`, `<rid>-failover-reconnect`) for safe retries
- Added targeted helperctl tests for `link.failover` routing/validation/workflow call sequence.
- Added standalone smoke script:
  - `scripts/runtime_helper_link_failover_smoke.sh`
  - validates end-to-end failover command and verifies selected gateway via `link.read`.

### Follow-up update (2026-04-13, security hardening)

- Implemented full helper security control plane based on methodology-driven risk model:
  - `POST /v1/helper/security.evaluate`
  - `POST /v1/helper/security.reputation.upsert`
  - `POST /v1/helper/security.policy.upsert`
  - `GET /v1/helper/security.policy.get`
  - `POST /v1/helper/security.corporate-allow.upsert`
  - `GET /v1/helper/security.audit`
- Added anti-spoofing provenance verification for client security signals:
  - HMAC (`SECURITY_SIGNAL_HMAC_KEY`)
  - timestamp window checks
  - nonce replay protection.
- Added TTL-based reputation cache with source quality scoring and audit tracking.
- Added per-tenant policy profiles (`strict|balanced|permissive`) and enforce flags.
- Added hysteresis-based hard-block logic (`temporary_block`) to avoid single-event overreaction.
- Added corporate ASN/CIDR allow-rules with expiration governance (false-positive dampener).

### Follow-up update (2026-04-13, contract sync + ingest + rollout)

- Added unified security contract artifact for cross-repo sync:
  - `docs/contracts/security_signal_contract_v1.json`
  - schema metadata field `securityContractVersion=2026-04-13`.
- Added ingestion pipeline endpoints:
  - `POST /v1/helper/security.signal.ingest`
  - `GET /v1/helper/security.signal.ingest.recent`
- Added tenant rollout endpoint:
  - `POST /v1/helper/security.policy.rollout`
  - supports default profile + strict-tenant list for staged adoption.
- Added rollout helper script:
  - `scripts/runtime_helper_security_rollout.sh`

### Follow-up update (2026-04-13, tunnel M1 start)

- Started M1 (`full rekey v1`) execution:
  - Added protocol draft:
    - `docs/contracts/rekey_v1_protocol_draft_2026-04-13.md`
  - Added core binary payload scaffolding:
    - `internal/core/rekey_v1.go`
    - `internal/core/rekey_v1_test.go`
- Updated core note:
  - `internal/core/README.md`

### Follow-up update (2026-04-13, tunnel M2 base integration)

- Added runtime rekey states:
  - `rekey_pending`
  - `rekey_overlap`
  - `rekey_cutover`
- Extended runtime snapshot with rekey telemetry:
  - `last_rekey_at`
  - `rekey_epoch`
  - `rekeys_initiated`
  - `rekeys_completed`
  - `rekey_fallbacks`
- Integrated control-path observer in `RunClient`:
  - parses `RekeyInitV1` / `RekeyAckV1`
  - emits state transitions on accepted rekey flow.
- Added runtime unit test for control-driven rekey transitions:
  - `TestRunClientRekeyStateTransitionsFromControlMessages`.
- Updated helper health mapping to treat rekey states as degraded (not failed).
