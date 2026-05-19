#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/release_gate_runtime_helper.sh [options]

Unified release gate for runtime-helper productization.
By default it runs:
  1) go test ./...
  2) scripts/runtime_helper_smoke.sh
  3) scripts/runtime_helper_bridge_autopilot_smoke.sh
  4) scripts/support_bundle_ingest_gate.sh (requires --bundle)
  5) scripts/runtime_helper_bridge_autopilot_canary.sh (requires canary params)

Options:
  --profile <name>                          local|staging|staging-full|staging-full-strict|ci-fast|ci-full (default: local)
  --report-file <path>                      Optional JSON summary output
  --skip-go-test
  --skip-helper-smoke
  --skip-autopilot-smoke
  --skip-support-bundle-gate
  --skip-autopilot-canary
  --contract-require-gateway-pool true|false   (default: true)
  --contract-require-gateway-policy true|false (default: true)
  --contract-require-rekey-policy true|false   (default: true)
  --contract-schema-version <ver>              (default: 2026-04-19; empty disables version check)

Support bundle gate params:
  --bundle <path>
  --require-signature true|false
  --active-key <key-id=/path/key>         (repeatable)
  --previous-key <key-id=/path/key>       (repeatable)
  --retired-key-id <key-id>               (repeatable)

Autopilot canary params:
  --canary-endpoint <url>                 (default: http://127.0.0.1:19090)
  --canary-unix-socket <path>
  --canary-token-file <path>
  --canary-payload-file <path>
  --canary-lease-owner <owner>            (default: canary-autopilot)
  --canary-lease-ttl <dur>                (default: 60s)
  --canary-timeout <dur>                  (default: 10s)
  --canary-interval <dur>                 (default: 2s)
  --canary-duration <dur>                 (default: 8s)
  --canary-max-steps <n>                  (default: 2)
  --canary-allow-restart true|false       (default: true)
  --canary-continue-on-error true|false   (default: true)

Examples:
  scripts/release_gate_runtime_helper.sh --profile staging \
    --skip-support-bundle-gate

  scripts/release_gate_runtime_helper.sh --profile staging-full \
    --bundle /var/tmp/support-bundle.json \
    --active-key k2=/etc/tun/support-signing-k2.key

  scripts/release_gate_runtime_helper.sh --profile staging-full-strict \
    --bundle /var/tmp/support-bundle.json \
    --active-key k2=/etc/tun/support-signing-k2.key

  scripts/release_gate_runtime_helper.sh --profile ci-fast

  scripts/release_gate_runtime_helper.sh --profile ci-full

  scripts/release_gate_runtime_helper.sh \
    --skip-support-bundle-gate \
    --skip-autopilot-canary

  scripts/release_gate_runtime_helper.sh \
    --bundle ./support-bundle.json \
    --require-signature true \
    --active-key k2=/etc/tun/support-signing-k2.key \
    --canary-unix-socket /run/tun/runtime-helper.sock \
    --canary-token-file /etc/tun/runtime-helper.token \
    --canary-payload-file /etc/tun/bootstrap.json
EOF
}

skip_go_test=false
skip_helper_smoke=false
skip_autopilot_smoke=false
skip_support_bundle_gate=false
skip_autopilot_canary=false

profile="local"
bundle="${SUPPORT_BUNDLE_PATH:-}"
require_signature="${SUPPORT_REQUIRE_SIGNATURE:-true}"
declare -a active_keys=()
declare -a previous_keys=()
declare -a retired_key_ids=()

canary_endpoint="http://127.0.0.1:19090"
canary_unix_socket=""
canary_token_file=""
canary_payload_file=""
canary_lease_owner="canary-autopilot"
canary_lease_ttl="60s"
canary_timeout="10s"
canary_interval="2s"
canary_duration="8s"
canary_max_steps="2"
canary_allow_restart="true"
canary_continue_on_error="true"
contract_require_gateway_pool="true"
contract_require_gateway_policy="true"
contract_require_rekey_policy="true"
contract_schema_version="2026-04-19"
report_file=""
declare -a step_reports=()

apply_profile_defaults() {
  local p="$1"
  case "$p" in
    local)
      # keep script defaults
      ;;
    staging)
      # staging usually validates an already-running helper daemon
      skip_helper_smoke=true
      skip_autopilot_smoke=true
      canary_unix_socket="/run/tun/runtime-helper.sock"
      canary_token_file="/etc/tun/runtime-helper.token"
      canary_payload_file="/etc/tun/bootstrap.json"
      ;;
    staging-full)
      # full staging gate against existing helper + full canary/support stages.
      skip_helper_smoke=true
      skip_autopilot_smoke=true
      skip_support_bundle_gate=false
      skip_autopilot_canary=false
      canary_unix_socket="/run/tun/runtime-helper.sock"
      canary_token_file="/etc/tun/runtime-helper.token"
      canary_payload_file="/etc/tun/bootstrap.json"
      ;;
    staging-full-strict)
      # strict full staging gate: do not skip critical verification stages.
      skip_helper_smoke=true
      skip_autopilot_smoke=true
      skip_support_bundle_gate=false
      skip_autopilot_canary=false
      canary_unix_socket="/run/tun/runtime-helper.sock"
      canary_token_file="/etc/tun/runtime-helper.token"
      canary_payload_file="/etc/tun/bootstrap.json"
      ;;
    ci-fast)
      # fastest CI profile: compile/tests only.
      skip_helper_smoke=true
      skip_autopilot_smoke=true
      skip_support_bundle_gate=true
      skip_autopilot_canary=true
      ;;
    ci-full)
      # full CI profile on ephemeral runners: local smokes enabled, external/staging gates disabled.
      skip_helper_smoke=false
      skip_autopilot_smoke=false
      skip_support_bundle_gate=true
      skip_autopilot_canary=true
      ;;
    *)
      echo "unknown profile: $p (expected local|staging|staging-full|staging-full-strict|ci-fast|ci-full)" >&2
      exit 2
      ;;
  esac
}

# Pre-scan profile so defaults are set before parsing user overrides.
args=("$@")
for ((i=0; i<${#args[@]}; i++)); do
  if [[ "${args[$i]}" == "--profile" ]]; then
    if (( i + 1 >= ${#args[@]} )); then
      echo "missing value for --profile" >&2
      exit 2
    fi
    profile="${args[$((i + 1))]}"
  fi
done
apply_profile_defaults "${profile}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile)
      profile="${2:-}"
      # already applied in pre-scan; keep for explicit validation/visibility
      shift 2
      ;;
    --report-file)
      report_file="${2:-}"
      shift 2
      ;;
    --skip-go-test)
      skip_go_test=true
      shift
      ;;
    --skip-helper-smoke)
      skip_helper_smoke=true
      shift
      ;;
    --skip-autopilot-smoke)
      skip_autopilot_smoke=true
      shift
      ;;
    --skip-support-bundle-gate)
      skip_support_bundle_gate=true
      shift
      ;;
    --skip-autopilot-canary)
      skip_autopilot_canary=true
      shift
      ;;
    --contract-require-gateway-pool)
      contract_require_gateway_pool="${2:-}"
      shift 2
      ;;
    --contract-require-gateway-policy)
      contract_require_gateway_policy="${2:-}"
      shift 2
      ;;
    --contract-require-rekey-policy)
      contract_require_rekey_policy="${2:-}"
      shift 2
      ;;
    --contract-schema-version)
      contract_schema_version="${2:-}"
      shift 2
      ;;
    --bundle)
      bundle="${2:-}"
      shift 2
      ;;
    --require-signature)
      require_signature="${2:-}"
      shift 2
      ;;
    --active-key)
      active_keys+=("${2:-}")
      shift 2
      ;;
    --previous-key)
      previous_keys+=("${2:-}")
      shift 2
      ;;
    --retired-key-id)
      retired_key_ids+=("${2:-}")
      shift 2
      ;;
    --canary-endpoint)
      canary_endpoint="${2:-}"
      shift 2
      ;;
    --canary-unix-socket)
      canary_unix_socket="${2:-}"
      shift 2
      ;;
    --canary-token-file)
      canary_token_file="${2:-}"
      shift 2
      ;;
    --canary-payload-file)
      canary_payload_file="${2:-}"
      shift 2
      ;;
    --canary-lease-owner)
      canary_lease_owner="${2:-}"
      shift 2
      ;;
    --canary-lease-ttl)
      canary_lease_ttl="${2:-}"
      shift 2
      ;;
    --canary-timeout)
      canary_timeout="${2:-}"
      shift 2
      ;;
    --canary-interval)
      canary_interval="${2:-}"
      shift 2
      ;;
    --canary-duration)
      canary_duration="${2:-}"
      shift 2
      ;;
    --canary-max-steps)
      canary_max_steps="${2:-}"
      shift 2
      ;;
    --canary-allow-restart)
      canary_allow_restart="${2:-}"
      shift 2
      ;;
    --canary-continue-on-error)
      canary_continue_on_error="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "${profile}" == "staging-full-strict" ]]; then
  if [[ "${skip_support_bundle_gate}" == "true" ]]; then
    echo "[gate] profile staging-full-strict forbids --skip-support-bundle-gate" >&2
    exit 2
  fi
  if [[ "${skip_autopilot_canary}" == "true" ]]; then
    echo "[gate] profile staging-full-strict forbids --skip-autopilot-canary" >&2
    exit 2
  fi
fi

record_step() {
  local name="$1"
  local status="$2"
  local note="${3:-}"
  step_reports+=("${name}|${status}|${note}")
}

run_step() {
  local step_name="$1"
  shift
  set +e
  "$@"
  local rc=$?
  set -e
  if [[ $rc -ne 0 ]]; then
    record_step "${step_name}" "failed" "exit=${rc}"
    return $rc
  fi
  record_step "${step_name}" "passed" ""
}

write_report() {
  local exit_code="$1"
  [[ -n "${report_file}" ]] || return 0
  local ok="false"
  if [[ "${exit_code}" -eq 0 ]]; then
    ok="true"
  fi
  local now
  now="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  {
    echo "{"
    echo "  \"ok\": ${ok},"
    echo "  \"profile\": \"${profile}\","
    echo "  \"timestamp_utc\": \"${now}\","
    echo "  \"steps\": ["
    local i
    for i in "${!step_reports[@]}"; do
      IFS='|' read -r nm st note <<<"${step_reports[$i]}"
      local comma=","
      if [[ "$i" == "$((${#step_reports[@]} - 1))" ]]; then
        comma=""
      fi
      echo "    {\"name\":\"${nm}\",\"status\":\"${st}\",\"note\":\"${note}\"}${comma}"
    done
    echo "  ]"
    echo "}"
  } > "${report_file}"
}

trap 'write_report "$?"' EXIT

echo "[gate] runtime-helper unified release gate started"
echo "[gate] profile: ${profile}"

if [[ "${skip_go_test}" != "true" ]]; then
  echo "[gate] step 1/5: go test ./..."
  run_step "go-test" go test ./...
else
  echo "[gate] step 1/5: skipped (go test)"
  record_step "go-test" "skipped" ""
fi

if [[ "${skip_helper_smoke}" != "true" ]]; then
  echo "[gate] step 2/5: runtime helper smoke"
  run_step "runtime-helper-smoke" env \
    HELPER_CONTRACT_REQUIRE_GATEWAY_POOL="${contract_require_gateway_pool}" \
    HELPER_CONTRACT_REQUIRE_GATEWAY_POLICY="${contract_require_gateway_policy}" \
    HELPER_CONTRACT_REQUIRE_REKEY_POLICY="${contract_require_rekey_policy}" \
    HELPER_CONTRACT_SCHEMA_VERSION="${contract_schema_version}" \
    ./scripts/runtime_helper_smoke.sh
else
  echo "[gate] step 2/5: skipped (runtime helper smoke)"
  record_step "runtime-helper-smoke" "skipped" ""
fi

if [[ "${skip_autopilot_smoke}" != "true" ]]; then
  echo "[gate] step 3/5: bridge autopilot daemon smoke"
  run_step "autopilot-daemon-smoke" env \
    HELPER_CONTRACT_REQUIRE_GATEWAY_POOL="${contract_require_gateway_pool}" \
    HELPER_CONTRACT_REQUIRE_GATEWAY_POLICY="${contract_require_gateway_policy}" \
    HELPER_CONTRACT_REQUIRE_REKEY_POLICY="${contract_require_rekey_policy}" \
    HELPER_CONTRACT_SCHEMA_VERSION="${contract_schema_version}" \
    ./scripts/runtime_helper_bridge_autopilot_smoke.sh
else
  echo "[gate] step 3/5: skipped (autopilot daemon smoke)"
  record_step "autopilot-daemon-smoke" "skipped" ""
fi

if [[ "${skip_support_bundle_gate}" != "true" ]]; then
  if [[ -z "${bundle}" ]]; then
    echo "[gate] support-bundle gate enabled but bundle path is missing (--bundle)" >&2
    exit 2
  fi
  echo "[gate] step 4/5: support bundle ingest gate"
  declare -a sb_cmd=(
    ./scripts/support_bundle_ingest_gate.sh
    --bundle "${bundle}"
    --require-signature "${require_signature}"
  )
  for spec in "${active_keys[@]}"; do
    [[ -n "${spec}" ]] || continue
    sb_cmd+=(--active-key "${spec}")
  done
  for spec in "${previous_keys[@]}"; do
    [[ -n "${spec}" ]] || continue
    sb_cmd+=(--previous-key "${spec}")
  done
  for key_id in "${retired_key_ids[@]}"; do
    [[ -n "${key_id}" ]] || continue
    sb_cmd+=(--retired-key-id "${key_id}")
  done
  run_step "support-bundle-gate" "${sb_cmd[@]}"
else
  echo "[gate] step 4/5: skipped (support bundle gate)"
  record_step "support-bundle-gate" "skipped" ""
fi

if [[ "${skip_autopilot_canary}" != "true" ]]; then
  if [[ -z "${canary_token_file}" || -z "${canary_payload_file}" ]]; then
    echo "[gate] autopilot canary enabled but --canary-token-file/--canary-payload-file are missing" >&2
    exit 2
  fi
  echo "[gate] step 5/5: bridge autopilot daemon canary"
  declare -a canary_cmd=(
    ./scripts/runtime_helper_bridge_autopilot_canary.sh
    --token-file "${canary_token_file}"
    --payload-file "${canary_payload_file}"
    --lease-owner "${canary_lease_owner}"
    --lease-ttl "${canary_lease_ttl}"
    --timeout "${canary_timeout}"
    --interval "${canary_interval}"
    --duration "${canary_duration}"
    --max-steps "${canary_max_steps}"
    --allow-restart "${canary_allow_restart}"
    --continue-on-error "${canary_continue_on_error}"
    --contract-require-gateway-pool "${contract_require_gateway_pool}"
    --contract-require-gateway-policy "${contract_require_gateway_policy}"
    --contract-require-rekey-policy "${contract_require_rekey_policy}"
    --contract-schema-version "${contract_schema_version}"
  )
  if [[ -n "${canary_unix_socket}" ]]; then
    canary_cmd+=(--unix-socket "${canary_unix_socket}")
  else
    canary_cmd+=(--endpoint "${canary_endpoint}")
  fi
  run_step "autopilot-daemon-canary" "${canary_cmd[@]}"
else
  echo "[gate] step 5/5: skipped (autopilot canary)"
  record_step "autopilot-daemon-canary" "skipped" ""
fi

echo "[gate] runtime-helper unified release gate passed"
