# Support bundle — data classification (draft)

**Статус:** draft (2026-05-19), после аудита F-017.  
**Код:** `internal/runtime/diagnostics.go`, `cmd/support-bundle-verify`.

## Что входит

- Runtime state machine snapshot (redacted)
- JSON event log tail (redacted via `reSecretKeyword`, long hex/base64 patterns)
- Envelope: `sha256` checksum; optional HMAC (`support-signing-key-file`)

## Redaction (реализовано в коде)

Паттерны: `token|secret|password|private|api_key|bearer|auth`, длинный hex, base64-like strings.  
**Не гарантирует** отсутствие PII: device IDs, profile IDs, IP маршруты могут остаться.

## PII / чувствительные поля (ручная политика)

| Класс | Примеры | В bundle? | Retention |
|-------|---------|-----------|-----------|
| Credentials | WG keys, tokens, HMAC keys | Нет (redact) | — |
| Identity | account email, phone | Не должны | N/A |
| Device | `deviceID`, lease owner | Да (support) | 90d max (предложение) |
| Network | routes, addresses, peer IPs | Да | 90d |
| Security signals | contract v1 payloads | Да, если включены | по incident |

## Signing rotation

- Active key id `k2` в `/etc/tun/support-signing-k2.key`
- Verify: `go run ./cmd/support-bundle-verify` с `--previous-key`, `--retired-key-id`
- Gate: `scripts/support_bundle_ingest_gate.sh`

## TODO (backlog)

- [ ] Юридическое согласование retention с LK/accounts
- [ ] Явный список полей envelope в OpenAPI/helper schema
- [ ] Тест: bundle never contains raw `Authorization` header
