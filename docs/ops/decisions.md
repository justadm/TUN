# Decisions (ADR-lite)

Краткий реестр. Подробности — [session-worklog-2026-05-19.md](./session-worklog-2026-05-19.md).

| ID | Дата | Решение | Статус |
|----|------|---------|--------|
| D-001 | 2026-05-19 | Первый приоритет — git sync всей платформы одним commit | accepted |
| D-002 | 2026-05-19 | Не коммитить `.tmp/`, `artifacts/`, `monitoring/.env` | accepted |
| D-003 | 2026-05-19 | `.docs/` пока остаётся в `.gitignore`; ops-канон в `docs/ops/` | accepted |
| D-004 | 2026-05-19 | CI: `go-test` + `runtime-helper-gate` на push `main` | accepted |
| D-005 | 2026-05-19 | CI fixes: testdata vectors, schema 2026-04-19, grep not rg | accepted |
| D-006 | 2026-05-19 | Pilot helper только после green CI; чеклист отдельно | accepted |
| D-007 | 2026-05-19 | WG production ≠ tunrnd mesh в операционной дисциплине | accepted |
| D-008 | — | Разделить Makefile targets `helper-*` / `tunrnd-*` | proposed |
| D-009 | — | Опубликовать критичные runbook’и из `.docs/` в `docs/runbooks/` | proposed |
| D-010 | — | Обязательный `-auth-token-file` даже для unix в production | proposed |
| D-011 | 2026-05-19 | Pilot runtime-helper не на EDG/VRN/uplinks; mesh ещё running — только recon/decommission plan | accepted |
| D-012 | 2026-05-19 | `bx_msk_d` / 158.160.254.197 не использовать; monitoring на `msk` = server-msk 85.239.44.49 | accepted |
