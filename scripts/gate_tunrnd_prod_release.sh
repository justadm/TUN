#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/gate_tunrnd_prod_release.sh [options]

Strict release gate wrapper over unified prod mesh gate.
Defaults are tuned to reduce false negatives from direct SSH path instability:
  - strict SSH failures enabled
  - fra/nyc checks routed via ams jump-host
  - increased SSH retries

Options:
  --out-dir <path>      default: ./artifacts/tunrnd-prod-release-gate
  --edg-host <host>     default: edg
  --fra-via <host>      default: ams
  --nyc-via <host>      default: ams
  --edg-via <host>      default: empty
  --ssh-retries <n>     default: 6
  --ssh-delay <sec>     default: 3
  --ssh-timeout <sec>   default: 20
  --help                show this help
EOF
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out_dir="./artifacts/tunrnd-prod-release-gate"
edg_host="edg"
fra_via="ams"
nyc_via="ams"
edg_via=""
ssh_retries=6
ssh_delay=3
ssh_timeout=20

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir) out_dir="${2:-}"; shift 2 ;;
    --edg-host) edg_host="${2:-}"; shift 2 ;;
    --fra-via) fra_via="${2:-}"; shift 2 ;;
    --nyc-via) nyc_via="${2:-}"; shift 2 ;;
    --edg-via) edg_via="${2:-}"; shift 2 ;;
    --ssh-retries) ssh_retries="${2:-}"; shift 2 ;;
    --ssh-delay) ssh_delay="${2:-}"; shift 2 ;;
    --ssh-timeout) ssh_timeout="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

"${script_dir}/gate_tunrnd_prod_mesh.sh" \
  --out-dir "${out_dir}" \
  --edg-host "${edg_host}" \
  --strict-ssh \
  --fra-via "${fra_via}" \
  --nyc-via "${nyc_via}" \
  --edg-via "${edg_via}" \
  --ssh-retries "${ssh_retries}" \
  --ssh-delay "${ssh_delay}" \
  --ssh-timeout "${ssh_timeout}"
