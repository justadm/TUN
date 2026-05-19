# Control-Plane

Repository-owned home for the live JsTun control-plane.

Scope:

- portal HTTP
- control API
- portal CLI
- account-based LK ownership layer
- billing integration contract
- migration helpers
- runtime config
- service units
- smoke checks

Extraction rule:

- preserve live behavior first
- refactor only after runtime contract and artifacts are frozen here

Migration rule:

- normalize legacy state before changing read/write paths
- keep quarantine reports for malformed legacy rows
