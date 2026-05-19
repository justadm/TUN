#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  bgp_policy_sync_resilient.sh --uplink <name:table:dev:nexthop> [--uplink ...]

Examples:
  bgp_policy_sync_resilient.sh \
    --uplink ams:301:trsrv-ams-spb:10.252.1.2 \
    --uplink nyc:302:trsrv-nyc-spb:10.252.3.2 \
    --uplink fra:304:trsrv-fra-spb:10.252.2.2

Behavior:
  - builds policy tables from routes in `table main proto bgp`
  - validates each uplink independently
  - skips unhealthy uplinks without touching their current table
  - never aborts the whole sync because one uplink is down
EOF
}

declare -a UPLINKS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --uplink)
      UPLINKS+=("${2:-}")
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown arg: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "${#UPLINKS[@]}" -eq 0 ]]; then
  echo "at least one --uplink is required" >&2
  exit 2
fi

mapfile -t prefixes < <(
  ip -4 route show table main proto bgp \
    | awk '{print $1}' \
    | grep -v '^default$' \
    | sort -u
)

uplink_ready() {
  local dev="$1" nh="$2"
  ip link show "$dev" >/dev/null 2>&1 || return 1
  ip route get "$nh" 2>/dev/null | grep -q "dev $dev" || return 1
  return 0
}

sync_table() {
  local name="$1" table="$2" dev="$3" nexthop="$4"
  local tmp

  if ! uplink_ready "$dev" "$nexthop"; then
    echo "skip_${name}=uplink_unavailable dev=${dev} nh=${nexthop}"
    echo "table_${table}=$(ip -4 route show table "$table" 2>/dev/null | wc -l | tr -d ' ')"
    return 0
  fi

  tmp="$(mktemp)"
  {
    echo "route flush table $table"
    for prefix in "${prefixes[@]}"; do
      echo "route replace table $table $prefix via $nexthop dev $dev"
    done
  } >"$tmp"

  ip -batch "$tmp"
  rm -f "$tmp"

  echo "synced_${name}=${#prefixes[@]}"
  echo "table_${table}=$(ip -4 route show table "$table" | wc -l | tr -d ' ')"
}

for uplink in "${UPLINKS[@]}"; do
  IFS=':' read -r name table dev nexthop <<<"$uplink"
  if [[ -z "${name:-}" || -z "${table:-}" || -z "${dev:-}" || -z "${nexthop:-}" ]]; then
    echo "invalid --uplink format: $uplink" >&2
    exit 2
  fi
  sync_table "$name" "$table" "$dev" "$nexthop"
done
