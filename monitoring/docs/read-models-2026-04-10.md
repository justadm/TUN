# Monitoring Read Models

Date: 2026-04-10

## Purpose

Этот документ фиксирует SQL read-models, на которые должны опираться API и UI.

Не нужно каждый endpoint собирать с нуля из raw tables.
Нужны стабильные projections.

## Implemented views

Defined in:

- [002_views.sql](/Users/just/projects/TUN/monitoring/db/002_views.sql)

## Views

### `monitor_v_link_current`

Canonical current link row for operator APIs.

Combines:

- link inventory
- node data
- current snapshot
- product correlation
- currently open incident, if any

Use for:

- links table
- link detail header
- gateway summary input
- node summary input

### `monitor_v_fleet_summary`

Single-row current fleet counters.

Use for:

- admin dashboard cards
- top summary SSE

### `monitor_v_gateway_summary`

Current gateway rollup by selected/current gateway.

Use for:

- gateways list
- dashboard gateway cards

### `monitor_v_node_summary`

Current node rollup.

Use for:

- nodes list
- stale node detection UI

### `monitor_v_app_runtime_summary`

Curated profile/device/account view for `JsTun` app-facing runtime APIs.

Use for:

- `/v1/app/runtime/summary`
- profile/device runtime home cards

### `monitor_v_incident_current`

Current incident projection with linked link IDs aggregated into one row.

Use for:

- `/v1/monitor/incidents`
- `/v1/monitor/incidents/{incident_id}`
- admin incident tables
- operator triage detail pages

## Design stance

### Operator and app read models must diverge

This is deliberate.

Operator read models optimize for:

- topology
- incidents
- actions
- failure analysis

App read models optimize for:

- current protection state
- current gateway/exit
- last activity
- simple degraded/failed messaging

## Next step

Build API response contracts directly on top of these views.
