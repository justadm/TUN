# Monitoring Docs

Design package for the universal tunnel link monitoring and control system.

## Contents

- `architecture-2026-04-10.md`: target system architecture and service boundaries
- `backend-2026-04-10.md`: backend services, dispatcher, evaluator, and module boundaries
- `api-2026-04-10.md`: external and internal API contracts
- `jstun-integration-2026-04-10.md`: how the monitoring platform integrates with the neighboring `JsTun` admin, LK, and app stack
- `storage-2026-04-10.md`: persistence model, schemas, retention, and query patterns
- `operator-ui-2026-04-10.md`: operator/admin frontend and interaction design

## Scope

This package covers:

- backend services
- ingestion model
- APIs
- storage
- operator-facing frontend
- integration with current `portal-http`, `control-api`, and `runtime-helper`

It deliberately does not define further tunnel/runtime code changes beyond the handoff file.
