# Runtime-helper pilot checklist (staging)

**Статус:** не выполнялось на реальных хостах (только локально/CI).  
**Предусловие:** `main` green — [GitHub Actions](https://github.com/justadm/TUN/actions).

## Решения для pilot (D-006, D-010)

- Unix socket **и** `-auth-token-file` (0600) на staging — не полагаться только на chmod socket.
- Не использовать `rollout_tunrnd_*` в том же change window (R&D mesh).
- JsTun bridge: только `/v1/helper/*`, schema `2026-04-19`.

## Checklist

### A. Host prep

- [ ] Бинарь `runtime-helper` из commit с зелёным CI (tag опционально)
- [ ] `deploy/systemd/tun-runtime-helper.service` + `runtime-helper.env.example` → `/etc/tun/runtime-helper.env`
- [ ] `TUN_HELPER_SOCKET=/run/tun/runtime-helper.sock`
- [ ] `TUN_HELPER_AUTH_TOKEN_FILE=/etc/tun/runtime-helper.token` (mode 600)
- [ ] `systemctl enable --now tun-runtime-helper`
- [ ] `ss -xl | grep runtime-helper` — socket exists, permissions 0600

### B. Gate на staging (с jump-host или на node)

```bash
make gate-staging-full-strict \
  BUNDLE=/var/tmp/support-bundle.json \
  ACTIVE_KEY=k2=/etc/tun/support-signing-k2.key \
  REPORT=/var/tmp/runtime-helper-gate-report.json
```

- [ ] Report JSON без failed steps
- [ ] Support bundle ingest: `scripts/support_bundle_ingest_gate.sh` (если не skip)

### C. JsTun integration smoke

- [ ] `runtime-helperctl -action bridge.startup` с реальным `bootstrap.json`
- [ ] `lease.ensure` / `bridge.shutdown` — handoff без blind takeover
- [ ] SSE `/v1/helper/events` — не логировать token в proxy

### D. Rollback

- [ ] `bridge.shutdown` + `systemctl stop tun-runtime-helper`
- [ ] `scripts/rollback_local_tun_client.sh` (если поднимался tun-rnd client)
- [ ] Документировать время и `X-Request-ID` из failed calls

## Evidence to attach

- `runtime-helper-gate-report.json`
- `journalctl -u tun-runtime-helper` excerpt (без token lines)
- Версия commit: `git rev-parse HEAD`
