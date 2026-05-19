# Operator UI

Date: 2026-04-10

## Product stance

V1 operator UI should live inside the existing admin interface in `portal-http`.

Причины:

- current admin already manages peers, edges, uplinks, events
- server-rendered HTML is already the operational baseline
- operators need reliability and low change surface more than frontend novelty

## New admin sections

### 1. `/admin/monitor/`

Fleet overview dashboard.

Widgets:

- healthy/degraded/failed/stale link counts
- open incidents
- stale nodes
- top failing gateways
- recent command failures

### 2. `/admin/monitor/links/`

Main table for all tun links.

Columns:

- `Link`
- `Node`
- `Gateway`
- `Role`
- `State`
- `Health`
- `Session`
- `Last HS`
- `Last RX`
- `Last TX`
- `RX`
- `TX`
- `Error`
- `Actions`

Filters:

- `health`
- `state`
- `node`
- `gateway`
- `role`
- `stale only`
- `flapping only`
- text search

### 3. `/admin/monitor/links/<linkID>/`

Link detail page.

Blocks:

- overview
- current snapshot
- recent events
- probe history
- recent commands
- related incident
- diagnostics export action

### 4. `/admin/monitor/nodes/`

Node inventory and helper freshness.

Columns:

- `Node`
- `Role`
- `Region`
- `Links`
- `Healthy`
- `Degraded`
- `Failed`
- `Last Seen`
- `Status`

### 5. `/admin/monitor/gateways/`

Gateway rollup page.

Columns:

- `Gateway`
- `Links`
- `Healthy`
- `Degraded`
- `Failed`
- `Open Incidents`
- `Actions`

### 6. `/admin/monitor/incidents/`

Incident list and detail pages.

Columns:

- `Incident`
- `Severity`
- `Kind`
- `Status`
- `Affected Links`
- `Opened`
- `Owner`

## Interaction model

### Page rendering

Use server-rendered HTML from `portal-http`.

### Live updates

Use SSE from central monitoring API for:

- summary counters
- link status chips
- incident badges

### Commands

All mutating actions go through explicit forms/buttons:

- reconnect
- drain
- resume
- export diagnostics

No hidden auto-actions from UI.

## Visual language

UI should match current admin style, not switch to a separate application aesthetic.

### Health colors

- `healthy`: green
- `degraded`: yellow
- `failed`: red
- `stale`: gray

### Compact chips

Use compact chips for:

- health
- state
- gateway
- stale age
- flapping

### Dense operator-first layout

Do not optimize for marketing polish.
Optimize for:

- scanability
- triage speed
- low-click drilldown

## Key UX rules

### 1. Separate current fact from desired intent

На link detail и gateway pages всегда различать:

- current observed status
- desired routing/placement intent

### 2. Show freshness everywhere

Any status without freshness is misleading.

Every page should show:

- `last updated`
- `last event`
- `stale if older than ...`

### 3. Show command history near actions

Operator must see:

- who triggered last reconnect
- when it ran
- whether it succeeded

### 4. Prefer drilldown over overloaded rows

Main tables must stay dense but readable.
Deep details belong to detail pages and side panels.

## Frontend implementation approach

### V1

- HTML generated in `portal-http`
- fetch JSON from central monitor API
- use small inline JS for:
  - SSE subscription
  - table refresh
  - live counter updates

### V2

If pages become too interactive:

- progressively extract JS components
- but keep server-rendered fallback

## Why not build a React SPA first

Потому что это would be architecture theater at current stage.

The real problem here is:

- telemetry normalization
- event correlation
- command safety
- operator workflow

not frontend framework deficiency.

