# JsTun Integration

Date: 2026-04-10

## Why this matters

Соседний проект [JsTun](/Users/just/projects/JsTun) уже является фактическим consumer'ом будущей monitoring/control platform.

Это меняет проектную рамку.

Мониторинг нельзя проектировать только как:

- внутренний SRE dashboard
- набор helper endpoints
- локальную ops-телеметрию

Он должен сразу поддерживать две разные проекции:

- operator-grade runtime admin
- client-grade app runtime status

## What already exists in JsTun

### Runtime-first admin is already real

Из документов и кода видно, что `JsTun` уже построил runtime-first admin model:

- dashboard centers on gateways, uplinks, waves, runtime peers, events
- отдельные runtime pages уже есть для `Gateways` и `Uplinks`
- local/dev `control-api` уже синтезирует `edges`, `gateways`, `uplinks`, enriched `peers`

Relevant evidence:

- [2026-04-06_runtime_admin_dashboard_rework.md](/Users/just/projects/JsTun/docs/2026-04-06_runtime_admin_dashboard_rework.md)
- [2026-04-06_runtime_admin_uplinks_surface.md](/Users/just/projects/JsTun/docs/2026-04-06_runtime_admin_uplinks_surface.md)
- [2026-04-07_local_control_runtime_contract.md](/Users/just/projects/JsTun/docs/2026-04-07_local_control_runtime_contract.md)

### App-facing runtime API also already exists

`JsTun` already defines app endpoints for:

- device registration
- embedded bootstrap
- runtime summary
- security events

Relevant evidence:

- [client-api-contract-2026-04-08.md](/Users/just/projects/JsTun/docs/app/client-api-contract-2026-04-08.md)

## Design consequence

Нужно проектировать monitoring backend как single source of runtime truth with multiple projections.

### Projection A: Operator/Admin

Needs:

- link-level detail
- node/gateway/uplink fleet rollups
- incidents
- commands
- diagnostics

### Projection B: App/Home screen

Needs:

- simplified runtime summary
- current protection health
- current gateway/exit location
- last security/runtime issues
- device-profile scoped perspective

## Recommended ownership split

### TUN project owns

- helper/agent contract
- telemetry semantics
- command semantics
- canonical link/event model

### JsTun project owns

- operator UI in portal-http
- distributed control API aggregation
- app-facing runtime summary API
- account/profile/device mapping

This is the correct split because `JsTun` is already the product/admin shell.

## Integration architecture

### Local runtime

`runtime-helper` on nodes publishes local link telemetry.

### Central monitoring

New monitoring services ingest helper data and persist normalized runtime state.

### JsTun admin

`portal-http` should read normalized monitoring data from central monitoring API, not from raw helpers.

### JsTun apps

App APIs should read curated profile/device-scoped runtime summaries derived from the same monitoring store.

## Exact integration points in JsTun

### Backend modules

Likely consumers:

- [internal/controlclient/client.go](/Users/just/projects/JsTun/internal/controlclient/client.go)
- [internal/controlserver/router.go](/Users/just/projects/JsTun/internal/controlserver/router.go)
- [internal/httpserver/app_api.go](/Users/just/projects/JsTun/internal/httpserver/app_api.go)
- [internal/httpserver/account_api.go](/Users/just/projects/JsTun/internal/httpserver/account_api.go)
- [internal/httpserver/view_models.go](/Users/just/projects/JsTun/internal/httpserver/view_models.go)

### Templates/pages

Likely admin surfaces:

- [admin.html](/Users/just/projects/JsTun/internal/httpserver/templates/dashboard/admin.html)
- [admin_live.html](/Users/just/projects/JsTun/internal/httpserver/templates/dashboard/admin_live.html)
- [admin_edges.html](/Users/just/projects/JsTun/internal/httpserver/templates/dashboard/admin_edges.html)
- [admin_uplinks.html](/Users/just/projects/JsTun/internal/httpserver/templates/dashboard/admin_uplinks.html)
- future monitor pages can live рядом with these

### App clients

Consumers:

- [clients/mobile/lib/main.dart](/Users/just/projects/JsTun/clients/mobile/lib/main.dart)
- [clients/desktop/lib/main.dart](/Users/just/projects/JsTun/clients/desktop/lib/main.dart)

## Recommended API exposure toward JsTun

### For admin/control-plane

Expose full monitoring API:

- `/v1/monitor/summary`
- `/v1/monitor/links`
- `/v1/monitor/nodes`
- `/v1/monitor/gateways`
- `/v1/monitor/incidents`
- `/v1/monitor/commands`

### For app/backend

Expose curated runtime/app endpoints:

- `/v1/app/runtime/summary`
- `/v1/app/runtime/profiles/{profile_id}`
- `/v1/app/runtime/devices/{device_id}`
- `/v1/app/security/events`

These app endpoints should not leak operator-only topology or remediation internals.

## Data mapping from monitoring to JsTun domain

### Monitoring domain

- node
- link
- gateway
- incident
- command

### JsTun product domain

- account
- device
- connection profile
- gateway
- runtime summary
- security event

### Mapping rules

1. one account can have many devices
2. one account can have many profiles
3. one device/profile may map to one or more runtime links over time
4. app runtime summary must be filtered by account ownership
5. admin runtime view must not be filtered by account ownership

## New requirement introduced by JsTun apps

The monitoring store now needs profile/device correlation fields.

At minimum add mapping capability for:

- `account_id`
- `device_id`
- `connection_profile_id`

These may live in:

- central monitoring store directly
- or in a join/materialization layer fed from JsTun product DB

## UI implication

### Admin

Admin pages can stay runtime-dense and operational.

### Mobile/Desktop apps

Apps need a completely different representation:

- one protection card
- one tunnel status line
- one current location line
- one issue banner if degraded

The same raw monitoring schema should not be sent directly to apps.

## Final integration stance

The correct strategic model is:

1. `TUN` defines runtime telemetry and helper contract
2. central monitoring normalizes and stores runtime truth
3. `JsTun` consumes that truth in two projections:
   - operator/admin
   - app/client

If this split is respected, both projects can evolve without coupling the app/UI layer to raw tunnel process details.

