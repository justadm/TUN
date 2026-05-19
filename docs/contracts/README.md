# Security Contract Versioning

- Current contract file:
  - `security_signal_contract_v1.json`
- Current runtime-helper schema version:
  - `securityContractVersion = 2026-04-13`

## Sync rule with JsTun

1. JsTun mobile/web producers must emit payloads conforming to `security_signal_contract_v1.json`.
2. TUN runtime-helper `/v1/helper/security.signal.ingest` and `/v1/helper/security.evaluate` consume this contract.
3. Any breaking contract change requires:
   - new file `security_signal_contract_v{N}.json`
   - new `securityContractVersion` value in helper schema
   - compatibility note in `docs/tun_security_protection_variants_2026-04-13.md`.
