#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/check_antifilter_bgp_frr.sh --host <ssh-host> [--neighbor <ipv4>]
EOF
}

HOST=""
NEIGHBOR="45.154.73.71"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) HOST="${2:-}"; shift 2 ;;
    --neighbor) NEIGHBOR="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ -n "${HOST}" ]] || { echo "--host is required" >&2; exit 2; }

echo "[check] host=${HOST} neighbor=${NEIGHBOR}"
ssh "${HOST}" "sudo systemctl is-active frr"
ssh "${HOST}" "sudo vtysh -c 'show bgp ipv4 unicast summary'"
ssh "${HOST}" "sudo vtysh -c 'show bgp neighbor ${NEIGHBOR}' | sed -n '1,160p'"
