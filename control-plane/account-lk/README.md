# Account-LK

This module is the repository-native starting point for the new account-based LK.

It is intentionally separate from the current WG-first LK and current portal runtime.

Scope of the first iteration:

- define ownership and identity schema
- define store contract for the new LK
- add import bridge from legacy peers to account-owned connection profiles
- add a minimal local persistence adapter
- add the first service layer for account-based auth and LK payloads
- keep current control-plane issuance/runtime unchanged

Non-goals of this directory:

- replacing the current live LK immediately
- replacing admin
- replacing current peer/routing runtime tables

The migration strategy is:

1. add account-owned schema
2. add store contract
3. build the new LK above current control-plane
4. keep WG-first LK only as compatibility/recovery

Current repository-native components:

- `store.py`
  phase-1 dataclasses and storage contract
- `sqlite_store.py`
  minimal sqlite-backed adapter for local development and smoke tests
- `service.py`
  first account/identity/session/profile service layer
- `import_legacy_profiles.py`
  helper that converts legacy peer snapshots into staged account/profile import payloads
- `import_runner.py`
  loads staged payloads or raw legacy snapshots into the sqlite-backed phase-1 store
- `http_app.py`
  minimal HTTP slice for account login, session-backed LK reads, profile creation, and legacy profile attach
- `smoke.py`
  local end-to-end smoke for the phase-1 stack
- `http_smoke.py`
  local HTTP smoke for the minimal account-based auth/LK flow

Current bridge routes:

- `/account/login`
  HTML login bridge for the new account-based LK
- `/account/`
  account home page with identities, balance, and connection profiles
- `/account/profiles/new`
  create a new account-owned profile stub
- `/account/claims/new`
  attach an existing legacy WG profile to an account
