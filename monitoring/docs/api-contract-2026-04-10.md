# Monitoring API Contract

Date: 2026-04-10

## Purpose

Определить API уже не в общем виде, а поверх конкретных SQL read-models из `monitoring/db/002_views.sql`.

## Operator API

Base path:

- `/v1/monitor`

## 1. Fleet summary

### `GET /v1/monitor/summary`

Backed by:

- `monitor_v_fleet_summary`

Response:

```json
{
  "ok": true,
  "summary": {
    "links_total": 42,
    "healthy_links": 36,
    "degraded_links": 3,
    "failed_links": 2,
    "stale_links": 1,
    "established_links": 35,
    "links_with_incidents": 4,
    "nodes_total": 8,
    "active_nodes": 8,
    "stale_nodes": 1,
    "open_incidents": 3,
    "acknowledged_incidents": 1,
    "critical_incidents": 1,
    "in_flight_commands": 2,
    "failed_commands_24h": 5,
    "generated_at": "2026-04-10T14:00:00Z"
  }
}
```

## 2. Links list

### `GET /v1/monitor/links`

Backed by:

- `monitor_v_link_current`

Query params:

- `health`
- `state`
- `node_id`
- `gateway_id`
- `role`
- `account_id`
- `device_id`
- `connection_profile_id`
- `stale`
- `q`
- `page`
- `per_page`

Response:

```json
{
  "ok": true,
  "items": [
    {
      "link_id": "client:dev-01:tun-main",
      "node_id": "fra-1",
      "node_name": "FRA 1",
      "node_region": "fra",
      "gateway_id": "msk",
      "role": "client",
      "transport_type": "tlsstream",
      "transport_addr": "10.0.0.10:18444",
      "tun_name": "tun-main",
      "desired_state": "up",
      "observed_state": "established",
      "health_status": "healthy",
      "session_id": "2e8f...",
      "error_class": "none",
      "last_error": "",
      "last_handshake_at": "2026-04-10T13:58:22Z",
      "last_rx_at": "2026-04-10T13:59:58Z",
      "last_tx_at": "2026-04-10T13:59:58Z",
      "rx_bytes": 102030,
      "tx_bytes": 99881,
      "gateway_id_selected": "msk",
      "monitor_source": "helper",
      "is_stale": false,
      "account_id": "acc_123",
      "device_id": "dev_123",
      "connection_profile_id": "cp_123",
      "open_incident_id": null
    }
  ],
  "page": 1,
  "per_page": 50,
  "total": 1
}
```

`monitor_source` semantics:

- `helper` for direct runtime-helper ingestion (poll/SSE)
- `control_peer` for control-plane peer projection fallback

## 3. Link detail

### `GET /v1/monitor/links/{link_id}`

Backed by:

- `monitor_v_link_current`
- plus recent rows from:
  - `monitor_link_events`
  - `monitor_probes`
  - `monitor_commands`

Response:

```json
{
  "ok": true,
  "link": {
    "link_id": "client:dev-01:tun-main",
    "current": {},
    "recent_events": [],
    "recent_probes": [],
    "recent_commands": [],
    "subject": {
      "account_id": "acc_123",
      "device_id": "dev_123",
      "connection_profile_id": "cp_123"
    }
  }
}
```

## 4. Gateways list

### `GET /v1/monitor/gateways`

Backed by:

- `monitor_v_gateway_summary`

## 5. Nodes list

### `GET /v1/monitor/nodes`

Backed by:

- `monitor_v_node_summary`

## 6. Incidents

### `GET /v1/monitor/incidents`

Backed by:

- `monitor_v_incident_current`

Response:

```json
{
  "ok": true,
  "items": [
    {
      "incident_id": "d7f3...",
      "kind": "link_failed",
      "severity": "critical",
      "status": "open",
      "title": "Link failed: client:dev-01:tun-main",
      "summary": "tls handshake timeout",
      "links_total": 1,
      "link_ids": ["client:dev-01:tun-main"],
      "opened_at": "2026-04-10T14:08:00Z"
    }
  ]
}
```

### `GET /v1/monitor/incidents/{incident_id}`

Backed by:

- `monitor_v_incident_current`
- `monitor_v_link_current`

## 6.1 Alerts feed

### `GET /v1/monitor/alerts`

Operational alias over incidents for alerting UI/automation.

Supports filters:

- `status` (`open` default, or `all`)
- `severity`
- `kind`
- `q`
- `limit` (default `100`, max `500`)

Backed by:

- `monitor_v_incident_current`

## 7. Commands

### `GET /v1/monitor/commands`

Reads:

- `monitor_commands`

Supports filters:

- `status`
- `command_type`
- `request_source`
- `node_id`
- `link_id`
- `q`
- `page`
- `per_page`

### `GET /v1/monitor/commands/{command_id}`

Reads:

- `monitor_commands`

### `GET /v1/monitor/commands/audit`

Reads:

- `monitor_v_command_audit`

Purpose:

- operational audit and dispatcher visibility
- includes retry/backoff fields (`dispatch_attempt`, `last_error`, `next_retry_at`, `backoff_active`)

### `POST /v1/monitor/commands`

Writes:

- `monitor_commands`

Request:

```json
{
  "target_type": "link",
  "target_id": "client:dev-01:tun-main",
  "command_type": "reconnect",
  "reason": "manual triage",
  "requested_by": "ops@example.com",
  "request_source": "operator_ui",
  "idempotency_key": "reconnect-client-dev-01-001",
  "args": {}
}
```

## 8. Daily report

### `GET /v1/monitor/reports/daily`

Query params:

- `day` in `YYYY-MM-DD` (UTC day; default current UTC day)

Response shape:

- `fleet_now` snapshot from `monitor_v_fleet_summary`
- `incidents` opened/resolved counters for the selected day
- `commands` accepted/succeeded/failed counters for the selected day
- `events` timeline counters (`link.discovered`, `link.session_changed`, etc.)
- `top_nodes` and `top_links` by incident count

Response:

```json
{
  "ok": true,
  "command": {
    "command_id": "f0b2...",
    "status": "accepted",
    "command_type": "reconnect",
    "request_source": "operator_ui"
  }
}
```

## App-facing API

Base path:

- `/v1/app/runtime`

These endpoints should live in `JsTun`, but use monitoring DB/read-models as source.

For local development they are exposed from `monitoring-api` directly.

## 1. Runtime summary

### `GET /v1/app/runtime/summary`

Backed by:

- `monitor_v_app_runtime_summary`

Filtered by current authenticated account.

Response:

```json
{
  "ok": true,
  "profiles": [
    {
      "connection_profile_id": "cp_123",
      "device_id": "dev_123",
      "links_total": 1,
      "healthy_links": 1,
      "degraded_links": 0,
      "failed_links": 0,
      "stale_links": 0,
      "last_handshake_at": "2026-04-10T13:58:22Z",
      "last_rx_at": "2026-04-10T13:59:58Z",
      "last_tx_at": "2026-04-10T13:59:58Z",
      "total_rx_bytes": 102030,
      "total_tx_bytes": 99881,
      "current_gateway_id": "msk",
      "protection_healthy": true,
      "protection_established": true
    }
  ]
}
```

### `GET /v1/app/runtime/profiles/{connection_profile_id}`

Backed by:

- `monitor_v_app_runtime_summary`
- curated rows from `monitor_v_link_current`

Current dev-mode filtering:

- `account_id`
- `device_id`

Response:

```json
{
  "ok": true,
  "runtime": {
    "profile": {
      "account_id": "acc_123",
      "device_id": "dev_123",
      "connection_profile_id": "cp_123",
      "links_total": 1,
      "healthy_links": 1,
      "protection_healthy": true,
      "protection_established": true
    },
    "links": [
      {
        "link_id": "client:dev-01:tun-main",
        "tun_name": "tun-main",
        "observed_state": "established",
        "health_status": "healthy",
        "current_gateway_id": "msk",
        "protection_status": "protected",
        "last_handshake_at": "2026-04-10T13:58:22Z",
        "last_rx_at": "2026-04-10T13:59:58Z",
        "last_tx_at": "2026-04-10T13:59:58Z",
        "rx_bytes": 102030,
        "tx_bytes": 99881
      }
    ]
  }
}
```

## Contract rule

Apps should never receive:

- raw helper payloads
- command metadata
- full topology
- operator-only incident internals

They should receive curated runtime state only.
