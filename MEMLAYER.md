# MemLayer Guide for TUN

This repository is expected to use MemLayer as its shared working memory.

## Endpoints

- Agent default: `http://127.0.0.1:18100`
- Escalated fallback: `https://memlayer.loc/api`
- Browser/UI-friendly: `https://memlayer.loc/api`

## Local Project Identity

- Project name: `TUN`
- Project root: `/Users/just/projects/TUN`
- Existing entry mode: `update`

## Required Agent Workflow

1. Read `memlayer.config.json`.
2. Check auth status with `GET /auth/me` if an API key is configured.
3. Before coding or debugging, fetch relevant memory for the current task.
4. Reuse prior decisions, constraints, risks, and artifacts whenever possible.
5. After meaningful progress, write back new memory entries and important links.
6. If repository structure or architecture changed, reimport the project into MemLayer.

## Suggested Memory Types

- `decision` for architecture and implementation choices
- `constraint` for hard limits, deployment boundaries, compliance, hosting rules
- `risk` for known failure modes, technical debt, fragile areas
- `artifact` for files, commands, migrations, deploy scripts, dashboards
- `task` for active work items or completed outcomes
- `note` for supporting context
- `event` for imports, incidents, releases, production changes

## Suggested Read Pattern

Before starting a task, search or request relevant memory around:

- the feature or bug name
- affected modules
- deployment/runtime constraints
- prior incidents and mitigations

## Suggested Write Pattern

Write memory when you:

- change architecture
- introduce a new script, service, migration, or integration
- discover a hidden dependency or limitation
- fix a production-significant bug
- complete a non-trivial task

Recommended metadata fields:

- `tags`
- `files`
- `commands`
- `service`
- `tenant_id`
- `source_path`
- `related_issue`

## Useful Calls

Health:

```bash
curl -sS "http://127.0.0.1:18100/health"
```

Auth status:

```bash
curl -sS "http://127.0.0.1:18100/auth/me" \
  -H "Authorization: Bearer $MEMORYBANK_API_KEY"
```

Search memory:

```bash
curl -sS "http://127.0.0.1:18100/memory/search?query=architecture&mode=hybrid&limit=8" \
  -H "Authorization: Bearer $MEMORYBANK_API_KEY"
```

Get relevant memory:

```bash
curl -sS -X POST "http://127.0.0.1:18100/memory/relevant" \
  -H "Authorization: Bearer $MEMORYBANK_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "query": "Current task context for TUN",
    "agent_id": "local-agent",
    "types": ["decision", "constraint", "risk", "artifact", "task", "note"],
    "limit": 8,
    "search_mode": "hybrid"
  }'
```

Reimport this project:

```bash
cd /Users/just/apps/memory.bank
PYTHONPATH=$PWD .venv313/bin/python scripts/import_project_cli.py \
  --project-root "/Users/just/projects/TUN" \
  --project-name "TUN" \
  --existing-entry-mode update \
  --memorybank-url "http://127.0.0.1:18100"
```

## Working Rule

Do not treat MemLayer as optional notes. Use it as the default shared memory layer for non-trivial work in this repository.
