# Universal TUN Link Monitoring And Control

Date: 2026-04-10

## Purpose

Спроектировать универсальную систему мониторинга и контроля поднятых линков на `tun-rnd`, опираясь на текущий код и документы, а не на абстрактную future-архитектуру.

## Executive summary

В проекте уже есть хороший стартовый фундамент:

- runtime state machine с классификацией ошибок и retry/failover логикой
- health/status endpoints у runtime
- helper API с lease, wait, SSE events, diagnostics export и bridge autopilot
- gateway selector с cooldown/sticky policy
- support bundle и event JSON log
- systemd units и SRE gate scripts для mesh/prod validation

Но этого пока недостаточно для универсального контроля линков.

Сейчас наблюдаемость в основном:

- процессная, а не линковая
- локальная, а не сквозная
- реактивная, а не policy-driven
- файловая и ad hoc, а не агрегированная в единую operational model

Ключевой вывод: не нужно строить отдельную "систему мониторинга поверх всего". Нужно превратить текущие `runtime` и `runtime-helper` в link agent, а сверху добавить единую control/observability plane для всех поднятых линков.

## What the project already has

### Runtime

В `internal/runtime` уже реализованы базовые примитивы, на которые надо опираться:

- `State` lifecycle: `idle`, `dialing`, `handshaking`, `established`, `reconnecting`, `stopped`
- `Snapshot` с попытками, reconnects, error class, gateway selection counters
- `ServiceStatusTracker` с `/live`, `/ready`, `/status`
- `TransportRetryPolicy` с burst handling для handshake failures
- `GatewayFailoverDialer` с ranking, sticky selection, cooldown

Это хорошая база для health engine, но пока там нет полноценной линковой модели:

- нет `link_id`
- нет session correlation между сторонами
- нет SLI per link
- нет active probe semantics
- нет distinction между `healthy` и `established but degraded`

### Helper

`cmd/runtime-helper` уже является локальной control plane:

- `lease.acquire|renew|takeover|release`
- `bridge.startup|shutdown|reconcile|autopilot`
- `status`, `health`, `stats.read`, `wait`
- SSE stream `/v1/helper/events`
- signed support bundle export

Это почти готовый local agent contract. Недостает трех вещей:

- link inventory
- richer telemetry schema
- explicit remediation commands per link

### Ops layer

Скрипты и артефакты показывают текущую реальную operational practice:

- mesh gates проверяют systemd status, TUN interfaces и end-to-end ping
- release validation уже включает soak и impairment scenarios
- systemd units уже задают стабильную точку запуска runtime и helper

Следовательно, новая система должна не заменить эти процедуры, а формализовать их в machine-readable monitoring/control loop.

## Main problem

Сейчас "поднятый линк" определяется косвенно:

- сервис запущен
- интерфейс существует
- ping проходит
- последний state был `established`

Это слишком слабая модель. Она плохо отвечает на важные operational questions:

- линк жив или просто процесс еще не умер
- трафик реально течет или stuck после handshake
- проблема локальная, транспортная, криптографическая или datapath
- надо ждать retry, сделать gateway switch или жестко перезапустить линк
- какой линк у какого peer/device/gateway сейчас является primary, backup, drain или orphaned

## Design principles

### 1. Link is the primary object

Главный объект мониторинга должен быть не process и не interface, а `link`.

`Link` это связка:

- runtime role: `client` или `server`
- local node
- remote node or gateway
- transport endpoint
- TUN interface
- session lifecycle
- control owner

### 2. Local truth first

Первичный источник правды должен быть локальный helper/agent на хосте, а не внешний polling system.

Причина простая:

- только локальный процесс знает точную причину сбоя
- только локальный процесс видит realtime transition sequence
- только локальный helper может безопасно выполнить remediation с lease semantics

### 3. Separate observation from remediation

Наблюдение должно быть непрерывным и дешевым.
Управление должно быть явным, ограниченным и аудируемым.

Нельзя, чтобы любой alarm немедленно делал restart.
Нужна политика с hysteresis, cooldown и ownership.

### 4. Use the existing helper contract

Новый слой надо строить как расширение `runtime-helper`, а не как параллельный daemon.

Это снижает сложность:

- не появляется второй локальный orchestrator
- reuse lease/idempotency/auth/unix-socket contract
- reuse SSE/events/support bundle

## Target architecture

### Layer 1. Link Agent on every node

Это расширенный `runtime-helper`.

Он отвечает за:

- локальный inventory поднятых линков
- текущий desired state и observed state
- сбор пассивных и активных метрик
- выполнение локальных control actions
- streaming events наружу

Каждый link agent должен уметь вернуть:

- список линков
- status каждого линка
- health каждого линка
- последние события
- counters
- причины деградации
- разрешенные команды управления

### Layer 2. Central Link Monitor

Отдельный control-plane сервис или модуль, который:

- агрегирует статусы со всех link agents
- хранит snapshots и events
- строит global topology links <-> nodes <-> gateways <-> peers
- считает SLI/SLO
- генерирует alerts
- выдает операторам единый control surface

### Layer 3. Command Dispatcher

Компонент, который отправляет на нужный helper команды:

- drain
- restart
- stop
- resume
- switch gateway
- freeze auto-select
- force diagnostics export

Этот слой не должен выполнять shell на хостах напрямую как основной способ управления.
SSH и shell должны остаться break-glass path, а не baseline.

## Unified link model

Минимальная сущность `LinkStatus`:

```json
{
  "linkID": "msk:client:fra",
  "role": "client",
  "nodeID": "fra",
  "peerNodeID": "msk_d",
  "gatewayID": "msk",
  "transport": {
    "type": "tlsstream",
    "addr": "10.0.0.10:18444",
    "serverName": "msk-gw"
  },
  "tun": {
    "name": "trcli-msk",
    "mtu": 1420,
    "addresses": ["10.251.2.2/30"]
  },
  "desiredState": "up",
  "observedState": "established",
  "health": "healthy",
  "sessionID": "01HV...",
  "leaseOwner": "runtime-helper-autopilot",
  "lastTransitionAt": "2026-04-10T12:00:00Z",
  "lastHandshakeAt": "2026-04-10T11:59:58Z",
  "lastTrafficAt": "2026-04-10T12:00:03Z",
  "counters": {
    "reconnects": 1,
    "handshakeFailures": 0,
    "gatewaySwitches": 0,
    "rxBytes": 123456,
    "txBytes": 120111
  },
  "degradationReasons": []
}
```

### Required states

Рекомендованная модель состояния:

- `planned`
- `starting`
- `dialing`
- `handshaking`
- `established`
- `degraded`
- `failing_over`
- `draining`
- `stopping`
- `stopped`
- `failed`
- `orphaned`

Важно: `established` и `healthy` не должны быть синонимами.

## Health model

Нужен отдельный health evaluator, который поверх runtime state вычисляет:

- `healthy`
- `degraded`
- `failed`
- `draining`
- `unknown`

### Signals for health

#### Passive signals

- runtime state transitions
- error class
- reconnect count and rate
- handshake failure burst
- last received byte age
- last sent byte age
- tunnel open failures
- engine failures
- gateway cooldown skips
- interface presence and MTU mismatch
- support bundle anomalies

#### Active signals

- local transport probe до endpoint
- control keepalive RTT
- in-tunnel echo/ping probe
- server-side acknowledgement of current session
- optional synthetic route probe through the link

### Suggested degradation rules

- `established` but no RX bytes for `N` seconds under expected traffic: `degraded`
- repeated reconnects over window: `degraded`
- handshake failures burst: `degraded` then `failed`
- TUN present but transport closed: `failed`
- transport up but in-tunnel probe fails: `degraded`
- gateway switched recently and current path unstable: `degraded`

## Control model

Нужны явные команды per link:

- `start`
- `stop`
- `restart`
- `drain`
- `resume`
- `reconnect`
- `forceGateway`
- `clearForcedGateway`
- `freezeAutoselect`
- `unfreezeAutoselect`
- `collectDiagnostics`

### Control safety rules

- все mutating actions только через active lease
- все actions должны быть idempotent
- каждая action должна писать audit event
- restart/failover automation должна иметь cooldown
- `drain` должен запрещать новый трафик, но не рубить линк мгновенно без policy

## Required API extensions

Расширять лучше существующий helper API.

### New read endpoints

- `GET /v1/helper/links`
- `GET /v1/helper/links/<linkID>`
- `GET /v1/helper/links/<linkID>/metrics`
- `GET /v1/helper/topology`

### New streaming endpoints

- `GET /v1/helper/links/events`
- `GET /v1/helper/links/health.stream`

### New control endpoints

- `POST /v1/helper/links/<linkID>/drain`
- `POST /v1/helper/links/<linkID>/resume`
- `POST /v1/helper/links/<linkID>/reconnect`
- `POST /v1/helper/links/<linkID>/gateway.select`

`/v1/helper/status` и `/stats.read` стоит сохранить, но считать legacy summary views.

## Data persistence

Центральный monitor должен хранить два типа данных:

### 1. Current snapshot

Для UI и operator workflows:

- current status
- health
- selected gateway
- lease owner
- last error
- last event

### 2. Event history

Для расследований:

- transitions
- control actions
- failover decisions
- cooldown decisions
- diagnostics exports
- support bundle checks

Минимальная схема:

- `link_inventory`
- `link_snapshot`
- `link_event`
- `link_command`
- `link_probe_result`

## Correlation problem

Сейчас у runtime почти нет сквозной идентичности сессии.
Это критическая дыра.

Нужно ввести:

- `link_id`
- `session_id`
- `attempt_id`
- `correlation_id`

Они должны попадать:

- в event log
- в SSE events
- в support bundle
- в helper status
- в central event store

Без этого нельзя надежно сопоставить:

- локальный reconnect на FRA
- серверное принятие сессии на MSK
- gateway switch
- пользовательскую деградацию в UI

## Recommended implementation plan

### Phase 1. Formalize local link state

Без изменений wire protocol:

- расширить `runtime.Snapshot` до линковой модели
- добавить `link_id`, `session_id`, `last_rx_at`, `last_tx_at`, `rx_bytes`, `tx_bytes`
- считать `health` отдельно от `state`
- добавить helper endpoints `links` и `links/<id>`

Это даст быстрый выигрыш и почти не ломает текущую архитектуру.

### Phase 2. Add active probing

- добавить lightweight control keepalive RTT
- добавить in-tunnel synthetic echo probe
- добавить probe result в snapshot/events
- различать `established-but-silent` и `established-and-passing-traffic`

### Phase 3. Build central monitor

- collector, который подписывается на helper SSE или периодически poll'ит helper API
- единое хранилище snapshot + events
- operator UI/CLI для всех линков
- alert rules по flap/failover/degraded/failure

### Phase 4. Controlled remediation

- команды `drain/resume/reconnect/gateway.select`
- policy engine с cooldown/hysteresis
- audit trail
- break-glass override для SRE

### Phase 5. Protocol-assisted observability

Когда базовая система уже работает:

- richer control messages
- explicit session heartbeat
- remote ack of session identity
- optional per-session stats exchange

Это полезно, но не должно быть первым шагом.

## What not to do

- не строить мониторинг только на `systemctl status` и shell ping
- не делать центральный сервис, который не знает локальной причины ошибок
- не смешивать alerting и auto-remediation без cooldown
- не вводить новую control plane параллельно `runtime-helper`
- не считать `process alive` достаточным признаком здоровья линка

## Concrete first backlog

1. Ввести `link_id` и `session_id` в `runtime.Event` и `runtime.Snapshot`.
2. Добавить в `engine` счетчики `rx_bytes`, `tx_bytes`, `last_rx_at`, `last_tx_at`.
3. Добавить `health` evaluator в `internal/runtime`.
4. Расширить `runtime-helper` endpoints до `GET /v1/helper/links` и `GET /v1/helper/links/<id>`.
5. Добавить команды `reconnect`, `drain`, `resume`, `gateway.select`.
6. Сохранить события и snapshots в центральный store.
7. Добавить operator UI/CLI с фильтрами `health`, `gateway`, `role`, `node`, `lease owner`, `flapping`.

## Final assessment

Текущий проект уже содержит 60-70% нужных строительных блоков, но они разрознены:

- runtime знает lifecycle
- helper знает orchestration
- scripts знают validation
- docs знают desired operational semantics

Нужный следующий шаг не в том, чтобы изобретать новый monitoring stack, а в том, чтобы:

1. нормализовать link как главный operational object
2. сделать `runtime-helper` стандартным link agent
3. вынести aggregated monitoring/control в отдельную control-plane service

Именно такая схема будет универсальной:

- для клиентских линков
- для server mesh линков
- для будущих `gateway pool` и `autoselect`
- для app-facing статусов и операторского recovery

