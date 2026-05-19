#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="${ROOT}/.tmp"
BIN_DIR="${TMP_DIR}/bin"
PID_FILE="${TMP_DIR}/nyc-laptop-runtime-client.pid"
LOG_FILE="${TMP_DIR}/nyc-laptop-runtime-client.log"
TUN_NAME_FILE="${TMP_DIR}/nyc-laptop-tun.name"
ROUTE_SNAPSHOT_FILE="${TMP_DIR}/nyc-laptop-default-route.snapshot"
ROUTE_PIN_FILE="${TMP_DIR}/nyc-laptop-route-pin.snapshot"
CLIENT_BIN="${BIN_DIR}/runtime-client"
PREFLIGHT_BIN="${BIN_DIR}/runtime-preflight"

SERVER_ADDR="${NYC_SERVER_ADDR:-108.165.154.213:18665}"
SERVER_NAME="${NYC_SERVER_NAME:-nyc.tun.local}"
CLIENT_ID="${NYC_CLIENT_ID:-a1b2c3d4e5f60718293a4b5c6d7e8f90}"
SERVER_STATIC_PUB="${NYC_SERVER_STATIC_PUB:-owhpNIEMmyNEVU6wsnR/JU+T9vT6KVYQZyiMuHF+NRg=}"
TUN_MTU="${NYC_TUN_MTU:-1420}"
TUN_ADDRESSES="${NYC_TUN_ADDRESSES:-10.255.10.2/30}"
TUN_ROUTES="${NYC_TUN_ROUTES:-}"
HEALTH_ADDR="${NYC_HEALTH_ADDR:-127.0.0.1:19265}"
PUBLIC_IP_URL="${NYC_PUBLIC_IP_URL:-https://ifconfig.me/ip}"
FORCE_REBUILD="${FORCE_REBUILD:-false}"
ALLOW_COMPETING_DEFAULT_TUN="${NYC_ALLOW_COMPETING_DEFAULT_TUN:-false}"
ENABLE_DARWIN_FULL_EGRESS_EXPERIMENT="${NYC_ENABLE_DARWIN_FULL_EGRESS_EXPERIMENT:-false}"

os_name="$(uname -s)"
case "${os_name}" in
  Darwin|Linux) ;;
  *)
    echo "[ERR] unsupported OS: ${os_name}. Expected Darwin or Linux." >&2
    exit 2
    ;;
esac

if [[ "${os_name}" == "Darwin" ]]; then
  TUN_NAME_DEFAULT="${NYC_TUN_NAME_DARWIN:-}"
else
  TUN_NAME_DEFAULT="${NYC_TUN_NAME_LINUX:-tun-laptop-nyc}"
fi
TUN_NAME="${NYC_TUN_NAME:-${TUN_NAME_DEFAULT}}"

usage() {
  cat <<EOF
Usage:
  scripts/nyc_laptop_local.sh up|down|status|logs

Environment (optional):
  NYC_SERVER_ADDR          default: ${SERVER_ADDR}
  NYC_SERVER_NAME          default: ${SERVER_NAME}
  NYC_CLIENT_ID            default: ${CLIENT_ID}
  NYC_SERVER_STATIC_PUB    default: ${SERVER_STATIC_PUB}
  NYC_TUN_NAME             default: ${TUN_NAME:-<auto>}
  NYC_TUN_MTU              default: ${TUN_MTU}
  NYC_TUN_ADDRESSES        default: ${TUN_ADDRESSES}
  NYC_TUN_ROUTES           default: ${TUN_ROUTES:-<empty>}
  NYC_HEALTH_ADDR          default: ${HEALTH_ADDR}
  NYC_PUBLIC_IP_URL        default: ${PUBLIC_IP_URL}
  FORCE_REBUILD=true|false default: ${FORCE_REBUILD}
  NYC_ENABLE_DARWIN_FULL_EGRESS_EXPERIMENT=true|false default: ${ENABLE_DARWIN_FULL_EGRESS_EXPERIMENT}
EOF
}

require_go() {
  if ! command -v go >/dev/null 2>&1; then
    echo "[ERR] go not found in PATH" >&2
    exit 1
  fi
}

build_bins() {
  require_go
  mkdir -p "${BIN_DIR}" "${TMP_DIR}"
  local rebuild="false"
  if [[ "${FORCE_REBUILD}" == "true" || ! -x "${CLIENT_BIN}" || ! -x "${PREFLIGHT_BIN}" ]]; then
    rebuild="true"
  elif find "${ROOT}/cmd/runtime-client" "${ROOT}/cmd/runtime-preflight" "${ROOT}/internal/runtime" "${ROOT}/internal/tun" \
      -type f -name '*.go' -newer "${CLIENT_BIN}" | grep -q .; then
    rebuild="true"
  fi
  if [[ "${rebuild}" == "true" ]]; then
    (cd "${ROOT}" && go build -o "${CLIENT_BIN}" ./cmd/runtime-client)
    (cd "${ROOT}" && go build -o "${PREFLIGHT_BIN}" ./cmd/runtime-preflight)
  fi
}

is_running() {
  if [[ ! -f "${PID_FILE}" ]]; then
    return 1
  fi
  local pid
  pid="$(tr -d '[:space:]' < "${PID_FILE}" || true)"
  [[ -n "${pid}" ]] || return 1
  kill -0 "${pid}" >/dev/null 2>&1
}

preflight() {
  local effective_routes
  effective_routes="$(runtime_tun_routes)"
  local args=(
    -tun-mtu "${TUN_MTU}"
    -tun-addresses "${TUN_ADDRESSES}"
    -tun-routes "${effective_routes}"
    -tun-config-mode "replace"
    -tun-cleanup-on-close "true"
  )
  if [[ -n "${TUN_NAME}" ]]; then
    args+=(-tun-name "${TUN_NAME}")
  fi
  echo "[INFO] preflight (${os_name})"
  sudo "${PREFLIGHT_BIN}" "${args[@]}"
}

detect_tun_name() {
  local detected
  detected="$(grep -Eo 'tun opened name=[^ ]+' "${LOG_FILE}" | tail -n1 | sed 's/tun opened name=//' || true)"
  if [[ -n "${detected}" ]]; then
    echo "${detected}" > "${TUN_NAME_FILE}"
    return
  fi
  if [[ -n "${TUN_NAME}" ]]; then
    echo "${TUN_NAME}" > "${TUN_NAME_FILE}"
  fi
}

probe_health_summary() {
  local out_status out_live out_ready
  out_status="$(curl -sS --max-time 1 "http://${HEALTH_ADDR}/status" 2>&1 || true)"
  out_live="$(curl -sS --max-time 1 "http://${HEALTH_ADDR}/live" 2>&1 || true)"
  out_ready="$(curl -sS --max-time 1 "http://${HEALTH_ADDR}/ready" 2>&1 || true)"

  if [[ -n "${out_status}" ]] || [[ -n "${out_live}" ]] || [[ -n "${out_ready}" ]]; then
    printf 'status=%s;live=%s;ready=%s' "${out_status:-n/a}" "${out_live:-n/a}" "${out_ready:-n/a}"
    return 0
  fi
  return 1
}

wait_for_health() {
  local tries=10
  local delay="0.5"
  local summary
  for _ in $(seq 1 "${tries}"); do
    summary="$(probe_health_summary || true)"
    if [[ -n "${summary}" && "${summary}" != *"Failed to connect"* && "${summary}" != *"Connection refused"* ]]; then
      return 0
    fi
    sleep "${delay}"
  done
  return 1
}

last_runtime_state() {
  if [[ ! -f "${LOG_FILE}" ]]; then
    echo "-"
    return
  fi
  local line
  line="$(grep -Eo 'state=[a-z_]+' "${LOG_FILE}" | tail -n1 || true)"
  if [[ -z "${line}" ]]; then
    echo "-"
    return
  fi
  echo "${line#state=}"
}

connection_ready() {
  local st
  st="$(last_runtime_state)"
  [[ "${st}" == "established" ]]
}

probe_public_ip() {
  local iface="${1:-}"
  local endpoints=(
    "${PUBLIC_IP_URL}"
    "https://api.ipify.org"
    "https://ifconfig.co/ip"
  )
  local ep out
  for ep in "${endpoints[@]}"; do
    local args=(-sS --max-time 4 "${ep}")
    if [[ -n "${iface}" && "${iface}" != "-" ]]; then
      args=(--interface "${iface}" "${args[@]}")
    fi
    out="$(curl "${args[@]}" 2>&1 || true)"
    out="$(echo "${out}" | tr -d '\r' | tr '\n' ' ' | sed 's/[[:space:]]\+/ /g' | sed 's/^ //; s/ $//')"
    if [[ -z "${out}" ]]; then
      continue
    fi
    if [[ "${out}" != curl:\ * ]]; then
      echo "${out}"
      return
    fi
  done
  echo "${out:--}"
}

default_route_if() {
  if [[ "${os_name}" == "Darwin" ]]; then
    route -n get default 2>/dev/null | awk '/interface:/{print $2; exit}' || true
    return
  fi
  ip route show default 2>/dev/null | awk '/default/{for(i=1;i<=NF;i++){if($i=="dev"){print $(i+1); exit}}}' || true
}

competing_default_tun() {
  local active_tun="${1:-}"
  local def_if
  def_if="$(default_route_if)"
  if [[ -z "${def_if}" ]]; then
    return 1
  fi
  if [[ "${def_if}" =~ ^utun[0-9]+$ ]] && [[ -n "${active_tun}" ]] && [[ "${def_if}" != "${active_tun}" ]]; then
    echo "${def_if}"
    return 0
  fi
  return 1
}

is_default_route_requested() {
  [[ ",${TUN_ROUTES}," == *",default,"* ]]
}

strip_default_route() {
  local csv="${1:-}"
  local out=()
  local item
  IFS=',' read -r -a items <<< "${csv}"
  for item in "${items[@]}"; do
    item="$(echo "${item}" | xargs)"
    [[ -z "${item}" ]] && continue
    [[ "${item}" == "default" ]] && continue
    out+=("${item}")
  done
  local joined=""
  local i
  for i in "${!out[@]}"; do
    if [[ "${i}" -gt 0 ]]; then
      joined+=","
    fi
    joined+="${out[$i]}"
  done
  echo "${joined}"
}

runtime_tun_routes() {
  # On Darwin, do not apply default route before tunnel is established:
  # it can break dial path to the NYC endpoint itself.
  if [[ "${os_name}" == "Darwin" ]] && is_default_route_requested; then
    strip_default_route "${TUN_ROUTES}"
    return 0
  fi
  echo "${TUN_ROUTES}"
}

snapshot_default_route() {
  if [[ "${os_name}" != "Darwin" ]]; then
    return 0
  fi
  local gateway iface
  gateway="$(route -n get default 2>/dev/null | awk '/gateway:/{print $2; exit}' || true)"
  iface="$(default_route_if || true)"
  if [[ -z "${gateway}" || -z "${iface}" ]]; then
    return 0
  fi
  mkdir -p "${TMP_DIR}"
  cat > "${ROUTE_SNAPSHOT_FILE}" <<EOF
gateway=${gateway}
interface=${iface}
EOF
}

restore_default_route() {
  if [[ "${os_name}" != "Darwin" ]]; then
    return 0
  fi
  if [[ ! -f "${ROUTE_SNAPSHOT_FILE}" ]]; then
    return 0
  fi
  local gateway iface cur_if
  gateway="$(awk -F= '$1=="gateway"{print $2}' "${ROUTE_SNAPSHOT_FILE}" | tr -d '[:space:]' || true)"
  iface="$(awk -F= '$1=="interface"{print $2}' "${ROUTE_SNAPSHOT_FILE}" | tr -d '[:space:]' || true)"
  cur_if="$(default_route_if || true)"
  if [[ -z "${gateway}" || -z "${iface}" ]]; then
    rm -f "${ROUTE_SNAPSHOT_FILE}"
    return 0
  fi
  if [[ "${cur_if}" =~ ^utun[0-9]+$ ]] || [[ -z "${cur_if}" ]]; then
    if sudo /sbin/route -n change default "${gateway}" >/dev/null 2>&1 || sudo /sbin/route -n add default "${gateway}" >/dev/null 2>&1; then
      echo "[INFO] restored default route via ${iface} (${gateway})"
    else
      echo "[WARN] failed to restore default route via ${iface} (${gateway})" >&2
    fi
  fi
  rm -f "${ROUTE_SNAPSHOT_FILE}"
}

apply_default_egress_darwin() {
  local tun_name="$1"
  if [[ -z "${tun_name}" || "${tun_name}" == "-" ]]; then
    echo "[ERR] cannot apply default egress: tun name is empty" >&2
    return 1
  fi
  if [[ ! -f "${ROUTE_SNAPSHOT_FILE}" ]]; then
    echo "[ERR] cannot apply default egress: route snapshot missing (${ROUTE_SNAPSHOT_FILE})" >&2
    return 1
  fi
  local gateway iface endpoint
  gateway="$(awk -F= '$1=="gateway"{print $2}' "${ROUTE_SNAPSHOT_FILE}" | tr -d '[:space:]' || true)"
  iface="$(awk -F= '$1=="interface"{print $2}' "${ROUTE_SNAPSHOT_FILE}" | tr -d '[:space:]' || true)"
  endpoint="${SERVER_ADDR%%:*}"
  if [[ -z "${gateway}" || -z "${iface}" || -z "${endpoint}" ]]; then
    echo "[ERR] cannot apply default egress: invalid gateway/interface/endpoint" >&2
    return 1
  fi

  # Keep control-channel endpoint outside tunnel.
  sudo /sbin/route -n add -host "${endpoint}" "${gateway}" -ifscope "${iface}" >/dev/null 2>&1 || \
    sudo /sbin/route -n change -host "${endpoint}" "${gateway}" -ifscope "${iface}" >/dev/null 2>&1 || true

  # Move default route to active utun.
  sudo /sbin/route -n delete default >/dev/null 2>&1 || true
  if ! sudo /sbin/route -n add default -interface "${tun_name}" >/dev/null 2>&1; then
    echo "[ERR] failed to install default route via ${tun_name}" >&2
    return 1
  fi

  cat > "${ROUTE_PIN_FILE}" <<EOF
endpoint=${endpoint}
gateway=${gateway}
interface=${iface}
tun=${tun_name}
EOF
}

remove_default_egress_pin_darwin() {
  if [[ "${os_name}" != "Darwin" ]]; then
    return 0
  fi
  if [[ ! -f "${ROUTE_PIN_FILE}" ]]; then
    return 0
  fi
  local endpoint gateway
  endpoint="$(awk -F= '$1=="endpoint"{print $2}' "${ROUTE_PIN_FILE}" | tr -d '[:space:]' || true)"
  gateway="$(awk -F= '$1=="gateway"{print $2}' "${ROUTE_PIN_FILE}" | tr -d '[:space:]' || true)"
  if [[ -n "${endpoint}" ]]; then
    sudo /sbin/route -n delete -host "${endpoint}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${gateway}" ]]; then
    sudo /sbin/route -n change default "${gateway}" >/dev/null 2>&1 || sudo /sbin/route -n add default "${gateway}" >/dev/null 2>&1 || true
  fi
  rm -f "${ROUTE_PIN_FILE}"
}

cmd_up() {
  if is_running; then
    local pid
    pid="$(tr -d '[:space:]' < "${PID_FILE}" || true)"
    echo "already running (pid=${pid})"
    exit 0
  fi
  build_bins
  if [[ "${os_name}" == "Darwin" ]] && is_default_route_requested; then
    if [[ "${ENABLE_DARWIN_FULL_EGRESS_EXPERIMENT}" != "true" ]]; then
      echo "[ERR] NYC_TUN_ROUTES=default is blocked on Darwin by safety policy." >&2
      echo "[ERR] Use split routes (no default), or set NYC_ENABLE_DARWIN_FULL_EGRESS_EXPERIMENT=true only for explicit manual experiment." >&2
      exit 1
    fi
    local competing
    competing="$(competing_default_tun "${TUN_NAME}" || true)"
    if [[ -n "${competing}" && "${ALLOW_COMPETING_DEFAULT_TUN}" != "true" ]]; then
      echo "[ERR] competing default route detected via ${competing}. Disable other VPN/tunnels or set NYC_ALLOW_COMPETING_DEFAULT_TUN=true to override." >&2
      exit 1
    fi
    snapshot_default_route
  fi
  preflight >/dev/null
  local effective_routes
  effective_routes="$(runtime_tun_routes)"
  local args=(
    -addr "${SERVER_ADDR}"
    -server-name "${SERVER_NAME}"
    -insecure=true
    -client-id "${CLIENT_ID}"
    -server-static-pub "${SERVER_STATIC_PUB}"
    -tun-mtu "${TUN_MTU}"
    -tun-addresses "${TUN_ADDRESSES}"
    -tun-routes "${effective_routes}"
    -tun-config-mode replace
    -tun-cleanup-on-close true
    -health-addr "${HEALTH_ADDR}"
  )
  if [[ -n "${TUN_NAME}" ]]; then
    args+=(-tun-name "${TUN_NAME}")
  fi
  nohup sudo "${CLIENT_BIN}" "${args[@]}" > "${LOG_FILE}" 2>&1 &
  echo $! > "${PID_FILE}"
  sleep 2
  if ! is_running; then
    echo "[ERR] runtime-client exited early; see ${LOG_FILE}" >&2
    tail -n 60 "${LOG_FILE}" >&2 || true
    exit 1
  fi
  detect_tun_name
  if ! wait_for_health; then
    if ! connection_ready; then
      echo "[WARN] runtime-client is running but not ready (no health and no established state)" >&2
      echo "[WARN] runtime_state=$(last_runtime_state)" >&2
      tail -n 40 "${LOG_FILE}" >&2 || true
    fi
  fi
  if [[ "${os_name}" == "Darwin" ]] && is_default_route_requested; then
    local tun_for_default="-"
    if [[ -f "${TUN_NAME_FILE}" ]]; then
      tun_for_default="$(tr -d '[:space:]' < "${TUN_NAME_FILE}" || true)"
    fi
    if ! connection_ready; then
      echo "[ERR] tunnel is not established, refusing to switch default route to avoid local outage" >&2
      cmd_down || true
      exit 1
    fi
    if ! apply_default_egress_darwin "${tun_for_default}"; then
      echo "[ERR] failed to apply Darwin default egress policy; rolling back" >&2
      cmd_down || true
      exit 1
    fi
  fi
  cmd_status
}

cmd_down() {
  remove_default_egress_pin_darwin || true
  local tun_name_for_cleanup
  tun_name_for_cleanup="${TUN_NAME:-}"
  if [[ -f "${TUN_NAME_FILE}" ]]; then
    local from_file
    from_file="$(tr -d '[:space:]' < "${TUN_NAME_FILE}" || true)"
    if [[ -n "${from_file}" ]]; then
      tun_name_for_cleanup="${from_file}"
    fi
  fi
  if [[ "${os_name}" == "Darwin" && -n "${tun_name_for_cleanup}" && ! "${tun_name_for_cleanup}" =~ ^utun[0-9]+$ ]]; then
    tun_name_for_cleanup=""
  fi
  if [[ -n "${tun_name_for_cleanup}" ]]; then
    "${ROOT}/scripts/rollback_local_tun_client.sh" \
      --tun-name "${tun_name_for_cleanup}" \
      --pid-file "${PID_FILE}" \
      --routes "${TUN_ROUTES}"
  else
    if is_running; then
      local pid
      pid="$(tr -d '[:space:]' < "${PID_FILE}" || true)"
      kill "${pid}" >/dev/null 2>&1 || true
      sleep 1
      kill -9 "${pid}" >/dev/null 2>&1 || true
    fi
  fi
  rm -f "${PID_FILE}" "${TUN_NAME_FILE}"
  restore_default_route || true
  echo "stopped"
  echo "log_file=${LOG_FILE}"
}

cmd_status() {
  local state="stopped"
  local pid="-"
  local tun_name="-"
  local health="-"
  local runtime_state="-"
  local public_ip="-"
  local public_ip_via_tun="-"
  local connection_ok="false"
  local route_default_if="-"
  local competing_tun="-"
  if is_running; then
    state="running"
    pid="$(tr -d '[:space:]' < "${PID_FILE}" || true)"
  fi
  if [[ -f "${TUN_NAME_FILE}" ]]; then
    tun_name="$(tr -d '[:space:]' < "${TUN_NAME_FILE}" || true)"
    [[ -n "${tun_name}" ]] || tun_name="-"
  fi
  if [[ "${os_name}" == "Darwin" && "${tun_name}" != "-" && ! "${tun_name}" =~ ^utun[0-9]+$ ]]; then
    tun_name="-"
  fi
  runtime_state="$(last_runtime_state)"
  if [[ "${runtime_state}" == "established" ]]; then
    connection_ok="true"
  fi
  if command -v curl >/dev/null 2>&1; then
    local health_summary
    health_summary="$(probe_health_summary || true)"
    if [[ -n "${health_summary}" ]]; then
      health="${health_summary}"
    fi
    public_ip="$(probe_public_ip)"
    if [[ "${state}" == "running" && "${tun_name}" != "-" && "${runtime_state}" == "established" ]]; then
      if [[ -z "${TUN_ROUTES}" ]]; then
        public_ip_via_tun="skipped(no_tun_routes_configured)"
      else
        public_ip_via_tun="$(probe_public_ip "${tun_name}")"
      fi
    elif [[ "${state}" == "running" && "${tun_name}" != "-" ]]; then
      public_ip_via_tun="pending(runtime_state=${runtime_state})"
    fi
  fi
  route_default_if="$(default_route_if || true)"
  [[ -n "${route_default_if}" ]] || route_default_if="-"
  if c="$(competing_default_tun "${tun_name}")"; then
    competing_tun="${c}"
  fi
  echo "state=${state}"
  echo "os=${os_name}"
  echo "pid=${pid}"
  echo "tun_name=${tun_name}"
  echo "runtime_state=${runtime_state}"
  echo "connection_ok=${connection_ok}"
  echo "health_addr=${HEALTH_ADDR}"
  echo "health=${health}"
  echo "public_ip=${public_ip}"
  echo "public_ip_via_tun=${public_ip_via_tun}"
  echo "route_default_if=${route_default_if}"
  echo "competing_default_tun=${competing_tun}"
  echo "log_file=${LOG_FILE}"
  if [[ "${competing_tun}" != "-" ]]; then
    echo "[WARN] default route goes via ${competing_tun}, not ${tun_name}. Disable other VPN/tunnel before NYC full-egress test." >&2
  fi
}

cmd_logs() {
  if [[ ! -f "${LOG_FILE}" ]]; then
    echo "[ERR] log file not found: ${LOG_FILE}" >&2
    exit 1
  fi
  tail -n 120 "${LOG_FILE}"
}

action="${1:-}"
case "${action}" in
  up) cmd_up ;;
  down) cmd_down ;;
  status) cmd_status ;;
  logs) cmd_logs ;;
  -h|--help|"") usage ;;
  *)
    echo "[ERR] unknown action: ${action}" >&2
    usage >&2
    exit 2
    ;;
esac
