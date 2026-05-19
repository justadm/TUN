# Embedded WireGuard Automatic Setup

Date: 2026-04-08

## Goal

Replace the current manual provisioning model based on QR and `.conf` transfer with automatic in-app tunnel setup across:

- iOS
- Android
- Windows
- macOS
- Linux

## Product decision

Automatic setup is the primary product path.

Manual export remains only for:

- support recovery
- break-glass onboarding
- environments where the embedded adapter is temporarily unavailable

## Desired user flow

1. User signs in
2. App creates or selects a device/profile
3. App requests tunnel bootstrap from backend
4. Embedded adapter applies configuration locally
5. User presses `Enable protection`
6. Tunnel starts without leaving the app
7. UI shows live status, traffic, and diagnostics

## Why this matters

Manual config transfer is incompatible with the target product position:

- it adds friction
- it leaks technical complexity into onboarding
- it makes device lifecycle harder to manage
- it blocks a trustworthy one-button UX

## Control-plane requirements

The backend needs an explicit bootstrap API for embedded adapters.

Suggested response shape:

```json
{
  "engine": "wireguard",
  "device_id": "dev_123",
  "profile_id": "prof_123",
  "config_version": 1,
  "expires_at": "2026-04-08T12:00:00Z",
  "wireguard": {
    "interface": {
      "private_key_ref": "secure-store://device-private-key",
      "addresses": ["10.0.0.2/32"],
      "dns": ["1.1.1.1", "9.9.9.9"],
      "mtu": 1280
    },
    "peer": {
      "public_key": "base64...",
      "endpoint": "edge.example.com:51820",
      "allowed_ips": ["0.0.0.0/0", "::/0"],
      "persistent_keepalive": 25
    },
    "policy": {
      "mode": "full_tunnel",
      "kill_switch": true,
      "on_demand": false
    }
  }
}
```

The exact schema can differ, but the idea is important:

- machine-facing payload
- no human-oriented export assumptions
- explicit versioning

## Platform implementation notes

### iOS

Recommended path:

- `NEPacketTunnelProvider`
- app group for secure coordination if needed
- tunnel configuration installed and controlled by the app

Needs:

- entitlement setup
- secure key storage in Keychain
- app-to-extension IPC/state sync
- battery-conscious reconnect behavior

### Android

Recommended path:

- `VpnService`
- foreground service for active tunnel
- strong handling for OEM background restrictions

Needs:

- permission and consent flow
- secure storage for secrets
- resilience under doze/app standby
- quick settings integration later

### macOS

Recommended path:

- Network Extension based tunnel management
- packaged helper/extension and app coordination

Needs:

- entitlement and signing model
- distribution/notarization planning
- secure local state and update safety

### Windows

Recommended path:

- desktop app plus privileged service/helper
- Wintun/WireGuard-NT backend integration

Needs:

- service installation lifecycle
- admin rights handling
- safe updates and rollback
- IPC between UI and service

### Linux

Recommended path:

- app UI plus privileged system helper/service
- system-level tunnel activation through supported WG backend

Needs:

- distro policy variance
- Polkit or equivalent privilege bridge
- secure config handling
- startup and persistence behavior

## Implementation architecture

### Shared layers

- UI app
- API SDK
- device/profile model
- tunnel state model
- diagnostics collector

### Platform-specific layers

- tunnel adapter
- secure storage adapter
- privileged helper where required
- OS lifecycle integration

## Required backend endpoints

- `POST /v1/app/devices/register`
- `POST /v1/app/profiles/create`
- `POST /v1/app/profiles/{id}/bootstrap`
- `POST /v1/app/profiles/{id}/refresh`
- `POST /v1/app/profiles/{id}/revoke`
- `GET /v1/app/runtime/status`
- `GET /v1/app/runtime/stats`

Exact names are provisional, but this scope should exist.

## Minimum feature set for first embedded release

- create device/profile from app
- fetch bootstrap payload
- apply config locally
- start/stop tunnel
- read current connection state
- read traffic counters
- read last handshake time
- recover from expired config by refresh

## Deferred features

- split-tunnel UI
- per-app routing
- mesh/multi-hop options
- advanced DNS policy UI
- seamless engine switching to `tun-rnd`

## Risks

- iOS/macOS entitlement friction
- Windows service packaging complexity
- Linux privilege fragmentation
- state drift between backend profile record and local adapter
- session expiry during background operation

## Recommendation

Make embedded WireGuard automatic setup the first tunnel milestone for the application program.

It gives the product the required one-button experience without waiting for `tun-rnd` to mature into a full production engine.
