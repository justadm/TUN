#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/gate_tunrnd_edg_mesh.sh [options]

Runs final SRE gate checks for persistent edg mesh deployment:
  edg <-> ams
  edg <-> fra
  edg <-> nyc

Writes gate evidence into artifacts directory.

Options:
  --out-dir <path>      default: ./artifacts/tunrnd-edg-mesh-gate
  --edg-host <host>     default: edg
  --ams-host <host>     default: ams
  --fra-host <host>     default: fra
  --nyc-host <host>     default: nyc
  --fra-via <host>      default: empty
  --nyc-via <host>      default: empty
  --edg-via <host>      default: empty
  --ssh-retries <n>     default: 4
  --ssh-delay <sec>     default: 3
  --ssh-timeout <sec>   default: 20
  --strict-ssh          fail gate on ssh timeout (default: warn and continue)
  --help                show this help
EOF
}

out_dir="./artifacts/tunrnd-edg-mesh-gate"
edg_host="edg"
ams_host="ams"
fra_host="fra"
nyc_host="nyc"
fra_via=""
nyc_via=""
edg_via=""
ssh_retries=4
ssh_delay=3
ssh_timeout=20
strict_ssh=false
warn_count=0
fail_count=0
timeout_cmd=""

ssh_via_for_host() {
  local host="$1"
  if [[ "${host}" == "${fra_host}" ]]; then
    printf "%s\n" "${fra_via}"
    return 0
  fi
  if [[ "${host}" == "${nyc_host}" ]]; then
    printf "%s\n" "${nyc_via}"
    return 0
  fi
  if [[ "${host}" == "${edg_host}" ]]; then
    printf "%s\n" "${edg_via}"
    return 0
  fi
  printf "%s\n" ""
}

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

ssh_try_capture() {
  local host="$1"
  local cmd="$2"
  local tries=0
  local max_tries="${ssh_retries}"
  local delay="${ssh_delay}"
  local via=""
  local -a ssh_cmd=(
    ssh
    -o BatchMode=yes
    -o LogLevel=ERROR
    -o ConnectTimeout="${ssh_timeout}"
    -o ServerAliveInterval=5
    -o ServerAliveCountMax=1
    -o ConnectionAttempts=1
  )
  local cmd_timeout=$((ssh_timeout + 10))
  local out=""
  local rc=0
  via="$(ssh_via_for_host "${host}")"
  if [[ -n "${via}" ]]; then
    ssh_cmd+=(-J "${via}")
  fi
  while (( tries < max_tries )); do
    out="$(run_with_timeout "${cmd_timeout}" "${ssh_cmd[@]}" "${host}" "${cmd}" 2>&1)" && rc=0 || rc=$?
    if (( rc == 0 )); then
      printf "%s\n" "${out}"
      return 0
    fi
    tries=$((tries + 1))
    if (( tries >= max_tries )); then
      break
    fi
    sleep "${delay}"
  done
  printf "%s\n" "${out}" >&2
  return "${rc}"
}

run_ssh_check() {
  local label="$1"
  local host="$2"
  local cmd="$3"
  local out=""
  local rc=0
  if out="$(ssh_try_capture "${host}" "${cmd}" 2>&1)"; then
    printf "%s\n" "${out}"
    return 0
  else
    rc=$?
  fi
  if [[ -z "${out}" && "${rc}" -eq 124 ]]; then
    out="ssh command timeout (${label}, host=${host})"
  elif [[ -z "${out}" ]]; then
    out="ssh check failed (${label}, host=${host}, rc=${rc})"
  fi
  if [[ "${strict_ssh}" == "false" && ( "${out}" == *"Operation timed out"* || "${out}" == *"Connection timed out"* || "${out}" == *"No route to host"* || "${out}" == *"Connection refused"* ) ]]; then
    warn_count=$((warn_count + 1))
    echo "[warn] ${label}: ssh timeout after retries"
    return 0
  fi
  echo "[error] ${label}: ssh failure rc=${rc}" >&2
  echo "${out}" >&2
  return 1
}

run_gate_check() {
  local label="$1"
  local host="$2"
  local cmd="$3"
  if run_ssh_check "${label}" "${host}" "${cmd}"; then
    return 0
  fi
  fail_count=$((fail_count + 1))
  return 0
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir) out_dir="${2:-}"; shift 2 ;;
    --edg-host) edg_host="${2:-}"; shift 2 ;;
    --ams-host) ams_host="${2:-}"; shift 2 ;;
    --fra-host) fra_host="${2:-}"; shift 2 ;;
    --nyc-host) nyc_host="${2:-}"; shift 2 ;;
    --fra-via) fra_via="${2:-}"; shift 2 ;;
    --nyc-via) nyc_via="${2:-}"; shift 2 ;;
    --edg-via) edg_via="${2:-}"; shift 2 ;;
    --ssh-retries) ssh_retries="${2:-}"; shift 2 ;;
    --ssh-delay) ssh_delay="${2:-}"; shift 2 ;;
    --ssh-timeout) ssh_timeout="${2:-}"; shift 2 ;;
    --strict-ssh) strict_ssh=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

init_timeout_cmd

mkdir -p "${out_dir}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
report="${out_dir}/edg-mesh-gate-${stamp}.txt"

{
  echo "[edg-mesh-gate] started at ${stamp}"
  echo "[edg-mesh-gate] strict_ssh=${strict_ssh}"
  echo "[edg-mesh-gate] fra_via=${fra_via:-none} nyc_via=${nyc_via:-none} edg_via=${edg_via:-none} ssh_retries=${ssh_retries} ssh_timeout=${ssh_timeout}"
  echo
  echo "== edg services =="
  run_gate_check "edg services" "${edg_host}" "sudo -n systemctl --no-pager --full status tun-runtime-server@ams.service tun-runtime-server@fra.service tun-runtime-server@nyc.service"
  echo
  echo "== client services =="
  run_gate_check "ams client service" "${ams_host}" "sudo -n systemctl --no-pager --full status tun-runtime-client@edg.service"
  run_gate_check "fra client service" "${fra_host}" "sudo -n systemctl --no-pager --full status tun-runtime-client@edg.service"
  run_gate_check "nyc client service" "${nyc_host}" "sudo -n systemctl --no-pager --full status tun-runtime-client@edg.service"
  echo
  echo "== interface addresses =="
  run_gate_check "edg interfaces" "${edg_host}" "sudo -n ip -brief addr show | grep -E 'trsrv-ams-edg|trsrv-fra-edg|trsrv-nyc-edg'"
  run_gate_check "ams interface" "${ams_host}" "sudo -n ip -brief addr show trcli-edg"
  run_gate_check "fra interface" "${fra_host}" "sudo -n ip -brief addr show trcli-edg"
  run_gate_check "nyc interface" "${nyc_host}" "sudo -n ip -brief addr show trcli-edg"
  echo
  echo "== end-to-end pings =="
  run_gate_check "ams ping" "${ams_host}" "sudo -n ping -I trcli-edg -c 3 -W 2 10.254.1.1"
  run_gate_check "fra ping" "${fra_host}" "sudo -n ping -I trcli-edg -c 3 -W 2 10.254.2.1"
  run_gate_check "nyc ping" "${nyc_host}" "sudo -n ping -I trcli-edg -c 3 -W 2 10.254.3.1"
  echo
  echo "== firewall checks on edg =="
  run_gate_check "edg ufw rules" "${edg_host}" "sudo -n ufw status numbered | grep -E '18653|18654|18655|tun-edg' || true"
  run_gate_check "edg nft rules" "${edg_host}" "sudo -n nft list chain inet filter input | grep -E '18653|18654|18655|tun-edg' || true"
  run_gate_check "edg nft service" "${edg_host}" "sudo -n systemctl --no-pager --full status tun-runtime-nft-reload.service"
  run_gate_check "edg nft policy file" "${edg_host}" "sudo -n ls -l /etc/tun/nft-runtime-ingress.conf && sudo -n sed -n '1,80p' /etc/tun/nft-runtime-ingress.conf"
  echo
  if (( fail_count > 0 )); then
    echo "[edg-mesh-gate] FAILED: ${fail_count} check(s) failed, warnings=${warn_count}"
  elif (( warn_count > 0 )); then
    echo "[edg-mesh-gate] WARN: completed with ${warn_count} warning(s)"
  else
    echo "[edg-mesh-gate] passed"
  fi
} > >(tee "${report}") 2>&1

echo "${report}"
if (( fail_count > 0 )); then
  exit 1
fi
