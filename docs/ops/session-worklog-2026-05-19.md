# Session worklog: 2026-05-19

Журнал сессии с агентом (аудит → git sync → CI). Обновлять при продолжении работ; MemLayer — дублирование ключевых записей (см. `decisions.md`).

---

## Контекст запроса

- **Задача:** полный read-only аудит TUN (`docs/AUDIT_2026-05-19.md`), границы с JsTun и router.
- **Затем:** «приступай» — закрыть разрыв git ↔ локальная копия.
- **Затем:** push/CI через `gh` (аккаунт `justadm`, SSH).
- **Сейчас:** продолжать работу, **обязательно фиксируя** обсуждения, решения, сделанное и оставшееся.

---

## Обсуждения и принятые решения

### D-001 — Приоритет №1: git sync, не рефакторинг

**Контекст:** в remote было 44 tracked файла при ~278 локальных артефактах платформы.  
**Решение:** один крупный commit «repo sync», без разбиения на PR-серию (скорость важнее review granularity на этом этапе).  
**Почему:** CI и коллаборация бессмысленны, пока код не на GitHub.

### D-002 — Что не коммитить

**Решение:** явно в `.gitignore`:

- `/.tmp/`, `/artifacts/`
- `/monitoring/.env` (реальные токены TG/discovery)
- `__pycache__/`, общие `.env` (кроме `*.example`)

**Почему:** audit F-005; локальный `monitoring/.env` содержал реальные hex-токены.

### D-003 — `.docs/` остаётся в gitignore

**Обсуждение:** 106 md decision logs (апрель 2026) ценны, но не в remote.  
**Решение (текущее):** **не** снимать `/.docs/` ignore в этом этапе; канон для команды — `docs/` + этот `docs/ops/`.  
**Открыто:** позже — выборочный перенос runbook’ов в `docs/runbooks/` или отдельный commit `.docs/` без секретов.

### D-004 — CI: два workflow на main

**Решение:**

- `go-test.yml` — `go test ./...` + `make gate-ci-fast`
- `runtime-helper-gate.yml` — на `push main` и PR: contract-matrix → ci-fast + ci-full

**Почему:** helper gates не гонялись до sync (workflow был untracked).

### D-005 — Исправления CI без изменения продуктовой семантики

| Проблема | Решение |
|----------|---------|
| `.docs/spec/test-vectors.json` отсутствует на runner | Копия в `internal/testvectors/testdata/`, loader с fallback |
| `integration_linux_test.go`: `undefined: bytes` | Локальный `repeatByte()` |
| `TestBridgeStartupWithProfileBundle…` падает без CAP_NET_ADMIN | `validateBootstrapFn` mock в тесте + поле на `helperManager` |
| contract-matrix ожидал schema `2026-04-10`, код `2026-04-19` | Обновлены Makefile, smoke, release_gate defaults |
| smoke использовал `rg`, на Ubuntu runner нет | `grep -qF` в smoke/autopilot smoke |

### D-006 — Pilot runtime-helper — только после зелёного CI

**Решение:** staging pilot не начинать, пока `main` не зелёный; чеклист вынесен в [runtime-helper-pilot-checklist.md](./runtime-helper-pilot-checklist.md).  
**Ограничение:** агент **не** подключается к prod/staging SSH — только документация и локальные gates.

### D-007 — Production (WG) vs R&D (tun-rnd mesh)

**Подтверждено в аудите:** не смешивать релизы; `make rollout-mesh-*` = R&D only.  
**Не сделано:** разделение targets в Makefile (`helper-*` vs `tunrnd-*`) — в backlog.

---

## Что сделано (хронология)

| Время (сессия) | Действие | Commit / артефакт |
|----------------|----------|-------------------|
| 1 | Аудит репозитория | `docs/AUDIT_2026-05-19.md` |
| 2 | `.gitignore` расширен | часть `074706d` |
| 3 | Sync 278 файлов в git | `074706d` |
| 4 | `git push origin main` | remote обновлён |
| 5 | CI fail → fixes (vectors, linux test, preflight mock) | `f620a23` |
| 6 | CI: schema 2026-04-19 | `a508933` |
| 7 | CI: grep вместо rg | `2862b0d` |
| 8 | Все workflow зелёные | run `26093853705` |

**Текущий HEAD (на момент worklog):** `2862b0d` на `main`.

### Проверки CI (успешные)

- https://github.com/justadm/TUN/actions/workflows/go-test.yml
- https://github.com/justadm/TUN/actions/runs/26093853705 — runtime-helper-gate (contract-matrix, ci-fast, ci-full)

---

## Что ещё не сделано

См. [backlog.md](./backlog.md). Кратко:

1. **Pilot runtime-helper** на staging-хосте (unix + auth token + `gate-staging-full-strict`)
2. **`.docs/`** — политика публикации runbook’ов
3. **Makefile** — разделение WG vs tunrnd targets
4. **`docs/support-bundle-data-classification.md`** — политика PII в bundle
5. **macOS CI** для `internal/tun` darwin paths
6. **Python monitoring** — `requirements.lock`, CI compose build
7. **Billing adapter** в control-plane
8. **Оставшиеся `rg` в scripts** (не в CI сейчас): `monitor_links_autodiscovery_smoke.sh`, canary scripts

---

## Команды для воспроизведения

```bash
# Локальная проверка как CI
go test ./...
make gate-ci-fast
HELPER_SMOKE_TRANSPORT=unix make gate-ci-full

# Статус remote
gh run list --limit 5
git log --oneline -5
```

---

## MemLayer

- Проект TUN в MemLayer на момент сессии **не был** в `/projects` (нужен reimport из `MEMLAYER.md`).
- После создания worklog — записи `decision` / `task` / `event` через API (см. commit с ops docs).

---

## Продолжение сессии (документирование)

| Действие | Статус |
|----------|--------|
| `docs/ops/` — worklog, decisions, backlog, pilot checklist | done |
| `docs/support-bundle-data-classification.md` (draft) | done |
| MemLayer project `TUN` + 4 memory entries | done (`479795e9-8060-4717-8180-c0e6adba75bf`) |
| Все `rg` → `grep` в `scripts/*.sh` | done |
| Commit + push | done (`2f0b173`) |

## Инфраструктурный осмотр SSH (2026-05-19)

**Запрос оператора:** доступы есть на все алиасы; `bx_msk_d` снят; после падений WG — основной канал; на серверах живые клиенты — только осмотр.

**Действие:** read-only SSH на `edg vrn ams fra nyc msk exe` (+ `spb`).

**Отчёт:** [infra-recon-2026-05-19.md](./infra-recon-2026-05-19.md)

**Ключевые выводы:**

- **EDG:** production WG (control-api `127.0.0.1:18110`, portal, uplinks) + shadow tunnels; tun-rnd mesh **не running**; **runtime-helper нет**
- **VRN:** shadow stack **active** + tun-rnd `server@ams|fra|nyc` **ещё running** (watchdog@nyc failed)
- **AMS/FRA/NYC:** tun-rnd **clients** к spb/vrn **active** — mesh не снят
- **MSK (`server-msk`):** monitoring docker 18070/18071; хвосты `/etc/tun`; не `bx_msk_d`
- **SPB:** mesh servers + wg api 18110 — активный tun-rnd hub
- **EXE:** вне контура (Bitrix)
- **runtime-helper:** нигде не установлен

**Решение D-011 (предложено):** pilot helper **не** на EDG/VRN/uplinks; tun-rnd decommission — отдельное окно после подтверждения оператора.

## Следующий шаг (человек / агент)

1. Оператор: подтвердить — tun-rnd mesh целевое состояние «off»?
2. Pilot helper — отдельная staging VM или dev machine (см. recon)
3. MemLayer full reimport (`MEMLAYER.md`)
4. Makefile split helper vs tunrnd (D-008)
