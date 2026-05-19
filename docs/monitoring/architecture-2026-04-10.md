# Monitoring Architecture

Date: 2026-04-10

## Goal

Спроектировать универсальную систему мониторинга и контроля поднятых линков `tun-rnd`, которая:

- встраивается в текущий `control-plane`
- использует существующий `runtime-helper` как локальный agent
- не требует немедленного переписывания portal/control-plane
- поддерживает и operator workflows, и будущий app-facing status plane

## Constraints from the current repo

### 1. Existing control-plane is Python and server-rendered

Сейчас фактические плоскости управления:

- [wg_portal_http.py](/Users/just/projects/TUN/control-plane/portal-http/wg_portal_http.py)
- [wg_control_api_server.py](/Users/just/projects/TUN/control-plane/control-api/wg_control_api_server.py)

Это важно. Реалистичная v1 архитектура должна расширять эти сервисы, а не подменять их SPA-платформой без operational причин.

### 2. Existing local runtime agent already exists

`runtime-helper` уже выполняет роль локального агента:

- контроль процесса туннеля
- lease semantics
- SSE events
- diagnostics export

Поэтому новая система не должна создавать второй локальный daemon.

### 3. Existing data model already distinguishes intent and effective state

Исторические control-plane документы уже развели:

- edge
- uplink
- peer
- effective routing
- health state

Мониторинг должен дополнять этот слой observed link facts, а не смешивать их с routing intent.

## Proposed system

### Components

Нужны четыре компонента.

### A. Link Agent

Это существующий `runtime-helper`, расширенный только в своей предметной области.

Функции:

- локальный inventory линков
- current observed link status
- локальные events
- diagnostics export
- команды link control

### B. Monitoring Ingestor

Новый сервис внутри `control-plane`.

Функции:

- подписка на helper SSE streams
- polling fallback для helpers
- normalization events and snapshots
- deduplication and sequence checks
- запись в central store

### C. Monitoring API

Новый machine-facing сервис или отдельный модуль в `control-api`.

Функции:

- выдача link/node/gateway status
- выдача event history
- выдача incidents/alerts
- прием operator commands
- выдача live SSE for UI

### D. Operator UI

Новый набор разделов внутри `portal-http`.

Функции:

- fleet overview
- links table
- node detail
- gateway detail
- incident timeline
- command execution UI

## Deployment model

### On every node with runtime

- `runtime-helper`
- optional `runtime-helper-autopilot`

### In central control-plane

- `tun-monitor-ingestor`
- `tun-monitor-api`
- Postgres schema for monitoring

### Why separate ingestor and API

Это не вкусовщина, а operational reason:

- ingestion traffic write-heavy and bursty
- operator/API traffic read-heavy and interactive
- isolation упрощает backpressure control
- инцидент в UI не должен ломать ingest

## Canonical objects

### Link

Главный runtime object.

Содержит:

- local node
- remote node or gateway
- role
- transport endpoint
- tunnel interface
- session identity
- health
- current counters

### Node

Операционный хост, на котором живут helper/runtime.

### Gateway

Control-plane/placement объект, через который идет ingress/egress policy.

### Incident

Появляется, когда один или несколько links нарушают policy:

- failed
- flapping
- stale
- degraded over threshold

### Command

Явное operator or automation action:

- reconnect
- drain
- resume
- gateway.select
- diagnostics.export

## Trust boundaries

### Local agent boundary

`runtime-helper` считается источником локальной фактической телеметрии.

### Central monitor boundary

Central monitor не доверяет helper безусловно:

- проверяет auth
- нормализует payload
- проверяет freshness
- помечает stale agents

### Operator action boundary

Никакая UI action не идет напрямую в shell/SSH.

Operator action flow:

`portal-http` -> `monitor-api` -> command store -> dispatcher -> helper API

## Recommended service split

### V1

- `control-plane/monitoring-ingestor`
- `control-plane/monitoring-api`
- UI pages added to `portal-http`

### V2

Если нагрузка вырастет:

- isolate dispatcher
- isolate alert evaluator
- optional websocket/live fanout service

## Event flow

1. `runtime-helper` emits local events
2. ingestor subscribes via SSE or polls snapshots
3. ingestor writes:
   - raw events
   - normalized snapshot
   - derived health transitions
4. API serves:
   - current views
   - timeline views
   - live updates
5. UI consumes:
   - summary JSON
   - detail JSON
   - SSE stream

## Health evaluation split

### Local health

Helper may publish a coarse local state:

- running
- established
- reconnecting

### Central health

Canonical operator-facing `health_status` should be computed centrally because only central plane sees:

- cross-node correlation
- stale ingestion
- asymmetry between client and server sides
- fleet-level flapping

## Command execution model

### Command types

- `reconnect`
- `drain`
- `resume`
- `freeze_autoselect`
- `select_gateway`
- `export_diagnostics`

### Command lifecycle

- `accepted`
- `dispatched`
- `acknowledged`
- `succeeded`
- `failed`
- `timed_out`
- `canceled`

### Why command store first

Прямая synchronous execution из UI плоха потому что:

- неустойчивые host connections
- длинные operations
- нужен audit trail
- нужен replay-safe idempotency model

## Alerting model

### Alert classes

- `link_failed`
- `link_flapping`
- `link_stale`
- `gateway_degraded`
- `node_unreachable`
- `ingestion_gap`

### Policy

Alerts should be:

- threshold-based
- suppressible
- deduplicated
- correlated into incidents

## Initial implementation choice

Наиболее прагматичный путь:

- helpers remain local agents
- new monitoring backend lives in Python next to current control-plane
- Postgres is primary store
- UI is server-rendered in `portal-http` with SSE fragments

Не because Python is ideal in the abstract, а потому что это минимально конфликтует с текущим operational reality.

