# Monitoring Services

Implementation skeleton for the monitoring subsystem.

## Services

- `api/`: central monitoring read/write API for admin and backend consumers
- `ingestor/`: helper polling/SSE ingestion worker
- `common/`: shared config and helper utilities

## Current scope

This is a minimal scaffold, not a complete production implementation.

It currently provides:

- container entrypoints
- environment/config loading
- health endpoints
- SQL-backed operator read APIs
- command API (`/v1/monitor/commands`)
- command audit API (`/v1/monitor/commands/audit`)
- dev-mode app-facing runtime APIs
- helper `/v1/helper/links` polling
- snapshot ingestion into Postgres
- transition-derived `monitor_link_events`
- passive `monitor_probes`
- basic incident evaluator for link and node reachability failures
- env-gated command dispatcher to helper link actions
- retry/backoff for command dispatch failures

Next implementation steps:

- migration management
- helper SSE ingestion
- probes ingestion
- command dispatcher
- incident routing and escalation policy
