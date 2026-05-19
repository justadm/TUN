#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/check_vrn_peer_split.sh [options]

Checks current vrn split-routing health for three test peers:
  10.18.0.58 -> AMS (NotRU), RU -> local
  10.18.0.51 -> NYC (NotRU), RU -> local
  10.18.0.50 -> FRA (NotRU), RU -> local

What is validated:
  - tun-runtime services active on vrn/ams/nyc/fra
  - TUN interfaces/addresses exist
  - tunnel RTT probes
  - policy rules/tables on vrn
  - nft split markers on vrn
  - expected route decisions for RU and NotRU samples
  - return routes and NAT rules on ams/nyc/fra

Options:
  --vrn-host <host>      default: vrn
  --ams-host <host>      default: ams
  --nyc-host <host>      default: nyc
  --fra-host <host>      default: fra
  --out-dir <path>       default: ./artifacts/vrn-peer-split-check
  --strict-ssh           fail on ssh timeout/refused (default: warn)
  --ssh-retries <n>      default: 4
  --ssh-delay <sec>      default: 2
  --ssh-timeout <sec>    default: 20
  -h, --help             show this help
EOF
}

vrn_host="vrn"
ams_host="ams"
nyc_host="nyc"
fra_host="fra"
out_dir="./artifacts/vrn-peer-split-check"
strict_ssh=false
ssh_retries=4
ssh_delay=2
ssh_timeout=20
timeout_cmd=""
warn_count=0
fail_count=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --vrn-host) vrn_host="${2:-}"; shift 2 ;;
    --ams-host) ams_host="${2:-}"; shift 2 ;;
    --nyc-host) nyc_host="${2:-}"; shift 2 ;;
    --fra-host) fra_host="${2:-}"; shift 2 ;;
    --out-dir) out_dir="${2:-}"; shift 2 ;;
    --strict-ssh) strict_ssh=true; shift ;;
    --ssh-retries) ssh_retries="${2:-}"; shift 2 ;;
    --ssh-delay) ssh_delay="${2:-}"; shift 2 ;;
    --ssh-timeout) ssh_timeout="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if command -v timeout >/dev/null 2>&1; then
  timeout_cmd="timeout"
elif command -v gtimeout >/dev/null 2>&1; then
  timeout_cmd="gtimeout"
else
  timeout_cmd=""
fi

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

run_check() {
  local label="$1"
  local host="$2"
  local cmd="$3"
  local out=""
  local rc=0
  if out="$(ssh_try_capture "${host}" "${cmd}" 2>&1)"; then
    echo "[ok] ${label}"
    printf "%s\n" "${out}"
    return 0
  fi
  rc=$?
  if [[ -z "${out}" ]]; then
    out="ssh failure (${label}, rc=${rc})"
  fi
  if [[ "${strict_ssh}" == "false" && ( "${out}" == *"timed out"* || "${out}" == *"No route to host"* || "${out}" == *"Connection refused"* ) ]]; then
    warn_count=$((warn_count + 1))
    echo "[warn] ${label}: ${out}"
    return 0
  fi
  fail_count=$((fail_count + 1))
  echo "[fail] ${label}: ${out}"
  return 0
}

mkdir -p "${out_dir}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
report="${out_dir}/vrn-peer-split-check-${stamp}.txt"

{
  echo "[vrn-peer-split-check] started=${stamp}"
  echo "[vrn-peer-split-check] hosts vrn=${vrn_host} ams=${ams_host} nyc=${nyc_host} fra=${fra_host}"
  echo

  run_check "vrn services" "${vrn_host}" \
    "sudo -n systemctl is-active tun-runtime-server@ams tun-runtime-server@nyc tun-runtime-server@fra"
  run_check "ams service" "${ams_host}" "sudo -n systemctl is-active tun-runtime-client@vrn"
  run_check "nyc service" "${nyc_host}" "sudo -n systemctl is-active tun-runtime-client@vrn"
  run_check "fra service" "${fra_host}" "sudo -n systemctl is-active tun-runtime-client@vrn"
  echo

  run_check "vrn ifaces" "${vrn_host}" \
    "ip -4 addr show dev trsrv-ams-vrn; ip -4 addr show dev trsrv-nyc-vrn; ip -4 addr show dev trsrv-fra-vrn"
  run_check "ams iface" "${ams_host}" "ip -4 addr show dev trcli-vrn"
  run_check "nyc iface" "${nyc_host}" "ip -4 addr show dev trcli-vrn"
  run_check "fra iface" "${fra_host}" "ip -4 addr show dev trcli-vrn"
  echo

  run_check "vrn ping ams/fra/nyc" "${vrn_host}" \
    "ping -c 3 -W 2 10.253.1.2; ping -c 3 -W 2 10.253.2.2; ping -c 3 -W 2 10.253.3.2"
  echo

  run_check "vrn rules present" "${vrn_host}" \
    "ip rule show | grep -E '10010:|10011:|10012:|10013:'"
  run_check "vrn tables present" "${vrn_host}" \
    "ip route show table vrn-ru; ip route show table vrn-ams; ip route show table vrn-nyc; ip route show table vrn-fra"
  run_check "vrn nft split markers" "${vrn_host}" \
    "sudo -n nft list chain inet vrn prerouting | grep -E 'peer_ams|peer_nyc|peer_fra|ru4|ru6'"
  echo

  run_check "route decision 10.18.0.58" "${vrn_host}" \
    "ip -4 route get 5.255.255.77 mark 0x10 iif wg0 from 10.18.0.58; ip -4 route get 1.1.1.1 mark 0x11 iif wg0 from 10.18.0.58"
  run_check "route decision 10.18.0.51" "${vrn_host}" \
    "ip -4 route get 5.255.255.77 mark 0x10 iif wg0 from 10.18.0.51; ip -4 route get 1.1.1.1 mark 0x12 iif wg0 from 10.18.0.51"
  run_check "route decision 10.18.0.50" "${vrn_host}" \
    "ip -4 route get 5.255.255.77 mark 0x10 iif wg0 from 10.18.0.50; ip -4 route get 1.1.1.1 mark 0x13 iif wg0 from 10.18.0.50"
  echo

  run_check "ams return/nat" "${ams_host}" \
    "ip -4 route get 10.18.0.58; sudo -n iptables -t nat -S POSTROUTING | grep -E '10.18.0.0/24|10.253.1.0/30'"
  run_check "nyc return/nat" "${nyc_host}" \
    "ip -4 route get 10.18.0.51; sudo -n iptables -t nat -S POSTROUTING | grep -E '10.18.0.0/24|10.253.3.0/30'"
  run_check "fra return/nat" "${fra_host}" \
    "ip -4 route get 10.18.0.50; sudo -n iptables -t nat -S POSTROUTING | grep -E '10.18.0.0/24|10.253.2.0/30'"
  echo

  if (( fail_count > 0 )); then
    echo "[vrn-peer-split-check] FAILED fails=${fail_count} warns=${warn_count}"
  elif (( warn_count > 0 )); then
    echo "[vrn-peer-split-check] WARN warns=${warn_count}"
  else
    echo "[vrn-peer-split-check] PASS"
  fi
} > >(tee "${report}") 2>&1

echo "${report}"
if (( fail_count > 0 )); then
  exit 1
fi
exit 0
