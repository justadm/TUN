# Client Workplan

Date: 2026-04-08

## Scope

This is the first execution-oriented workplan for the JsTun client program.

It assumes:

- `docs/` is now the canonical location for active planning
- first-generation clients are WireGuard-first with embedded setup
- `tun-rnd` remains an experimental parallel stream

## Program tracks

### Track A. Documentation and decisions

Goal:

- establish one canonical planning surface

Tasks:

- freeze docs policy
- capture product and tunnel decisions
- maintain working log in `docs/app/`

### Track B. Auth and account platform

Goal:

- support app-grade login and account ownership model

Tasks:

- integrate OAuth component into account-based LK/service path
- replace memory-only OAuth state with Redis or DB
- implement VK provider
- implement verified Telegram flow
- clarify whether MAX can support real user sign-in for this product
- define account/device/profile/session model

### Track C. Client-facing API

Goal:

- expose machine-facing APIs instead of HTML-first portal flows

Tasks:

- define `/me` contract
- define device registration contract
- define profile bootstrap contract
- define stats and security event summaries
- define billing/subscription API slice

### Track D. Embedded tunnel enablement

Goal:

- eliminate manual config transfer in the happy path

Tasks:

- define bootstrap payload
- implement mobile tunnel adapters
- implement desktop privileged helpers
- implement config refresh/revoke path
- expose tunnel state to UI

### Track E. App shell and UX

Goal:

- deliver modern cross-platform clients

Tasks:

- choose shared app framework
- define design system
- implement auth flow
- implement protection home screen
- implement stats/security/device screens
- implement onboarding and recovery UX

### Track F. `tun-rnd` productization

Goal:

- make future migration possible without blocking release

Tasks:

- finish real packet path
- implement platform adapters
- implement lifecycle resilience
- implement observability
- complete security and release validation

## Suggested execution order

### Step 1. Freeze contracts

Deliver:

- docs policy
- automatic setup decision
- tunnel strategy decision

### Step 2. Build backend substrate

Deliver:

- account/auth stabilization
- device/profile/session model
- client JSON API

### Step 3. Deliver mobile first

Reason:

- mobile value is highest for one-button protection
- iOS and Android constraints will force early correctness in API and provisioning design

Deliver:

- app shell
- iOS embedded setup
- Android embedded setup
- traffic and state telemetry

### Step 4. Deliver desktop second

Deliver:

- Windows helper/service
- macOS helper/extension
- Linux helper/service
- aligned UI and diagnostics

### Step 5. Add experimental `tun-rnd` lane

Deliver:

- backend abstraction
- feature-flagged engine selection
- internal-only rollout path

## Milestone view

### M0. Planning baseline

Definition of done:

- app docs established in `docs/app/`
- documentation policy recorded
- tunnel readiness assessment recorded
- first roadmap and workplan recorded

### M1. Backend ready for apps

Definition of done:

- app auth supported
- device/profile bootstrap API exists
- persistent OAuth state exists
- profile and session ownership model is stable

### M2. Mobile beta

Definition of done:

- iOS and Android app can sign in
- app can provision and activate tunnel automatically
- app shows state, traffic, and core diagnostics

### M3. Desktop beta

Definition of done:

- Windows, macOS, Linux app can provision and activate tunnel automatically
- support diagnostics and device management exist

### M4. Unified v1 release candidate

Definition of done:

- all target platforms support same product core
- manual `.conf` is fallback-only
- rollout and rollback paths are defined

## Immediate next documents to create

- app architecture overview
- client API contract
- auth provider rollout matrix
- mobile tunnel adapter design
- desktop helper architecture
- UI information architecture

## Working rule

Every meaningful planning decision, architecture note, or scope change for the app program should be recorded under `docs/app/`.
