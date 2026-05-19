#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  /usr/local/sbin/tun-runtime-link-watchdog.sh --role <client|server> --instance <name>

Reads /etc/tun/runtime-<role>-<instance>.env and validates:
  - systemd unit is active
  - health endpoint responds
  - readiness endpoint reports true
  - optional route guard (if configured by env keys)

Optional env keys in runtime-* env:
  TUN_WATCHDOG_ROUTE_TABLE=<table-id-or-name>
  TUN_WATCHDOG_EXPECT_DEV=<ifname>
  TUN_WATCHDOG_EXPECT_GW=<ip>
EOF
}

role=""
instance=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --role) role="${2:-}"; shift 2 ;;
    --instance) instance="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ "${role}" != "client" && "${role}" != "server" ]]; then
  echo "invalid --role, expected client|server" >&2
  exit 2
fi
if [[ -z "${instance}" ]]; then
  echo "missing --instance" >&2
  exit 2
fi

unit="tun-runtime-${role}@${instance}.service"
env_file="/etc/tun/runtime-${role}-${instance}.env"

if [[ ! -f "${env_file}" ]]; then
  # The timer can stay enabled across contour cutovers; missing env is a normal no-op.
  exit 0
fi

# shellcheck disable=SC1090
source "${env_file}"

health_addr="${TUN_HEALTH_ADDR:-}"
route_table="${TUN_WATCHDOG_ROUTE_TABLE:-}"
expect_dev="${TUN_WATCHDOG_EXPECT_DEV:-}"
expect_gw="${TUN_WATCHDOG_EXPECT_GW:-}"

restart_unit() {
  echo "[watchdog] restarting ${unit}" >&2
  systemctl restart "${unit}"
}

if ! systemctl is-active --quiet "${unit}"; then
  restart_unit
  exit 0
fi

if [[ -z "${health_addr}" ]]; then
  echo "[watchdog] ${unit}: no TUN_HEALTH_ADDR in ${env_file}; skip health checks" >&2
  exit 0
fi

if ! curl -fsS --max-time 2 "http://${health_addr}/live" >/dev/null 2>&1; then
  echo "[watchdog] ${unit}: live endpoint unavailable (${health_addr})" >&2
  restart_unit
  exit 0
fi

ready_payload="$(curl -fsS --max-time 2 "http://${health_addr}/ready" 2>/dev/null || true)"
status_payload="$(curl -fsS --max-time 2 "http://${health_addr}/status" 2>/dev/null || true)"

ready_ok=0
if printf '%s' "${ready_payload}" | grep -qiE '(^|[^a-zA-Z])true([^a-zA-Z]|$)|"ready"[[:space:]]*:[[:space:]]*true'; then
  ready_ok=1
fi

if [[ "${ready_ok}" -ne 1 ]]; then
  echo "[watchdog] ${unit}: ready check failed; ready=${ready_payload:-<empty>} status=${status_payload:-<empty>}" >&2
  restart_unit
  exit 0
fi

if [[ -n "${route_table}" && -n "${expect_dev}" && -n "${expect_gw}" ]]; then
  if ! ip route show table "${route_table}" 2>/dev/null | grep -q "default via ${expect_gw} dev ${expect_dev}"; then
    echo "[watchdog] ${unit}: route guard failed table=${route_table} gw=${expect_gw} dev=${expect_dev}" >&2
    restart_unit
    exit 0
  fi
fi

echo "[watchdog] ${unit}: ok" >&2
exit 0
