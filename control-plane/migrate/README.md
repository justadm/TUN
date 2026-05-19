# JsTun Control-Plane Migration Helpers

This directory contains repository-native helpers for exporting and normalizing legacy `control-plane` state before DB cutover.

Current helper:

- `legacy_export.py`
- `import_plan.py`
- `sql_emit.py`
- `parity_report.py`

Purpose:

- read legacy state from a directory such as `/var/lib/wg-portal`
- normalize peers, overrides, audit log, and billing records
- emit one JSON snapshot for importer development and parity testing
- transform that snapshot into a DB-oriented import plan
- emit Postgres SQL from that import plan
- produce one machine-readable parity report across all migration stages

Example:

```bash
python3 control-plane/migrate/legacy_export.py \
  --state-dir /var/lib/wg-portal \
  --output /tmp/jstun-legacy-snapshot.json

python3 control-plane/migrate/import_plan.py \
  --input /tmp/jstun-legacy-snapshot.json \
  --output /tmp/jstun-import-plan.json

python3 control-plane/migrate/sql_emit.py \
  --input /tmp/jstun-import-plan.json \
  --ddl /Users/just/projects/TUN/.docs/control-plane-ddl-draft-2026-04-03.sql \
  --output /tmp/jstun-import.sql

python3 control-plane/migrate/parity_report.py \
  --snapshot /tmp/jstun-legacy-snapshot.json \
  --import-plan /tmp/jstun-import-plan.json \
  --sql-meta /tmp/jstun-import.sql.meta.json \
  --output /tmp/jstun-parity-report.json
```

Notes:

- the script is read-only
- malformed rows are not discarded; they are reported into `quarantine`
- the output is intended as an intermediate migration artifact, not as the final storage format
- `import_plan.py` already separates seed inventory, source-of-truth rows, runtime placeholders, and quarantine
- by default `import_plan.py` excludes `blocked`, `expired`, and `removed` peers from working import and keeps them in `tables.skipped_peers`
- `sql_emit.py` only emits FK-safe `billing_records` rows and nullifies `events.peer_id` when the referenced peer was skipped from import
- `parity_report.py` gives one machine-readable checkpoint before disposable Postgres dry-run
