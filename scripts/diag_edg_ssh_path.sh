#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/diag_edg_ssh_path.sh [options]

Diagnose SSH reachability/auth/session path for edg-like hosts.

Options:
  --target <host>         default: edg
  --target-ip <ip>        default: 85.239.44.100
  --target-port <port>    default: 65022
  --target-user <user>    default: opsadmin
  --jump <host>           default: ams
  --timeout <sec>         default: 8
  --out-dir <path>        default: ./artifacts/tunrnd-prod-release-validation/ssh-diag
  --help                  show this help
EOF
}

target_host="edg"
target_ip="85.239.44.100"
target_port="65022"
target_user="opsadmin"
jump_host="ams"
timeout_sec=8
out_dir="./artifacts/tunrnd-prod-release-validation/ssh-diag"
timeout_cmd=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target) target_host="${2:-}"; shift 2 ;;
    --target-ip) target_ip="${2:-}"; shift 2 ;;
    --target-port) target_port="${2:-}"; shift 2 ;;
    --target-user) target_user="${2:-}"; shift 2 ;;
    --jump) jump_host="${2:-}"; shift 2 ;;
    --timeout) timeout_sec="${2:-}"; shift 2 ;;
    --out-dir) out_dir="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

init_timeout_cmd() {
  if command -v timeout >/dev/null 2>&1; then
    timeout_cmd="timeout"
  elif command -v gtimeout >/dev/null 2>&1; then
    timeout_cmd="gtimeout"
  else
    timeout_cmd=""
  fi
}

run_with_timeout() {
  local seconds="$1"
  shift
  if [[ -n "${timeout_cmd}" ]]; then
    "${timeout_cmd}" "${seconds}" "$@"
    return $?
  fi
  "$@" &
  local cmd_pid=$!
  local deadline=$(( $(date +%s) + seconds ))
  while kill -0 "${cmd_pid}" >/dev/null 2>&1; do
    if (( $(date +%s) >= deadline )); then
      kill "${cmd_pid}" >/dev/null 2>&1 || true
      sleep 1
      kill -9 "${cmd_pid}" >/dev/null 2>&1 || true
      wait "${cmd_pid}" >/dev/null 2>&1 || true
      return 124
    fi
    sleep 1
  done
  wait "${cmd_pid}"
}

probe() {
  local name="$1"
  shift
  local out=""
  local rc=0
  if out="$(run_with_timeout $((timeout_sec + 6)) "$@" 2>&1)"; then
    rc=0
  else
    rc=$?
  fi
  {
    echo "== ${name} =="
    echo "rc=${rc}"
    if [[ -n "${out}" ]]; then
      printf "%s\n" "${out}"
    else
      echo "(no output)"
    fi
    echo
  } >> "${report}"
}

mkdir -p "${out_dir}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
report="${out_dir}/edg-ssh-diag-${stamp}.log"

init_timeout_cmd

{
  echo "[edg-ssh-diag] started=${stamp}"
  echo "[edg-ssh-diag] target_host=${target_host} target_ip=${target_ip} target_port=${target_port} target_user=${target_user} jump=${jump_host} timeout=${timeout_sec}s"
  echo
} > "${report}"

probe "tcp-local-target" nc -zv -w "${timeout_sec}" "${target_ip}" "${target_port}"
probe "ssh-auth-none-direct" ssh -vv -o BatchMode=yes -o ConnectTimeout="${timeout_sec}" -o ConnectionAttempts=1 -o PreferredAuthentications=none "${target_host}" "true"
probe "ssh-cmd-direct" ssh -vv -o BatchMode=yes -o ConnectTimeout="${timeout_sec}" -o ConnectionAttempts=1 -o IdentitiesOnly=yes -i ~/.ssh/id_ed25519 "${target_host}" "true"
probe "tcp-from-jump-public" ssh -o BatchMode=yes -o ConnectTimeout="${timeout_sec}" "${jump_host}" "nc -zv -w ${timeout_sec} ${target_ip} ${target_port}"
probe "tcp-from-jump-private" ssh -o BatchMode=yes -o ConnectTimeout="${timeout_sec}" "${jump_host}" "nc -zv -w ${timeout_sec} 10.200.0.4 ${target_port}"
probe "ssh-cmd-proxyjump" ssh -vv -J "${jump_host}" -o BatchMode=yes -o ConnectTimeout="${timeout_sec}" -o ConnectionAttempts=1 -o IdentitiesOnly=yes -i ~/.ssh/id_ed25519 "${target_host}" "true"

echo "${report}"

