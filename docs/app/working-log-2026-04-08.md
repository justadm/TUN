# Working Log

Date: 2026-04-08

## Decisions recorded

### 1. Documentation split

Decision:

- `docs/` becomes the canonical location for active documentation
- `.docs/` remains as legacy/internal archive and protocol-spec store

Reason:

- there is no live `docs/` directory yet, so we can establish a clean convention now
- `.docs/` currently mixes archival material, specs, runbooks, and product notes

### 2. Tunnel delivery strategy

Decision:

- do not put `tun-rnd` on the critical path for first-generation clients
- ship automatic setup on top of embedded WireGuard control first

Reason:

- product requirement is one-button activation without manual config handling
- current `tun-rnd` code is still at R&D maturity

### 3. Automatic setup policy

Decision:

- reject manual `.conf` transfer as the primary happy path
- keep it only as fallback/recovery tooling

Implication:

- client and backend work must target embedded tunnel provisioning from day one

## Actions completed

- updated top-level repository guidance to introduce `docs/`
- documented the `docs/` versus `.docs/` policy
- created app-planning workspace in `docs/app/`
- wrote detailed `tun-rnd` productization gap analysis
- wrote first client roadmap centered on automatic setup

## Follow-up items

- define client-facing API contract in `docs/app/`
- define provider-by-provider auth delivery plan
- define embedded WireGuard adapter strategy per platform
- define UI information architecture and release rings
