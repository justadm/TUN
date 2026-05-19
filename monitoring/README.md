# Monitoring

Dedicated workspace for the tunnel monitoring and control subsystem.

## Purpose

This directory is the implementation-oriented home for:

- monitoring backend design
- database schema
- ingestion model
- command model
- integration notes for `JsTun`

## Layout

- `db/`: SQL schema drafts and migrations for monitoring storage
- `config/`: local and staged node source definitions
- `docs/`: implementation-facing notes for monitoring subsystem structure
- `services/`: monitoring-api and monitoring-ingestor implementation skeleton
- `scripts/`: local smoke helpers for end-to-end checks

## Current status

The first deliverable here is the monitoring database model, designed to support:

- local helper/runtime telemetry ingestion
- central current-state views
- append-only event history
- operator commands
- incident correlation
- mapping to `JsTun` account/device/profile entities

Current SQL artifacts:

- `db/001_init.sql`
- `db/002_views.sql`

Current implementation-facing docs:

- `docs/db-model-2026-04-10.md`
- `docs/read-models-2026-04-10.md`
- `docs/api-contract-2026-04-10.md`
- `docs/dev-stack-2026-04-10.md`

Current implementation state:

- local Postgres schema and read-models
- `monitoring-api` with SQL-backed summary, links, nodes, gateways, incidents
- `monitoring-ingestor` with helper polling into nodes, links, snapshots
- transition-derived `monitor_link_events`
- basic incident lifecycle for `link_failed`, `link_stale`, `link_degraded`, `node_unreachable`
- profile-aware ingestion (`/v1/helper/status` + `/v1/helper/profile.current`) with link subject indexing by `profileID+revision`
- policy alerts: `high_risk_violation`, `profile_drift`, `startup_contract_failure`, `link_without_profile`, `gateway_flap_risk`

## Node Discovery

`monitoring-ingestor` supports two node source modes:

- static file (`config/nodes.json`)
- auto-discovery from control-plane API (`MONITORING_DISCOVERY_EDGES_URL`, `MONITORING_DISCOVERY_UPLINKS_URL`)

When discovery is enabled, discovered nodes are merged with `nodes.json` by `node_id`, and discovered
`helper_base_url`/host values take precedence.

Discovery can use:

- direct token (`MONITORING_DISCOVERY_API_TOKEN`)
- inherited token (`WG_CONTROL_API_TOKEN`)
- env-file token source (`MONITORING_DISCOVERY_API_TOKEN_FILE`, `WG_CONTROL_API_TOKEN=...`)

If control-plane payload does not contain host/node identity fields, templates can be used:

- `MONITORING_DISCOVERY_NODE_ID_TEMPLATE` (default: `{edge_id}-1`)
- `MONITORING_DISCOVERY_HOST_TEMPLATE` (example: `{edge_id}.local`)

## Control-Plane Peer Fallback

If helper endpoints are unreachable but control-plane is available, ingestor can project peer runtime into
monitoring links via:

- `MONITORING_CONTROL_PEERS_INGEST_ENABLED=true`
- `MONITORING_CONTROL_PEERS_URL=http://172.17.0.1:18110/v1/peers`

This keeps monitoring tables populated with real peer/link states from control-plane while helper read-path is
being rolled out per node.

## Realtime streams

For helper-enabled nodes, ingestor keeps realtime subscriptions:

- primary: `GET /v1/helper/links/health.stream`
- fallback/context: `GET /v1/helper/bridge.status.stream`

Realtime updates use the same link identity key (`node_id + ":" + linkID`) and keep missing links as `stale`
instead of hard delete.

## Profile-aware policy correlation

`monitoring-ingestor` now enriches each link with helper profile context from:

- `GET /v1/helper/links`
- `GET /v1/helper/status`
- `GET /v1/helper/profile.current`

Additional persistence:

- `monitor_link_subjects.security_profile`
- `monitor_link_subjects.profile_revision`
- `monitor_link_subjects.profile_index_key` (`profileID+revision`)
- `monitor_profile_inventory` (`profile_id`, `revision`, `security_profile`, `region`, `ruleset_ref`, DNS mode/template)
