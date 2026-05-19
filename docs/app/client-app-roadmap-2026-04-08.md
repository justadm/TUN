# Client App Roadmap

Date: 2026-04-08

## Objective

Build modern mobile and desktop clients for JsTun with:

- iOS, Android, Windows, macOS, Linux coverage
- account login via Telegram, MAX, VK, Yandex, Google
- one-button protection control
- traffic and security statistics
- automatic tunnel setup without manual `.conf` transfer

## Strategic decision

The first shipping generation should be WireGuard-first with embedded tunnel control.

This satisfies the product requirement of automatic setup while keeping `tun-rnd` off the critical path until it becomes production-ready.

## Canonical requirements

### User requirements

- sign in quickly with supported providers
- activate protection from the app directly
- see whether protection is on, healthy, and where traffic exits
- see traffic usage and meaningful security events
- manage devices and profiles without portal gymnastics

### Product requirements

- no manual config handoff in the normal flow
- same brand and UX across mobile and desktop
- gradual migration from current portal/LK to account-based model
- ability to support both WireGuard and future `tun-rnd` engines

## Delivery phases

### Phase 0. Documentation and contracts

Deliverables:

- docs policy
- app architecture overview
- tunnel strategy decision
- initial API contract for clients

Status:

- started on 2026-04-08 in `docs/app/`

### Phase 1. Backend foundation

Deliverables:

- account-based auth integrated with current control-plane
- machine-facing `/me`, `/devices`, `/profiles`, `/stats`, `/security-events` APIs
- persistent OAuth state store
- provider completion plan for VK, Telegram verify, MAX clarification

Notes:

- current OAuth component supports Yandex and Google directly, while Telegram and MAX need special handling and VK still needs implementation

### Phase 2. Embedded WireGuard control

Deliverables:

- mobile tunnel adapters
- desktop privileged helpers
- automatic provisioning flow from app to tunnel backend
- no required manual `.conf` export in the happy path

Implementation stance:

- keep `.conf` export only as recovery and support fallback
- the UI should treat automatic setup as the default and preferred path

### Phase 3. Application shell

Deliverables:

- shared design system
- login flow
- home screen with animated protection toggle
- stats screens
- device management
- subscription and billing screens

### Phase 4. Reliability and rollout

Deliverables:

- reconnect and background behavior
- diagnostics
- crash and network telemetry
- feature flags and staged rollout

### Phase 5. `tun-rnd` experimental lane

Deliverables:

- backend abstraction for multiple tunnel engines
- hidden or opt-in experimental engine selector
- limited rollout and metrics

## Automatic setup architecture

### Provisioning flow

1. User authenticates in app
2. App registers or resolves device record
3. App requests tunnel bootstrap payload from control-plane
4. App hands payload to platform tunnel adapter
5. Adapter creates or updates local tunnel configuration
6. Adapter activates protection directly
7. App renders live status and metrics

### Bootstrap payload contents

- device/profile identifier
- server endpoint metadata
- tunnel public parameters
- engine type: `wireguard` or future `jstun`
- DNS and routing policy
- expiry and refresh metadata
- optional feature flags

### Recovery path

Keep manual download/export only as support fallback, not as the default UX.

## Work breakdown

### Backend and control-plane

- finalize account model
- provide client-facing JSON API
- unify profile and device concepts
- expose usage and security event summaries
- build provisioning endpoints for embedded tunnel adapters

### Mobile apps

- Flutter shell
- iOS adapter
- Android adapter
- app lifecycle and state synchronization

### Desktop apps

- Flutter desktop shell
- Windows service/helper
- macOS extension/helper
- Linux service/helper

### UX and product

- login and consent UX
- tunnel state language and animations
- onboarding and failure recovery
- device and session management

## Suggested sequence

1. Backend client API and auth stabilization
2. Mobile automatic setup
3. Desktop automatic setup
4. Unified stats and security feed
5. `tun-rnd` experimental integration

## Non-goals for first release

- making `tun-rnd` the only tunnel engine
- exposing raw operational admin concepts to end users
- relying on manual config transfer as the main setup path
