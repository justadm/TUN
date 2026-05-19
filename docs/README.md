# docs

Current working documentation for JsTun.

## Policy

- `docs/` is the canonical location for active product, app, API, rollout, and planning documents.
- `docs/app/` contains client-application strategy, architecture, delivery plans, and working notes.
- `docs/monitoring/` contains the active design package for tunnel monitoring and control.
- `tun-link-monitoring-control-2026-04-10.md` captures the proposed universal monitoring/control architecture for active `tun-rnd` links.
- `tunnel-runtime-handoff-2026-04-10.md` is the handoff contract for the separate tunnel/runtime implementation bot.
- `.docs/` is retained for legacy/internal artifacts, protocol-spec history, dated research notes, and existing references used by scripts or code.
- New product and client documentation should be created in `docs/` unless it is explicitly a low-level protocol/spec artifact tied to the historical `tun-rnd` research track.

## Migration stance

- Do not mass-move historical `.docs/` content until links, scripts, and code references are inventoried and rewritten.
- When a historical `.docs/` document remains useful, supersede it from `docs/` with an explicit cross-reference instead of creating duplicate copies.
