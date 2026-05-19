# Backlog (после git-sync 2026-05-19)

Приоритет сверху вниз. Источник: [AUDIT_2026-05-19.md](../AUDIT_2026-05-19.md), [session-worklog-2026-05-19.md](./session-worklog-2026-05-19.md).

## P0 — сделать в ближайшие дни

- [ ] **Оператор:** подтвердить судьбу tun-rnd mesh (сейчас **active** на vrn/ams/fra/nyc/spb — см. [infra-recon-2026-05-19.md](./infra-recon-2026-05-19.md))
- [ ] **Pilot runtime-helper** — **не на prod EDG**; staging VM или dev laptop — [runtime-helper-pilot-checklist.md](./runtime-helper-pilot-checklist.md)
- [ ] **MemLayer reimport** TUN после изменения структуры (`MEMLAYER.md`, import script)
- [ ] Подтвердить, что `monitoring/.env` никогда не попадал в git history (`git log -p -- monitoring/.env`)

## P1 — 2 недели

- [ ] `docs/support-bundle-data-classification.md` — draft есть; юридическое согласование и полный список полей
- [x] Заменить `rg` в ops-скриптах на `grep` (2026-05-19)
- [ ] Makefile: `make helper-*` vs `make tunrnd-mesh-*` (D-008)
- [ ] `docs/runbooks/` — cutover, write-mirror, DR (выжимка из `.docs/`, без секретов)

## P2 — квартал

- [ ] macOS job в CI для `internal/tun/open_darwin.go`
- [ ] monitoring: `requirements.lock`, workflow `docker compose build`
- [ ] control-plane billing adapter (сейчас только README contract)
- [ ] Deprecate legacy helper paths (`/bridge.startup` → только `/v1/helper/*` для JsTun)

## Сделано ✓

- [x] Git sync платформы (`074706d`)
- [x] `.gitignore` для секретов и кэшей
- [x] CI green: go-test + runtime-helper-gate (`2862b0d`)
- [x] Аудит `docs/AUDIT_2026-05-19.md`
- [x] Ops worklog + decisions + backlog (этот каталог)
- [x] MemLayer project TUN + memory entries
- [x] Все `scripts/*.sh`: `rg` → `grep`
- [x] Read-only infra recon SSH 2026-05-19 (`infra-recon-2026-05-19.md`)
