# TUN Runtime Security Variants (VPN/Proxy abuse)

Date: 2026-04-13

## Source reviewed

- `/Users/just/projects/JsTun/.docs/1/ocr_methodika_vpn_proxy.md`

## Threat model extracted from methodology

- GeoIP/ASN/hosting mismatch and reputation hits (VPN/proxy/TOR) on server side.
- Contradicting location signals (server geography vs client geography).
- Client direct signals (OS VPN/proxy flags).
- Client indirect signals (interfaces/routes/DNS), with high false-positive risk.
- Benign-but-suspicious scenarios requiring dampening:
  - corporate VPN and whitelisted networks
  - iCloud Private Relay
  - roaming/NAT/CDN artifacts

## Implemented in this repository

1. New helper endpoint for server-side decisioning:
   - `POST /v1/helper/security.evaluate`
   - legacy alias: `POST /security.evaluate`

2. Decision engine with tri-state output:
   - `not_detected`
   - `additional_check`
   - `detected`

3. Protection plan synthesis (actionable controls):
   - `not_detected` -> `allow`
   - `additional_check` -> `allow_with_limits`, `step_up_auth`, `scheduled_recheck`
   - `detected` -> `deny_sensitive_actions`, `step_up_auth`, `manual_review_queue`
   - escalation (`tor`/repeat) adds `temporary_block`

4. False-positive dampeners:
   - corporate whitelist downgrade
   - iCloud Private Relay downgrade (when no direct signal)
   - roaming downgrade (when no direct/TOR signal)

5. Helperctl support:
   - action `security.evaluate` mapped to `/v1/helper/security.evaluate`

## Additional hardening implemented (full pack)

1. Signal provenance anti-spoofing

- HMAC verification for client-signal envelope in `security.evaluate`.
- Config: environment variable `SECURITY_SIGNAL_HMAC_KEY`.
- Required fields when key is configured:
  - `signalTimestamp`
  - `signalNonce`
  - `signalSignature`
- Replay defense via nonce cache (time-bounded).
- Signature base string:
  - `deviceID|tenantID|nonce|directDetected|indirectDetected|geoipDetected|signalTimestamp`

2. Reputation cache with TTL and source quality

- New endpoint:
  - `POST /v1/helper/security.reputation.upsert`
- Entry model includes:
  - `ip`, `source`, `riskType`, `confidence`, `expiresAt`, `sourceScore`.
- Source quality weighting implemented (`ranr`, `maxmind`, `ip2location`, etc.).
- Reputation upserts and decisions are pushed into security audit stream.

3. Decision hysteresis

- Per subject (`tenantID + deviceID/IP`) history retained.
- `temporary_block` and `hardBlock=true` are applied only after threshold.
- Default threshold is controlled by tenant policy.

4. Tenant policy profiles and rollout flags

- New endpoints:
  - `POST /v1/helper/security.policy.upsert`
  - `GET /v1/helper/security.policy.get`
- Supported profiles:
  - `strict`
  - `balanced`
  - `permissive`
- Policy controls:
  - `enforce`
  - `hysteresisThreshold`
  - `hysteresisWindowSec`

5. Corporate allow rules with expiration governance

- New endpoint:
  - `POST /v1/helper/security.corporate-allow.upsert`
- Rules support:
  - ASN allow
  - CIDR allow
  - TTL-bound expiry
- Applied as false-positive dampener in `security.evaluate`.

6. Security audit endpoint

- New endpoint:
  - `GET /v1/helper/security.audit`
- Returns rolling audit entries for:
  - policy/rule/reputation updates
  - evaluate outcomes
  - signature rejections

7. Unified ingestion path for JsTun client signals

- New endpoints:
  - `POST /v1/helper/security.signal.ingest`
  - `GET /v1/helper/security.signal.ingest.recent`
- `security.signal.ingest` supports:
  - signature-required mode (`requireSignature=true`)
  - evaluate toggle (`evaluate=true|false`)
  - immediate normalization into same decision engine used by `security.evaluate`.

8. Tenant rollout endpoint

- New endpoint:
  - `POST /v1/helper/security.policy.rollout`
- Applies:
  - default profile for all tenants
  - strict profile list for high-risk tenants
- Intended initial rollout:
  - `defaultProfile=balanced`
  - strict only for explicitly listed risk tenants.

## Contract and version sync

- Canonical contract: `docs/contracts/security_signal_contract_v1.json`
- Helper schema version field: `securityContractVersion=2026-04-13`
- Sync source analyzed: `/Users/just/projects/JsTun/.docs/1/` (including `apps-research.md`, `blokirovka-vpn-i-ii-rkn.md`, tables in `img/`).

## Quick runbook

1. Rollout defaults:
   - `scripts/runtime_helper_security_rollout.sh --default-profile balanced --strict-tenants tenantA,tenantB`
2. Upsert reputation:
   - helperctl `-action security.reputation.upsert` with payload `{tenantID, ip, source, riskType, confidence, ttl}`
3. Ingest signed client signal:
   - helperctl `-action security.signal.ingest -payload-file ...`
4. Check outcomes:
   - helperctl `-action security.audit`
   - helperctl `-action security.signal.ingest.recent`

## Input contract (security.evaluate)

```json
{
  "geoipDetected": true,
  "directDetected": false,
  "indirectDetected": true,
  "hostingRisk": true,
  "torRisk": false,
  "vpnReputationRisk": true,
  "corporateWhitelisted": false,
  "serverCountry": "DE",
  "clientCountry": "RU",
  "clientRegion": "MOW",
  "icloudPrivateRelay": false,
  "roamingLikely": false,
  "repeatOffenseCount": 1
}
```

## Output contract (security.evaluate)

```json
{
  "ok": true,
  "decision": "detected",
  "protectionPlan": ["deny_sensitive_actions", "step_up_auth", "manual_review_queue"],
  "riskScore": 90,
  "reasons": [
    "geoip_risk_detected",
    "indirect_client_signal_detected",
    "hosting_asn_risk",
    "vpn_proxy_reputation_risk",
    "geoip_client_country_mismatch_ru"
  ]
}
```

## Recommended next hardening steps

1. Add signed provenance for client-side signals (anti-spoofing of direct/indirect flags).
2. Add reputation feed cache with TTL, source quality ranking, and audit trail.
3. Add decision hysteresis over time window (N events) before hard block.
4. Add per-tenant policy profiles (strict/balanced/permissive) and rollout flags.
5. Add explicit allow rules for known corporate CIDR/ASN with expiration governance.
