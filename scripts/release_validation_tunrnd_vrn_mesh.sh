#!/usr/bin/env bash
set -uo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/release_validation_tunrnd_vrn_mesh.sh [options]

Release-validation harness for vrn mesh:
  vrn <-> ams
  vrn <-> fra
  vrn <-> nyc

Stages:
  1) strict vrn mesh gate
  2) soak checks (periodic services + ping)
  3) impairment (client restart + recovery SLA)
  4) packaging/update baseline checks
  5) report generation (json + markdown)

Options:
  --out-dir <path>          default: ./artifacts/tunrnd-vrn-release-validation
  --profile <quick|full>    default: quick
  --soak-duration <sec>     default: 180 (quick), 900 (full)
  --soak-interval <sec>     default: 20
  --soak-failure-budget <n> default: 2
  --impair-host <host>      default: fra (ams|fra|nyc)
  --impair-timeout <sec>    default: 120
  --impair-restart-server   default: true (true|false)
  --fra-via <host>          default: ams
  --nyc-via <host>          default: ams
  --ssh-retries <n>         default: 6
  --ssh-delay <sec>         default: 3
  --ssh-timeout <sec>       default: 20
  --stage1-timeout <sec>    default: 900
  --help                    show this help
EOF
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out_dir="./artifacts/tunrnd-vrn-release-validation"
profile="quick"
soak_duration=""
soak_interval=20
soak_failure_budget=2
impair_host="fra"
impair_timeout=120
impair_restart_server="true"
fra_via="ams"
nyc_via="ams"
ssh_retries=6
ssh_delay=3
ssh_timeout=20
stage1_timeout=900
timeout_cmd=""

gate_status="failed"
soak_status="failed"
impair_status="failed"
packaging_status="failed"
overall_status="failed"
notes=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir) out_dir="${2:-}"; shift 2 ;;
    --profile) profile="${2:-}"; shift 2 ;;
    --soak-duration) soak_duration="${2:-}"; shift 2 ;;
    --soak-interval) soak_interval="${2:-}"; shift 2 ;;
    --soak-failure-budget) soak_failure_budget="${2:-}"; shift 2 ;;
    --impair-host) impair_host="${2:-}"; shift 2 ;;
    --impair-timeout) impair_timeout="${2:-}"; shift 2 ;;
    --impair-restart-server) impair_restart_server="${2:-}"; shift 2 ;;
    --fra-via) fra_via="${2:-}"; shift 2 ;;
    --nyc-via) nyc_via="${2:-}"; shift 2 ;;
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

case "${impair_host}" in
  ams|fra|nyc) ;;
  *)
    echo "invalid impair-host: ${impair_host}" >&2
    exit 2
    ;;
esac

if ! [[ "${soak_failure_budget}" =~ ^[0-9]+$ ]]; then
  echo "invalid soak-failure-budget: ${soak_failure_budget}" >&2
  exit 2
fi

case "${impair_restart_server}" in
  true|false) ;;
  *)
    echo "invalid impair-restart-server: ${impair_restart_server}" >&2
    exit 2
    ;;
esac

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
  local out=""
  local rc=0
  while (( tries < ssh_retries )); do
    out="$(run_remote "${host}" "${cmd}" 2>&1)" && rc=0 || rc=$?
    if (( rc == 0 )); then
      printf "%s\n" "${out}"
      return 0
    fi
    tries=$((tries + 1))
    if (( tries >= ssh_retries )); then
      break
    fi
    sleep "${ssh_delay}"
  done
  printf "%s\n" "${out}" >&2
  return "${rc}"
}

append_note() {
  local msg="$1"
  if [[ -n "${notes}" ]]; then
    notes="${notes}; ${msg}"
  else
    notes="${msg}"
  fi
}

init_timeout_cmd

mkdir -p "${out_dir}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
log_file="${out_dir}/vrn-release-validation-${stamp}.log"
report_json="${out_dir}/vrn-release-validation-${stamp}.json"
report_md="${out_dir}/vrn-release-validation-${stamp}.md"
soak_log="${out_dir}/vrn-soak-${stamp}.log"

echo "[vrn-release-validation] started at ${stamp}" | tee "${log_file}"
{
  echo "[vrn-release-validation] profile=${profile} soak_duration=${soak_duration}s soak_interval=${soak_interval}s"
  echo "[vrn-release-validation] soak_failure_budget=${soak_failure_budget}"
  echo "[vrn-release-validation] impairment host=${impair_host} timeout=${impair_timeout}s"
  echo "[vrn-release-validation] impairment restart_server=${impair_restart_server}"
  echo "[vrn-release-validation] fra_via=${fra_via} nyc_via=${nyc_via} ssh_retries=${ssh_retries} ssh_timeout=${ssh_timeout} stage1_timeout=${stage1_timeout}s"
  echo
} | tee -a "${log_file}"

echo "== stage 1: strict vrn gate ==" | tee -a "${log_file}"
stage1_log="${out_dir}/stage1-vrn-gate-${stamp}.log"
if run_with_timeout "${stage1_timeout}" \
  "${script_dir}/gate_tunrnd_vrn_mesh.sh" \
    --out-dir "${out_dir}" \
    --fra-via "${fra_via}" \
    --nyc-via "${nyc_via}" \
    --strict-ssh \
    --ssh-retries "${ssh_retries}" \
    --ssh-delay "${ssh_delay}" \
    --ssh-timeout "${ssh_timeout}" >"${stage1_log}" 2>&1; then
  gate_status="passed"
  echo "[stage1] passed" | tee -a "${log_file}"
else
  gate_status="failed"
  append_note "stage1 strict vrn gate failed"
  echo "[stage1] failed (see ${stage1_log})" | tee -a "${log_file}"
fi

echo "== stage 2: soak checks ==" | tee -a "${log_file}"
if [[ "${gate_status}" == "passed" ]]; then
  start_ts="$(date +%s)"
  end_ts=$((start_ts + soak_duration))
  cycle=0
  soak_failed=0
  soak_failures=0
  while (( $(date +%s) < end_ts )); do
    cycle=$((cycle + 1))
    now_utc="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    if {
      echo "[soak] cycle=${cycle} at=${now_utc}"
      run_remote_retry "vrn" "systemctl is-active tun-runtime-server@ams.service tun-runtime-server@fra.service tun-runtime-server@nyc.service"
      run_remote_retry "ams" "systemctl is-active tun-runtime-client@vrn.service && sudo -n ping -I trcli-vrn -c 1 -W 2 10.253.1.1"
      run_remote_retry "fra" "systemctl is-active tun-runtime-client@vrn.service && sudo -n ping -I trcli-vrn -c 1 -W 2 10.253.2.1"
      run_remote_retry "nyc" "systemctl is-active tun-runtime-client@vrn.service && sudo -n ping -I trcli-vrn -c 1 -W 2 10.253.3.1"
    } >> "${soak_log}" 2>&1; then
      :
    else
      soak_failures=$((soak_failures + 1))
      echo "[soak] cycle=${cycle} transient failure count=${soak_failures}" >> "${soak_log}"
      if (( soak_failures > soak_failure_budget )); then
        soak_failed=1
      fi
    fi
    if (( soak_failed != 0 )); then
      break
    fi
    sleep "${soak_interval}"
  done
  if (( soak_failed == 0 )); then
    soak_status="passed"
    echo "[stage2] passed" | tee -a "${log_file}"
  else
    soak_status="failed"
    append_note "stage2 soak failed"
    echo "[stage2] failed (see ${soak_log})" | tee -a "${log_file}"
  fi
else
  soak_status="skipped"
  append_note "stage2 skipped due to stage1 failure"
  echo "[stage2] skipped" | tee -a "${log_file}"
fi

echo "== stage 3: impairment recovery ==" | tee -a "${log_file}"
if [[ "${gate_status}" == "passed" ]]; then
  target_ip="10.253.2.1"
  server_instance="fra"
  if [[ "${impair_host}" == "ams" ]]; then
    target_ip="10.253.1.1"
    server_instance="ams"
  elif [[ "${impair_host}" == "nyc" ]]; then
    target_ip="10.253.3.1"
    server_instance="nyc"
  fi

  impair_log="${out_dir}/stage3-impair-${stamp}.log"
  restart_ok=0
  for _ in $(seq 1 3); do
    if {
      echo "[impair] restart client service on ${impair_host}"
      run_remote_retry "${impair_host}" "sudo -n systemctl restart tun-runtime-client@vrn.service"
    } > "${impair_log}" 2>&1; then
      restart_ok=1
      break
    fi
    sleep 10
  done
  if (( restart_ok == 0 )); then
    impair_status="failed"
    append_note "stage3 restart failed on ${impair_host}"
    echo "[stage3] failed (restart, see ${impair_log})" | tee -a "${log_file}"
  else
    deadline=$(( $(date +%s) + impair_timeout ))
    recovered=0
    while (( $(date +%s) < deadline )); do
      if run_remote_retry "${impair_host}" "systemctl is-active tun-runtime-client@vrn.service >/dev/null && sudo -n ping -I trcli-vrn -c 1 -W 2 ${target_ip} >/dev/null"; then
        recovered=1
        break
      fi
      sleep 5
    done
    if (( recovered == 1 )); then
      impair_status="passed"
      echo "[stage3] passed (recovered within ${impair_timeout}s)" | tee -a "${log_file}"
    else
      if [[ "${impair_restart_server}" == "true" ]]; then
        echo "[stage3] timeout reached, trying server-side restart fallback (tun-runtime-server@${server_instance}.service on vrn)" | tee -a "${log_file}"
        if run_remote_retry "vrn" "sudo -n systemctl restart tun-runtime-server@${server_instance}.service"; then
          deadline2=$(( $(date +%s) + impair_timeout ))
          recovered2=0
          while (( $(date +%s) < deadline2 )); do
            if run_remote_retry "${impair_host}" "systemctl is-active tun-runtime-client@vrn.service >/dev/null && sudo -n ping -I trcli-vrn -c 1 -W 2 ${target_ip} >/dev/null"; then
              recovered2=1
              break
            fi
            sleep 5
          done
          if (( recovered2 == 1 )); then
            impair_status="passed"
            append_note "stage3 required server-side fallback restart on vrn@${server_instance}"
            echo "[stage3] passed after fallback restart" | tee -a "${log_file}"
          else
            impair_status="failed"
            append_note "stage3 recovery timeout on ${impair_host} (fallback restart did not recover)"
            echo "[stage3] failed (recovery timeout after fallback)" | tee -a "${log_file}"
          fi
        else
          impair_status="failed"
          append_note "stage3 server fallback restart failed on vrn@${server_instance}"
          echo "[stage3] failed (server fallback restart failed)" | tee -a "${log_file}"
        fi
      else
        impair_status="failed"
        append_note "stage3 recovery timeout on ${impair_host}"
        echo "[stage3] failed (recovery timeout)" | tee -a "${log_file}"
      fi
    fi
  fi
else
  impair_status="skipped"
  append_note "stage3 skipped due to stage1 failure"
  echo "[stage3] skipped" | tee -a "${log_file}"
fi

echo "== stage 4: packaging/update baseline ==" | tee -a "${log_file}"
stage4_log="${out_dir}/stage4-packaging-${stamp}.log"
{
  run_remote_retry "vrn" "test -x /usr/local/bin/runtime-server-systemd && systemctl cat tun-runtime-server@.service >/dev/null"
  run_remote_retry "ams" "test -x /usr/local/bin/runtime-client && systemctl cat tun-runtime-client@.service >/dev/null"
  run_remote_retry "fra" "test -x /usr/local/bin/runtime-client && systemctl cat tun-runtime-client@.service >/dev/null"
  run_remote_retry "nyc" "test -x /usr/local/bin/runtime-client && systemctl cat tun-runtime-client@.service >/dev/null"
} > "${stage4_log}" 2>&1
if [[ $? -eq 0 ]]; then
  packaging_status="passed"
  echo "[stage4] passed" | tee -a "${log_file}"
else
  packaging_status="failed"
  append_note "stage4 packaging baseline failed"
  echo "[stage4] failed (see ${stage4_log})" | tee -a "${log_file}"
fi

if [[ "${gate_status}" == "passed" && "${soak_status}" == "passed" && "${impair_status}" == "passed" && "${packaging_status}" == "passed" ]]; then
  overall_status="passed"
else
  overall_status="failed"
fi

cat > "${report_json}" <<JSON
{
  "ok": $( [[ "${overall_status}" == "passed" ]] && echo "true" || echo "false" ),
  "profile": "${profile}",
  "timestamp_utc": "${stamp}",
  "stages": {
    "stage1_gate": "${gate_status}",
    "stage2_soak": "${soak_status}",
    "stage3_impairment": "${impair_status}",
    "stage4_packaging": "${packaging_status}"
  },
  "notes": "${notes}"
}
JSON

cat > "${report_md}" <<MD
# vrn Mesh Release Validation Report

- Timestamp (UTC): ${stamp}
- Profile: ${profile}
- Overall: **${overall_status}**

## Stage Status

- Stage 1 (strict vrn gate): ${gate_status}
- Stage 2 (soak): ${soak_status}
- Stage 3 (impairment): ${impair_status}
- Stage 4 (packaging baseline): ${packaging_status}

## Runtime Params

- Soak duration: ${soak_duration}s
- Soak interval: ${soak_interval}s
- Impair host: ${impair_host}
- Impair timeout: ${impair_timeout}s
- fra via: ${fra_via}
- nyc via: ${nyc_via}

## Notes

- ${notes:-none}

## Artifacts

- log: ${log_file}
- stage1: ${stage1_log}
- soak: ${soak_log}
- stage4: ${stage4_log}
- report json: ${report_json}
MD

echo "[vrn-release-validation] overall=${overall_status}" | tee -a "${log_file}"
echo "${report_json}"
echo "${report_md}"

if [[ "${overall_status}" != "passed" ]]; then
  exit 1
fi
