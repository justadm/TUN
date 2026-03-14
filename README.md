# TUN

Custom VPN protocol research and implementation in Go. Focus is on a small, auditable core protocol, with transport pluggability and DPI-resilient transports handled as separate layers.

## Status

Early prototype. Core framing, AEAD, replay window, and initial handshake scaffolding are in progress.

## Repository layout

- `cmd/client`: client entrypoint (stub)
- `cmd/server`: server entrypoint (stub)
- `internal/core`: protocol core (frames, replay, AEAD, handshake)
- `internal/transport`: transport interfaces and implementations
- `internal/tun`: TUN interface abstraction
- `.docs/`: specs, notes, and planning

## Local development

Requirements:

- Go 1.22+ (1.25 installed locally)

Commands:

```bash
go test ./...
```

## Notes

Specs and planning live under `.docs/spec/`. The project starts with a TLS stream transport and a Noise-style handshake with server key pinning.
