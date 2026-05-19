#!/usr/bin/env bash
set -uo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/release_validation_tunrnd_prod_mesh.sh [options]

Unified release-validation harness for tun-rnd prod mesh.

Validation stages:
  1) strict interop gate (vrn + edg contours)
  2) soak checks (periodic service + ping sanity)
  3) network impairment (client service restart + recovery window)
  4) packaging/update baseline checks
  5) go/no-go report generation (json + markdown)

Options:
  --out-dir <path>          default: ./artifacts/tunrnd-prod-release-validation
  --profile <quick|full>    default: quick
  --soak-duration <sec>     default: 180 (quick), 900 (full)
  --soak-interval <sec>     default: 20
  --impair-host <host>      default: fra
  --impair-contour <name>   default: vrn (vrn|edg)
  --edg-host <host>         default: edg
  --impair-timeout <sec>    default: 120
  --impair-restart-server   default: true (true|false)
  --fra-via <host>          default: ams
  --nyc-via <host>          default: ams
  --edg-via <host>          default: empty
  --ssh-retries <n>         default: 6
  --ssh-delay <sec>         default: 3
  --ssh-timeout <sec>       default: 20
  --stage1-timeout <sec>    default: 900
  --help                    show this help
EOF
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out_dir="./artifacts/tunrnd-prod-release-validation"
profile="quick"
soak_duration=""
soak_interval=20
impair_host="fra"
impair_contour="vrn"
edg_host="edg"
impair_timeout=120
impair_restart_server="true"
fra_via="ams"
nyc_via="ams"
edg_via=""
ssh_retries=6
ssh_delay=3
ssh_timeout=20
stage1_timeout=900
timeout_cmd=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir) out_dir="${2:-}"; shift 2 ;;
    --profile) profile="${2:-}"; shift 2 ;;
    --soak-duration) soak_duration="${2:-}"; shift 2 ;;
    --soak-interval) soak_interval="${2:-}"; shift 2 ;;
    --impair-host) impair_host="${2:-}"; shift 2 ;;
    --impair-contour) impair_contour="${2:-}"; shift 2 ;;
    --edg-host) edg_host="${2:-}"; shift 2 ;;
    --impair-timeout) impair_timeout="${2:-}"; shift 2 ;;
    --impair-restart-server) impair_restart_server="${2:-}"; shift 2 ;;
    --fra-via) fra_via="${2:-}"; shift 2 ;;
    --nyc-via) nyc_via="${2:-}"; shift 2 ;;
    --edg-via) edg_via="${2:-}"; shift 2 ;;
    --ssh-retries) ssh_retries="${2:-}"; shift 2 ;;
    --ssh-delay) ssh_delay="${2:-}"; shift 2 ;;
    --ssh-timeout) ssh_timeout="${2:-}"; shift 2 ;;
    --stage1-timeout) stage1_timeout="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "${profile}" in
  quick|full) ;;
  *)
    echo "invalid profile: ${profile}" >&2
    exit 2
    ;;
esac

if [[ -z "${soak_duration}" ]]; then
  if [[ "${profile}" == "full" ]]; then
    soak_duration=900
  else
    soak_duration=180
  fi
fi

case "${impair_contour}" in
  vrn|edg) ;;
  *)
    echo "invalid impair contour: ${impair_contour}" >&2
    exit 2
    ;;
esac

case "${impair_host}" in
  ams|fra|nyc) ;;
  *)
    echo "invalid impair host: ${impair_host}" >&2
    exit 2
    ;;
esac

case "${impair_restart_server}" in
  true|false) ;;
  *)
    echo "invalid impair-restart-server: ${impair_restart_server}" >&2
    exit 2
    ;;
esac

mkdir -p "${out_dir}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
log_file="${out_dir}/release-validation-${stamp}.log"
report_json="${out_dir}/release-validation-${stamp}.json"
report_md="${out_dir}/release-validation-${stamp}.md"
soak_log="${out_dir}/soak-${stamp}.log"

gate_status="failed"
soak_status="failed"
impair_status="failed"
packaging_status="failed"
notes=""

ssh_via_for_host() {
  local host="$1"
  if [[ "${host}" == "fra" ]]; then
    printf "%s\n" "${fra_via}"
    return 0
  fi
  if [[ "${host}" == "nyc" ]]; then
    printf "%s\n" "${nyc_via}"
    return 0
  fi
  if [[ "${host}" == "${edg_host}" ]]; then
    printf "%s\n" "${edg_via}"
    return 0
  fi
  printf "%s\n" ""
}

run_remote() {
  local host="$1"
  local cmd="$2"
  local via
  local cmd_timeout
  cmd_timeout=$((ssh_timeout + 10))
  via="$(ssh_via_for_host "${host}")"
  if [[ -n "${via}" ]]; then
    run_with_timeout "${cmd_timeout}" \
      ssh -J "${via}" \
        -o BatchMode=yes \
        -o LogLevel=ERROR \
        -o ConnectTimeout="${ssh_timeout}" \
        -o ServerAliveInterval=5 \
        -o ServerAliveCountMax=1 \
        -o ConnectionAttempts=1 \
        "${host}" "${cmd}"
  else
    run_with_timeout "${cmd_timeout}" \
      ssh \
        -o BatchMode=yes \
        -o LogLevel=ERROR \
        -o ConnectTimeout="${ssh_timeout}" \
        -o ServerAliveInterval=5 \
        -o ServerAliveCountMax=1 \
        -o ConnectionAttempts=1 \
        "${host}" "${cmd}"
  fi
}

run_remote_retry() {
  local host="$1"
  local cmd="$2"
  local tries=0
  while (( tries < ssh_retries )); do
    if run_remote "${host}" "${cmd}"; then
      return 0
    fi
    tries=$((tries + 1))
    if (( tries >= ssh_retries )); then
      break
    fi
    sleep "${ssh_delay}"
  done
  return 1
}

append_note() {
  local msg="$1"
  if [[ -n "${notes}" ]]; then
    notes="${notes}; ${msg}"
  else
    notes="${msg}"
  fi
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
  return $?
}

init_timeout_cmd

echo "[release-validation] started at ${stamp}" | tee "${log_file}"
{
  echo "[release-validation] profile=${profile} soak_duration=${soak_duration}s soak_interval=${soak_interval}s"
  echo "[release-validation] impairment host=${impair_host} contour=${impair_contour} timeout=${impair_timeout}s"
  echo "[release-validation] impairment restart_server=${impair_restart_server}"
  echo "[release-validation] edg_host=${edg_host} fra_via=${fra_via} nyc_via=${nyc_via} edg_via=${edg_via:-none} ssh_retries=${ssh_retries} ssh_timeout=${ssh_timeout} stage1_timeout=${stage1_timeout}s"
  echo
} | tee -a "${log_file}"

echo "== stage 1: strict interop gate ==" | tee -a "${log_file}"
stage1_log="${out_dir}/stage1-strict-gate-${stamp}.log"
stage1_rc=0
run_with_timeout "${stage1_timeout}" "${script_dir}/gate_tunrnd_prod_release.sh" \
  --out-dir "${out_dir}/strict-gate" \
  --edg-host "${edg_host}" \
  --fra-via "${fra_via}" \
  --nyc-via "${nyc_via}" \
  --edg-via "${edg_via}" \
  --ssh-retries "${ssh_retries}" \
  --ssh-delay "${ssh_delay}" \
  --ssh-timeout "${ssh_timeout}" > "${stage1_log}" 2>&1 || stage1_rc=$?
if [[ "${stage1_rc}" -eq 0 ]]; then
  gate_status="passed"
  cat "${stage1_log}" >> "${log_file}"
  gate_artifact="$(tail -n 1 "${stage1_log}")"
  echo "[stage1] passed: ${gate_artifact}" | tee -a "${log_file}"
else
  gate_status="failed"
  cat "${stage1_log}" >> "${log_file}"
  if [[ "${stage1_rc}" -eq 124 ]]; then
    append_note "stage1 strict gate timeout (${stage1_timeout}s)"
  else
    append_note "stage1 strict gate failed"
  fi
  echo "[stage1] failed" | tee -a "${log_file}"
fi
echo | tee -a "${log_file}"

echo "== stage 2: soak checks ==" | tee -a "${log_file}"
soak_ok=true
soak_start="$(date +%s)"
soak_end=$((soak_start + soak_duration))
iter=0
: > "${soak_log}"
run_soak_check() {
  local label="$1"
  local host="$2"
  local cmd="$3"
  local out=""
  if out="$(run_remote_retry "${host}" "${cmd}" 2>&1)"; then
    if [[ -n "${out}" ]]; then
      printf "%s\n" "${out}" | tee -a "${soak_log}" "${log_file}"
    fi
    return 0
  fi
  if [[ -n "${out}" ]]; then
    printf "%s\n" "${out}" | tee -a "${soak_log}" "${log_file}"
  fi
  echo "[soak] fail check=${label}" | tee -a "${soak_log}" "${log_file}"
  return 1
}
while (( "$(date +%s)" < soak_end )); do
  iter=$((iter + 1))
  now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "[soak] iter=${iter} ts=${now}" | tee -a "${soak_log}" "${log_file}"
  run_soak_check "vrn_servers_active" "vrn" "sudo -n systemctl is-active tun-runtime-server@ams.service tun-runtime-server@fra.service tun-runtime-server@nyc.service" || soak_ok=false
  run_soak_check "edg_servers_active" "edg" "sudo -n systemctl is-active tun-runtime-server@ams.service tun-runtime-server@fra.service tun-runtime-server@nyc.service" || soak_ok=false
  run_soak_check "ams_vrn_ping" "ams" "sudo -n ping -I trcli-vrn -c 1 -W 2 10.253.1.1 >/dev/null && echo ams-vrn=ok" || soak_ok=false
  run_soak_check "ams_edg_ping" "ams" "sudo -n ping -I trcli-edg -c 1 -W 2 10.254.1.1 >/dev/null && echo ams-edg=ok" || soak_ok=false
  run_soak_check "fra_vrn_ping" "fra" "sudo -n ping -I trcli-vrn -c 1 -W 2 10.253.2.1 >/dev/null && echo fra-vrn=ok" || soak_ok=false
  run_soak_check "fra_edg_ping" "fra" "sudo -n ping -I trcli-edg -c 1 -W 2 10.254.2.1 >/dev/null && echo fra-edg=ok" || soak_ok=false
  run_soak_check "nyc_vrn_ping" "nyc" "sudo -n ping -I trcli-vrn -c 1 -W 2 10.253.3.1 >/dev/null && echo nyc-vrn=ok" || soak_ok=false
  run_soak_check "nyc_edg_ping" "nyc" "sudo -n ping -I trcli-edg -c 1 -W 2 10.254.3.1 >/dev/null && echo nyc-edg=ok" || soak_ok=false
  if [[ "${soak_ok}" == "true" ]]; then
    echo "[soak] iter=${iter} result=ok" | tee -a "${soak_log}" "${log_file}"
  else
    echo "[soak] iter=${iter} result=failed" | tee -a "${soak_log}" "${log_file}"
  fi
  echo | tee -a "${soak_log}" "${log_file}"
  if [[ "${soak_ok}" != "true" ]]; then
    break
  fi
  sleep "${soak_interval}"
done
if [[ "${soak_ok}" == "true" ]]; then
  soak_status="passed"
  echo "[stage2] passed (iterations=${iter})" | tee -a "${log_file}"
else
  soak_status="failed"
  append_note "stage2 soak failed"
  echo "[stage2] failed (iterations=${iter})" | tee -a "${log_file}"
fi
echo | tee -a "${log_file}"

echo "== stage 3: network impairment ==" | tee -a "${log_file}"
impair_service="tun-runtime-client@${impair_contour}.service"
impair_server_host="${impair_contour}"
impair_server_service="tun-runtime-server@${impair_host}.service"
if [[ "${impair_contour}" == "edg" ]]; then
  impair_server_host="${edg_host}"
fi
impair_ping_cmd=""
case "${impair_contour}:${impair_host}" in
  vrn:ams) impair_ping_cmd="sudo -n ping -I trcli-vrn -c 1 -W 2 10.253.1.1 >/dev/null" ;;
  vrn:fra) impair_ping_cmd="sudo -n ping -I trcli-vrn -c 1 -W 2 10.253.2.1 >/dev/null" ;;
  vrn:nyc) impair_ping_cmd="sudo -n ping -I trcli-vrn -c 1 -W 2 10.253.3.1 >/dev/null" ;;
  edg:ams) impair_ping_cmd="sudo -n ping -I trcli-edg -c 1 -W 2 10.254.1.1 >/dev/null" ;;
  edg:fra) impair_ping_cmd="sudo -n ping -I trcli-edg -c 1 -W 2 10.254.2.1 >/dev/null" ;;
  edg:nyc) impair_ping_cmd="sudo -n ping -I trcli-edg -c 1 -W 2 10.254.3.1 >/dev/null" ;;
esac
if [[ "${impair_restart_server}" == "true" ]]; then
  stage3_server_restart_out=""
  if stage3_server_restart_out="$(run_remote_retry "${impair_server_host}" "sudo -n systemctl restart ${impair_server_service}" 2>&1)"; then
    if [[ -n "${stage3_server_restart_out}" ]]; then
      printf "%s\n" "${stage3_server_restart_out}" | tee -a "${log_file}"
    fi
    echo "[stage3] info (${impair_server_host}:${impair_server_service} restarted)" | tee -a "${log_file}"
  else
    if [[ -n "${stage3_server_restart_out}" ]]; then
      printf "%s\n" "${stage3_server_restart_out}" | tee -a "${log_file}"
    fi
    echo "[stage3] warn (${impair_server_host}:${impair_server_service} restart failed, continuing with client restart)" | tee -a "${log_file}"
  fi
fi
stage3_client_restart_out=""
if stage3_client_restart_out="$(run_remote_retry "${impair_host}" "sudo -n systemctl restart ${impair_service}" 2>&1)"; then
  if [[ -n "${stage3_client_restart_out}" ]]; then
    printf "%s\n" "${stage3_client_restart_out}" | tee -a "${log_file}"
  fi
  deadline=$(( $(date +%s) + impair_timeout ))
  recovered=false
  while (( "$(date +%s)" < deadline )); do
    if run_remote_retry "${impair_host}" "sudo -n systemctl is-active ${impair_service}" \
      && run_remote_retry "${impair_host}" "${impair_ping_cmd}"; then
      recovered=true
      break
    fi
    sleep 5
  done
  if [[ "${recovered}" == "true" ]]; then
    impair_status="passed"
    echo "[stage3] passed (${impair_host}:${impair_service} recovered)" | tee -a "${log_file}"
  else
    impair_status="failed"
    append_note "stage3 impairment recovery timeout"
    echo "[stage3] failed (${impair_host}:${impair_service} not recovered in ${impair_timeout}s)" | tee -a "${log_file}"
  fi
else
  if [[ -n "${stage3_client_restart_out}" ]]; then
    printf "%s\n" "${stage3_client_restart_out}" | tee -a "${log_file}"
  fi
  impair_status="failed"
  append_note "stage3 impairment restart failed"
  echo "[stage3] failed (restart command failed)" | tee -a "${log_file}"
fi
echo | tee -a "${log_file}"

echo "== stage 4: packaging/update checks ==" | tee -a "${log_file}"
pack_ok=true
stage4_edg_access_noted=false
check_packaging() {
  local host="$1"
  local label="$2"
  local cmd="$3"
  local out=""
  if out="$(run_remote_retry "${host}" "${cmd}" 2>&1)"; then
    if [[ -n "${out}" ]]; then
      printf "%s\n" "${out}" >> "${log_file}"
    fi
    echo "[stage4] ok ${host} ${label}" >> "${log_file}"
    return 0
  fi
  if [[ -n "${out}" ]]; then
    printf "%s\n" "${out}" >> "${log_file}"
  fi
  echo "[stage4] fail ${host} ${label}" | tee -a "${log_file}"
  if [[ "${host}" == "edg" && "${stage4_edg_access_noted}" == "false" ]]; then
    append_note "stage4 edg checks inconclusive (ssh/session access failure)"
    stage4_edg_access_noted=true
  fi
  return 1
}
for h in bx_msk_d vrn "${edg_host}"; do
  check_packaging "${h}" "server_bin" "sudo -n test -x /usr/local/bin/runtime-server-systemd" || pack_ok=false
  check_packaging "${h}" "server_unit" "sudo -n test -f /etc/systemd/system/tun-runtime-server@.service" || pack_ok=false
  check_packaging "${h}" "server_env" "sudo -n sh -lc '[ -f /etc/tun/runtime-server.env ] || ls /etc/tun/runtime-server-*.env >/dev/null 2>&1'" || pack_ok=false
done
for h in ams fra nyc; do
  check_packaging "${h}" "client_bin" "sudo -n test -x /usr/local/bin/runtime-client" || pack_ok=false
  check_packaging "${h}" "client_unit" "sudo -n test -f /etc/systemd/system/tun-runtime-client@.service" || pack_ok=false
done
if [[ "${pack_ok}" == "true" ]]; then
  packaging_status="passed"
  echo "[stage4] passed" | tee -a "${log_file}"
else
  packaging_status="failed"
  append_note "stage4 packaging checks failed"
  echo "[stage4] failed" | tee -a "${log_file}"
fi
echo | tee -a "${log_file}"

go_no_go="GO"
if [[ "${gate_status}" != "passed" || "${soak_status}" != "passed" || "${impair_status}" != "passed" || "${packaging_status}" != "passed" ]]; then
  go_no_go="NO_GO"
fi

cat > "${report_json}" <<EOF
{
  "timestamp_utc": "${stamp}",
  "profile": "${profile}",
  "results": {
    "strict_interop_gate": "${gate_status}",
    "soak": "${soak_status}",
    "network_impairment": "${impair_status}",
    "packaging_update": "${packaging_status}"
  },
  "go_no_go": "${go_no_go}",
  "notes": "${notes}",
  "artifacts": {
    "log_file": "${log_file}",
    "soak_log": "${soak_log}"
  }
}
EOF

cat > "${report_md}" <<EOF
# tun-rnd prod mesh release validation

- Timestamp (UTC): ${stamp}
- Profile: ${profile}
- Strict interop gate: ${gate_status}
- Soak: ${soak_status}
- Network impairment: ${impair_status}
- Packaging/update: ${packaging_status}
- Decision: ${go_no_go}
- Notes: ${notes}

## Artifacts

- Log: ${log_file}
- Soak log: ${soak_log}
- JSON report: ${report_json}
EOF

echo "${report_json}"
echo "${report_md}"
if [[ "${go_no_go}" != "GO" ]]; then
  exit 1
fi
