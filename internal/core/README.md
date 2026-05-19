# core

Protocol core: handshake, AEAD, replay window.

Notes for v0:
- handshake model is NK-style, server-auth-only (client pins server static key).
- rekey control message types are defined with binary payloads: `RekeyInitV1/RekeyAckV1`.
- in-session rekey M2 is active:
  - dual-key overlap decrypt (active + prepared-next),
  - cutover on accepted ack,
  - replay mark after successful decrypt.
- key refresh reconnect-rotation (v0) remains supported as fallback policy.
