# TUN Productization Gap Analysis

Date: 2026-04-08

## Executive summary

`tun-rnd` is not ready to be the production tunnel engine for the first generation of client applications.

The repository explicitly states that the product baseline is a WireGuard-based access platform and that the custom TUN transport is still an R&D direction. See:

- [/Users/just/projects/TUN/README.md](/Users/just/projects/TUN/README.md)
- [/Users/just/projects/TUN/cmd/client/main.go](/Users/just/projects/TUN/cmd/client/main.go)
- [/Users/just/projects/TUN/cmd/server/main.go](/Users/just/projects/TUN/cmd/server/main.go)

Today, the research implementation demonstrates:

- TLS transport setup
- custom handshake
- encrypted frame exchange
- simple ping/pong behavior
- benchmark mode

It does not yet demonstrate a full consumer-grade tunnel runtime suitable for iOS, Android, Windows, macOS, or Linux shipping clients.

## Current state

### What exists now

- protocol core: handshake, AEAD, replay window, rekey
- transport abstraction and TLS stream adapter
- basic client and server entrypoints
- TUN abstraction placeholder
- protocol docs and test vectors under `.docs/spec/`

### What is missing for product use

- packet-forwarding data path integrated with real TUN devices
- stable session lifecycle for mobile and desktop
- platform-specific privileged tunnel adapters
- reconnect and roaming behavior
- MTU and fragmentation strategy
- kill switch and DNS-leak controls
- observability for production support
- configuration and policy distribution model
- security review of the full runtime
- automated interop, soak, and chaos testing

## Evidence-based assessment

### Product position

The repository README states:

- production direction is WireGuard-based ingress and managed edges
- custom TUN transport is a future uplink/transport option, not the current baseline

That is a direct signal that the project itself does not yet treat `tun-rnd` as release-ready product infrastructure.

### Implementation shape

The current client and server entrypoints are still at the handshake and test-exchange level.

Observed behavior from the code:

- client performs handshake, sends `ping`, reads `pong`
- server performs handshake, reads one encrypted frame, responds with `pong`
- optional benchmark mode sends framed payloads until `done`

This is useful as protocol validation and benchmarking, but it is not a complete tunnel product runtime.

## Product requirements for embedded use in applications

To ship a tunnel engine inside mobile and desktop clients, the runtime must satisfy all of the following.

### Functional requirements

- Create, configure, and own a real OS tunnel interface
- Push IP packets through the tunnel, not only framed messages
- Handle DNS routing and split/full tunnel policies
- Support policy updates without reinstalling profiles
- Resume after app restarts or transient network loss
- Support token-based or certificate-based device authorization
- Expose health, traffic, and diagnostic telemetry to the UI

### Reliability requirements

- fast reconnect after path loss
- NAT rebinding tolerance
- mobile background survival
- bounded memory and CPU usage
- session resumption or equivalent low-friction reconnect
- graceful version negotiation and downgrade behavior

### Security requirements

- formal key lifecycle and rotation rules
- replay protection at runtime, not only in lab tests
- server authentication and trust-anchor management
- device binding and session invalidation
- anti-abuse controls around provisioning and login
- secret storage on each platform
- audit events without credential leakage

### Platform requirements

- iOS: `NEPacketTunnelProvider`, background behavior, entitlement handling
- macOS: Network Extension or system extension, notarization, permissions
- Android: `VpnService`, foreground service requirements, OEM background restrictions
- Windows: service/helper separation, TUN driver packaging, update safety
- Linux: system service, privilege separation, distro variance

## Required workstreams before product rollout

### 1. Data-path completion

Implement the actual tunnel runtime, not only encrypted message exchange.

Required outputs:

- packet read/write loop for TUN device
- framing strategy for packets and control messages
- flow control and backpressure handling
- keepalive strategy
- idle timeout strategy

Exit criteria:

- an application can open a tunnel and pass real IP traffic through it end-to-end

### 2. Session lifecycle and resilience

Required outputs:

- reconnect state machine
- session resume or fast re-auth
- roaming support when network path changes
- explicit error taxonomy for UI and support
- policy on stale sessions and server-side invalidation

Exit criteria:

- device survives Wi‑Fi to LTE switches, sleep/wake, and brief upstream loss without manual repair

### 3. Platform tunnel adapters

Required outputs:

- iOS Network Extension implementation
- Android `VpnService` implementation
- macOS privileged tunnel helper
- Windows service/helper and driver integration
- Linux service/helper integration

Exit criteria:

- same control app can start/stop the tunnel on all target platforms without manual file exchange

### 4. Provisioning and control-plane integration

Required outputs:

- machine-facing client API for device provisioning
- account/device/profile/session model
- device attestation or durable device registration model
- policy/config payload format for tunnel bootstrap
- rollout flags to choose tunnel backend per ring

Exit criteria:

- client app can authenticate, register a device, obtain tunnel policy, and activate protection automatically

### 5. Observability and supportability

Required outputs:

- structured runtime logs
- tunnel state model exposed to UI
- handshake/connectivity counters
- traffic counters
- error classification
- support bundle export

Exit criteria:

- support team can diagnose failures from logs and client diagnostics without shell access

### 6. Security hardening

Required outputs:

- cryptographic review against the current handshake and framing design
- trust and key-management document
- secure storage adapters per platform
- revocation/session kill model
- abuse and anomaly event taxonomy

Exit criteria:

- security review closes critical findings and produces an approved release checklist

### 7. Test matrix and release validation

Required outputs:

- unit tests for core and transport
- interop tests across versions
- soak tests
- network impairment tests
- mobile lifecycle tests
- desktop packaging and auto-update tests

Exit criteria:

- release candidate passes a predefined cross-platform matrix over sustained runs

## Recommended delivery strategy

### Phase A. Product launch path

Launch first-generation clients with embedded WireGuard-based tunnel control, not with `tun-rnd`.

Reason:

- it satisfies the product goal of one-button protection much sooner
- it removes the current manual `.conf` exchange
- it does not block the UI, auth, billing, and account roadmap on unfinished tunnel R&D

### Phase B. Experimental transport path

Continue `tun-rnd` under a feature flag and restricted rollout ring:

- internal dogfood
- staff beta
- opt-in experimental users

### Phase C. Promotion gate

Promote `tun-rnd` to product only after:

- embedded tunnel adapters exist on all target platforms
- resilience and supportability metrics are acceptable
- security review is complete
- rollback to WireGuard remains available

## Automatic setup requirement

The project should explicitly reject a product model based on manual transfer of WireGuard configs.

### What to avoid

- showing raw `.conf` as the primary path
- asking users to copy files between portal and app
- depending on external WireGuard UI for the normal happy path

### What to build instead

- app login
- device registration
- provisioning token exchange
- tunnel activation through embedded platform backend
- policy updates delivered from control-plane API
- silent refresh/reissue when safe

This automatic setup requirement is valid both for the immediate WireGuard-first product and for any future `tun-rnd` engine.

## Decision

For application delivery, treat `tun-rnd` as an experimental transport workstream, not as the critical path for client release.

The critical path for the first shipping apps is:

- account and auth
- client-facing API
- embedded tunnel setup without manual config transfer
- polished mobile and desktop UX
