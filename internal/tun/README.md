# tun

TUN device integration.

Current adapter status:

- Linux: real `/dev/net/tun` adapter via `Open(...)` with optional MTU, automatic `IFF_UP`, address/route apply (`replace|add`) via `iproute2`, and optional cleanup on close
- macOS (darwin): real `utun` adapter via `Open(...)`, with optional MTU, interface up, address/route apply via `ifconfig/route`, and optional cleanup on close
- Other platforms: explicit `ErrUnsupportedPlatform` fallback

Preflight:

- Linux: `Preflight(...)` validates privileges (`root` or `CAP_NET_ADMIN`), `/dev/net/tun` access, and `iproute2` presence when address/route config is enabled
- Linux: `BuildPreflightReport(...)` returns structured check results for automation and `ExecStartPre` integration
- macOS: `Preflight(...)` validates options, `utun` name format (if provided), and `ifconfig/route` availability when needed
- other platforms: `Preflight(...)` returns `ErrUnsupportedPlatform`
