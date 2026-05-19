# TUN Agent Guide

This repository is connected to MemLayer.

<!-- MEMLAYER_ROOT_PACK:START -->
# MemLayer Working Memory

Read [MEMLAYER.md](./MEMLAYER.md) before planning or making significant changes.

Minimum workflow for every meaningful task:

1. Read recent project context from MemLayer before coding, debugging, or deploying.
2. Reuse existing decisions, constraints, risks, and artifacts instead of reinventing them.
3. After meaningful work, write back:
   - decisions
   - constraints
   - risks
   - artifacts
   - task outcomes
4. If the local project structure changed, run a project reimport/update into MemLayer.

Use this local config first:

- [memlayer.config.json](./memlayer.config.json)
- [.env.memlayer.example](./.env.memlayer.example)

When in doubt, prefer the sandbox-friendly `http://127.0.0.1:18100` endpoint first.
Use `https://memlayer.loc/api` only as a human-friendly or escalated fallback when localhost is unavailable.
<!-- MEMLAYER_ROOT_PACK:END -->
