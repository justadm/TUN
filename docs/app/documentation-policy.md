# Documentation Policy

Date: 2026-04-08

## Decision

Use `docs/` as the canonical location for active documentation going forward.

Retain `.docs/` as a legacy/internal archive and protocol-spec store until references are deliberately migrated.

## Why this decision

There is no active `docs/` directory in the repository today. Only `.docs/` exists. That means the repository is not suffering from literal duplicate files yet, but it is vulnerable to future duplication because `.docs/` mixes several different classes of material:

- protocol specification and test vectors
- internal research and dated planning notes
- operational runbooks
- product and roadmap discussions

Using `docs/` for active work makes the structure clearer and lowers the chance that user-facing plans, internal experiments, and archival notes drift into parallel versions.

## Directory split

`docs/`
- current product documents
- application strategy
- rollout plans
- API and integration contracts
- decision records

`.docs/`
- historical planning snapshots
- internal migration notes
- protocol-spec archive for `tun-rnd`
- files already referenced by scripts or code

## Migration rules

1. Do not bulk-move historical `.docs/` files yet.
2. New app and product documents go only to `docs/`.
3. If an old `.docs/` file remains relevant, write a fresh canonical document in `docs/` and link back to the older artifact.
4. Migrate `.docs/` files into `docs/` only when:
   - they are still operationally relevant,
   - their links can be updated safely,
   - and their ownership is clear.

## Immediate actions

1. Start `docs/app/` as the canonical workspace for client planning.
2. Keep `.docs/spec/` in place until the `tun-rnd` implementation and test-vector loaders are re-pointed intentionally.
3. Update top-level repository guidance so contributors know where new documents belong.
