#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/runtime_helper_bridge_autopilot_smoke.sh

Runs bridge.autopilot.daemon contract smoke on an ephemeral local helper instance.
Validates schema/contract availability and that daemon mode emits JSON ticks with required fields.

Environment:
  HELPER_TIMEOUT_SEC      Request timeout seconds (default: 5)
  HELPER_SMOKE_TRANSPORT  tcp|unix (default: tcp)
  HELPER_SMOKE_PORT       tcp port for helper smoke (default: 19092)
  HELPER_LEASE_OWNER      lease owner for bridge actions (default: smoke-autopilot)
  HELPER_CONTRACT_REQUIRE_GATEWAY_POOL   true|false (default: true)
  HELPER_CONTRACT_REQUIRE_GATEWAY_POLICY true|false (default: true)
  HELPER_CONTRACT_REQUIRE_REKEY_POLICY   true|false (default: true)
  HELPER_CONTRACT_SCHEMA_VERSION         expected bootstrap schema version (default: 2026-04-13)
EOF
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

timeout_sec="${HELPER_TIMEOUT_SEC:-5}"
tmp_dir="$(mktemp -d)"
socket_path="${tmp_dir}/runtime-helper.sock"
transport="${HELPER_SMOKE_TRANSPORT:-tcp}"
smoke_port="${HELPER_SMOKE_PORT:-19092}"
endpoint="http://127.0.0.1:${smoke_port}"
state_file="${tmp_dir}/runtime-helper-state.json"
token_file="${tmp_dir}/runtime-helper.token"
helper_log="${tmp_dir}/runtime-helper.log"
daemon_out="${tmp_dir}/daemon.out"
lease_owner="${HELPER_LEASE_OWNER:-smoke-autopilot}"
contract_require_gateway_pool="${HELPER_CONTRACT_REQUIRE_GATEWAY_POOL:-true}"
contract_require_gateway_policy="${HELPER_CONTRACT_REQUIRE_GATEWAY_POLICY:-true}"
contract_require_rekey_policy="${HELPER_CONTRACT_REQUIRE_REKEY_POLICY:-true}"
contract_schema_version="${HELPER_CONTRACT_SCHEMA_VERSION:-2026-04-19}"

cleanup() {
  if [[ -n "${helper_pid:-}" ]]; then
    kill "${helper_pid}" >/dev/null 2>&1 || true
    wait "${helper_pid}" >/dev/null 2>&1 || true
  fi
  rm -rf "${tmp_dir}"
}
trap cleanup EXIT

printf 'helper-smoke-token\n' > "${token_file}"
chmod 600 "${token_file}"

declare -a helper_cmd=(go run ./cmd/runtime-helper -state-file "${state_file}" -auth-token-file "${token_file}")
declare -a ctl_base=(go run ./cmd/runtime-helperctl -token-file "${token_file}" -timeout "${timeout_sec}s")

if [[ "${transport}" == "unix" ]]; then
  helper_cmd+=(-unix-socket "${socket_path}")
  ctl_base+=(-unix-socket "${socket_path}")
else
  helper_cmd+=(-listen "127.0.0.1:${smoke_port}")
  ctl_base+=(-endpoint "${endpoint}")
fi

"${helper_cmd[@]}" >"${helper_log}" 2>&1 &
helper_pid=$!

ready=false
for _ in $(seq 1 120); do
  if "${ctl_base[@]}" -action schema >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.5
done

if [[ "${ready}" != "true" ]]; then
  echo "helper did not become ready; log:" >&2
  tail -n 50 "${helper_log}" >&2 || true
  exit 1
fi

declare -a contract_cmd=("${ctl_base[@]}" -action contract.check)
if [[ "${contract_require_gateway_pool}" == "true" ]]; then
  contract_cmd+=(--require-gateway-pool)
fi
if [[ "${contract_require_gateway_policy}" == "true" ]]; then
  contract_cmd+=(--require-gateway-policy)
fi
if [[ "${contract_require_rekey_policy}" == "true" ]]; then
  contract_cmd+=(--require-rekey-policy)
fi
if [[ -n "${contract_schema_version}" ]]; then
  contract_cmd+=(--require-bootstrap-schema-version "${contract_schema_version}")
fi
"${contract_cmd[@]}" >/dev/null

"${ctl_base[@]}" \
  -action bridge.autopilot.daemon \
  -lease-owner "${lease_owner}" \
  -lease-ttl "${timeout_sec}s" \
  -bridge-reconcile-ensure-lease=true \
  -bridge-autopilot-max-steps 2 \
  -bridge-autopilot-allow-restart=true \
  -bridge-autopilot-interval 1s \
  -bridge-autopilot-duration 3s \
  -bridge-autopilot-continue-on-error=true > "${daemon_out}"

tick_count="$(wc -l < "${daemon_out}" | tr -d ' ')"
if [[ "${tick_count}" -lt 2 ]]; then
  echo "expected at least 2 daemon ticks, got ${tick_count}" >&2
  cat "${daemon_out}" >&2
  exit 1
fi

if ! rg -q '"run":' "${daemon_out}" || ! rg -q '"at":' "${daemon_out}" || ! rg -q '"autopilot":' "${daemon_out}"; then
  echo "daemon output does not contain required JSON fields" >&2
  cat "${daemon_out}" >&2
  exit 1
fi

echo "runtime helper bridge autopilot smoke passed"
