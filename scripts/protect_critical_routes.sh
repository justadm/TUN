#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/protect_critical_routes.sh on [host_or_ip ...]
  scripts/protect_critical_routes.sh off [host_or_ip ...]
  scripts/protect_critical_routes.sh status [host_or_ip ...]
  scripts/protect_critical_routes.sh list

What it does:
  - adds or removes host routes for critical servers outside VPN/tunnel paths
  - never changes default route
  - resolves SSH host aliases via `ssh -G <host>`

Examples:
  scripts/protect_critical_routes.sh on
  scripts/protect_critical_routes.sh on fra ams spb
  scripts/protect_critical_routes.sh status fra 103.110.65.30
  scripts/protect_critical_routes.sh off
EOF
}

DEFAULT_TARGETS=(
  bx_msk_d
  nyc
  fra
  ams
  msk
  spb
  vrn
  edg
  exe
)

is_ipv4() {
  [[ "${1:-}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]
}

resolve_target_ip() {
  local target="${1:-}"
  if is_ipv4 "${target}"; then
    printf '%s\n' "${target}"
    return 0
  fi

  local host
  host="$(ssh -G "${target}" 2>/dev/null | awk '/^hostname / {print $2; exit}')"
  if [[ -z "${host}" ]]; then
    echo "cannot resolve target: ${target}" >&2
    return 1
  fi
  printf '%s\n' "${host}"
}

detect_gateway() {
  local gw
  gw="$(netstat -rn -f inet | awk '$1=="default" && $4 !~ /^utun/ {print $2; exit}')"
  if [[ -z "${gw}" ]]; then
    gw="$(route -n get default 2>/dev/null | awk '/gateway:/ {print $2; exit}')"
  fi
  printf '%s\n' "${gw}"
}

targets_from_args() {
  if [[ $# -gt 0 ]]; then
    printf '%s\n' "$@"
    return 0
  fi
  printf '%s\n' "${DEFAULT_TARGETS[@]}"
}

print_status() {
  local target="$1"
  local ip="$2"
  echo "[status] ${target} -> ${ip}"
  route -n get "${ip}" 2>/dev/null || true
}

main() {
  local mode="${1:-}"
  case "${mode}" in
    on|off|status|list) ;;
    -h|--help|"") usage; exit 0 ;;
    *) echo "unknown mode: ${mode}" >&2; usage >&2; exit 2 ;;
  esac
  shift || true

  if [[ "${mode}" == "list" ]]; then
    printf '%s\n' "${DEFAULT_TARGETS[@]}"
    exit 0
  fi

  local gw=""
  if [[ "${mode}" == "on" ]]; then
    gw="$(detect_gateway)"
    if [[ -z "${gw}" ]]; then
      echo "failed to detect non-tunnel default gateway" >&2
      exit 1
    fi
    echo "[gateway] ${gw}"
  fi

  local target ip
  while IFS= read -r target; do
    [[ -n "${target}" ]] || continue
    ip="$(resolve_target_ip "${target}")"
    case "${mode}" in
      on)
        echo "[protect] ${target} -> ${ip} via ${gw}"
        sudo route -n change -host "${ip}" "${gw}" 2>/dev/null || sudo route -n add -host "${ip}" "${gw}"
        ;;
      off)
        echo "[unprotect] ${target} -> ${ip}"
        sudo route -n delete -host "${ip}" 2>/dev/null || true
        ;;
      status)
        print_status "${target}" "${ip}"
        ;;
    esac
  done < <(targets_from_args "$@")
}

main "$@"
