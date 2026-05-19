#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/rollback_local_tun_client.sh [options]

Local rollback for a manual runtime-client session.

What it does:
  1) stops runtime-client process (by pid-file or by tun-name pattern)
  2) brings down/removes the specified TUN interface where possible
  3) removes only explicitly provided routes

Options:
  --tun-name <name>              required (e.g. utun8, tun-laptop-nyc)
  --pid-file <path>              optional pid file for runtime-client process
  --routes <csv>                 optional routes to delete (example: 10.10.0.0/16,default)
  --kill-timeout-sec <n>         graceful stop timeout (default: 5)
  --dry-run                      print commands only
  -h, --help

Examples:
  scripts/rollback_local_tun_client.sh --tun-name utun8
  scripts/rollback_local_tun_client.sh --tun-name tun-laptop-nyc --routes 10.255.10.0/30
EOF
}

tun_name=""
pid_file=""
routes_csv=""
kill_timeout_sec="5"
dry_run="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --tun-name) tun_name="${2:-}"; shift 2 ;;
    --pid-file) pid_file="${2:-}"; shift 2 ;;
    --routes) routes_csv="${2:-}"; shift 2 ;;
    --kill-timeout-sec) kill_timeout_sec="${2:-}"; shift 2 ;;
    --dry-run) dry_run="true"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ -z "${tun_name}" ]]; then
  echo "missing required --tun-name" >&2
  exit 2
fi

run() {
  if [[ "${dry_run}" == "true" ]]; then
    echo "+ $*"
    return 0
  fi
  "$@"
}

run_sh() {
  if [[ "${dry_run}" == "true" ]]; then
    echo "+ sh -c $1"
    return 0
  fi
  sh -c "$1"
}

kill_pid_graceful() {
  local pid="$1"
  if ! kill -0 "${pid}" >/dev/null 2>&1; then
    return 0
  fi
  run kill "${pid}" || true
  local i
  for i in $(seq 1 "${kill_timeout_sec}"); do
    if ! kill -0 "${pid}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  run kill -9 "${pid}" || true
}

stop_runtime_client() {
  if [[ -n "${pid_file}" && -f "${pid_file}" ]]; then
    local pid
    pid="$(tr -d '[:space:]' < "${pid_file}")"
    if [[ -n "${pid}" ]]; then
      echo "[rollback] stop by pid-file: ${pid_file} (pid=${pid})"
      kill_pid_graceful "${pid}"
    fi
  fi

  # Best-effort fallback: only processes that include both runtime-client and tun-name.
  local pids
  pids="$(pgrep -f "runtime-client.*${tun_name}" || true)"
  if [[ -n "${pids}" ]]; then
    echo "[rollback] stop by pattern runtime-client.*${tun_name}: ${pids//$'\n'/ }"
    local p
    for p in ${pids}; do
      kill_pid_graceful "${p}"
    done
  fi
}

delete_routes_linux() {
  local routes="$1"
  local route
  IFS=',' read -r -a route_arr <<< "${routes}"
  for route in "${route_arr[@]}"; do
    route="$(echo "${route}" | xargs)"
    [[ -z "${route}" ]] && continue
    if [[ "${route}" == "default" ]]; then
      run sudo ip route del default dev "${tun_name}" || true
    else
      run sudo ip route del "${route}" dev "${tun_name}" || true
    fi
  done
}

delete_routes_darwin() {
  local routes="$1"
  local route
  IFS=',' read -r -a route_arr <<< "${routes}"
  for route in "${route_arr[@]}"; do
    route="$(echo "${route}" | xargs)"
    [[ -z "${route}" ]] && continue
    if [[ "${route}" == "default" ]]; then
      # Best-effort scoped default deletion for this interface only.
      run_sh "sudo route -n delete default -ifscope ${tun_name} >/dev/null 2>&1 || true"
    else
      run_sh "sudo route -n delete -net ${route} -ifscope ${tun_name} >/dev/null 2>&1 || true"
    fi
  done
}

cleanup_tun_linux() {
  run sudo ip link set dev "${tun_name}" down || true
  run sudo ip tuntap del dev "${tun_name}" mode tun || true
}

cleanup_tun_darwin() {
  # utun interfaces are often auto-destroyed when process exits; keep best-effort.
  run_sh "sudo ifconfig ${tun_name} down >/dev/null 2>&1 || true"
  run_sh "sudo ifconfig ${tun_name} destroy >/dev/null 2>&1 || true"
}

echo "[rollback] start tun=${tun_name} dry_run=${dry_run}"
stop_runtime_client

uname_s="$(uname -s)"
case "${uname_s}" in
  Linux)
    if [[ -n "${routes_csv}" ]]; then
      delete_routes_linux "${routes_csv}"
    fi
    cleanup_tun_linux
    ;;
  Darwin)
    if [[ -n "${routes_csv}" ]]; then
      delete_routes_darwin "${routes_csv}"
    fi
    cleanup_tun_darwin
    ;;
  *)
    echo "[rollback] unsupported OS: ${uname_s} (process stop completed; interface cleanup skipped)" >&2
    ;;
esac

echo "[rollback] done"
