# Rekey v1 Protocol Draft

Date: 2026-04-13  
Status: Draft (M1)  
Scope: In-session key rotation for TUN runtime without full reconnect on normal path.

## 1. Goals

- Rotate session keys without tearing down healthy transport.
- Keep dataplane continuity across key cutover.
- Preserve replay safety and bounded overlap.
- Provide deterministic fallback to reconnect-rotation for unsupported peers.

## 2. Control-plane message types

Existing control frame types are reused:

- `ControlTypeRekeyInit` (2)
- `ControlTypeRekeyAck` (3)

## 3. RekeyInitV1 payload (fixed-size)

Binary layout (big-endian):

- `version` (1 byte) = `1`
- `flags` (1 byte)
- `reserved` (2 bytes) = `0`
- `epoch` (8 bytes) rekey sequence per session
- `overlapMillis` (4 bytes) key overlap window
- `notBeforeUnix` (8 bytes) sender requested activation lower bound
- `newKeyID` (16 bytes) opaque key identifier
- `rekeyNonce` (12 bytes) anti-replay nonce

Total: 52 bytes

Rules:

- `reserved` must be zero.
- `epoch > 0`.
- `overlapMillis` bounded by implementation policy.
- `newKeyID` must not be all-zero.
- `rekeyNonce` must not be all-zero.

## 4. RekeyAckV1 payload (fixed-size)

Binary layout (big-endian):

- `version` (1 byte) = `1`
- `status` (1 byte):
  - `0` = accepted
  - `1` = rejected
  - `2` = retry-later
- `reserved` (2 bytes) = `0`
- `epoch` (8 bytes) echoes init epoch
- `acceptedAtUnix` (8 bytes) receiver activation timestamp (or 0 on reject)
- `activeKeyID` (16 bytes) receiver selected key id
- `proof` (16 bytes) implementation-defined binding proof

Total: 50 bytes

Rules:

- `reserved` must be zero.
- `epoch > 0`.
- on `accepted`, `acceptedAtUnix > 0`.

## 5. Runtime state machine (target)

- `steady`
- `rekey_pending` (sent init / waiting ack)
- `overlap` (old+new keys valid)
- `cutover` (new key primary)
- `settled`
- `rollback` (on mismatch/error)

## 6. Compatibility and fallback

- If peer does not support `version=1` or rejects with unsupported reason:
  - mark session as `rekey_unsupported`
  - use current reconnect-rotation path.
- Fallback must be explicit and observable in telemetry.

## 7. Security constraints

- Nonce replay protection per `(session, epoch)`.
- Strict monotonic epoch progression.
- Overlap window upper bound to minimize replay surface.
- Reject invalid reserved fields or malformed payload lengths.

## 8. Implementation phases

Phase M1 (this draft):
- payload formats + parsing/validation scaffolding in `internal/core`.

Phase M2:
- runtime integration for negotiation and dual-key overlap.

Phase M3:
- rollout flags, observability, canary and fallback gates.
