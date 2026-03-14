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

## Quick local test (dev keys)

This generates a dev X25519 static keypair and a self-signed TLS cert in `.dev/`:

```bash
./scripts/gen_dev_keys.sh
```

Run the server:

```bash
go run ./cmd/server \
  -addr :8443 \
  -cert ./.dev/tls_cert.pem \
  -key ./.dev/tls_key.pem \
  -server-id 00112233445566778899aabbccddeeff \
  -server-static-priv "$(cat ./.dev/server_static_priv.b64)"
```

Run the client:

```bash
go run ./cmd/client \
  -addr 127.0.0.1:8443 \
  -server-name localhost \
  -insecure true \
  -client-id 0102030405060708090a0b0c0d0e0f10 \
  -server-static-pub "$(cat ./.dev/server_static_pub.b64)"
```

## Notes

Specs and planning live under `.docs/spec/`. The project starts with a TLS stream transport and a Noise-style handshake with server key pinning.
