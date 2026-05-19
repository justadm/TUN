#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/runtime_helper_smoke.sh [--full]

Default mode:
  Runs helper contract smoke on an ephemeral local helper instance (TCP by default).
  Verifies:
    schema -> contract.check -> status -> lease.takeover|lease.acquire -> bootstrap.apply -> tunnel.start -> stats.read -> diagnostics.export
  In default mode, tunnel.start failure is allowed (expected in non-privileged/dev env).

--full mode:
  Requires helper bootstrap payload from HELPER_BOOTSTRAP_FILE and expects tunnel.start to stay up.

Environment:
  HELPER_BOOTSTRAP_FILE   JSON payload for bootstrap.apply (required in --full mode)
  HELPER_TIMEOUT_SEC      Request timeout seconds (default: 5)
  HELPER_SMOKE_TRANSPORT  tcp|unix (default: tcp)
  HELPER_SMOKE_PORT       tcp port for helper smoke (default: 19091)
  HELPER_LEASE_OWNER      lease owner used by smoke flow (default: smoke)
  HELPER_CONTRACT_REQUIRE_GATEWAY_POOL   true|false (default: true)
  HELPER_CONTRACT_REQUIRE_GATEWAY_POLICY true|false (default: true)
  HELPER_CONTRACT_REQUIRE_REKEY_POLICY   true|false (default: true)
  HELPER_CONTRACT_SCHEMA_VERSION         expected bootstrap schema version (default: 2026-04-13)
EOF
}

full_mode=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --full)
      full_mode=true
      shift
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

timeout_sec="${HELPER_TIMEOUT_SEC:-5}"
tmp_dir="$(mktemp -d)"
socket_path="${tmp_dir}/runtime-helper.sock"
transport="${HELPER_SMOKE_TRANSPORT:-tcp}"
smoke_port="${HELPER_SMOKE_PORT:-19091}"
endpoint="http://127.0.0.1:${smoke_port}"
state_file="${tmp_dir}/runtime-helper-state.json"
token_file="${tmp_dir}/runtime-helper.token"
bundle_file="${tmp_dir}/bundle.json"
bootstrap_file="${tmp_dir}/bootstrap.json"
helper_log="${tmp_dir}/runtime-helper.log"
lease_owner="${HELPER_LEASE_OWNER:-smoke}"
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

if [[ -n "${HELPER_BOOTSTRAP_FILE:-}" ]]; then
  cp "${HELPER_BOOTSTRAP_FILE}" "${bootstrap_file}"
else
  cat > "${bootstrap_file}" <<'JSON'
{
  "deviceID": "smoke-device-01",
  "profileBootstrap": {
    "addr": "127.0.0.1:8443",
    "serverName": "localhost",
    "insecure": true,
    "clientID": "00112233445566778899aabbccddeeff",
    "serverStaticPub": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
    "rekeyPolicy": {
      "enabled": true,
      "ackRetries": 2,
      "ackRetryDelay": 200000000,
      "initInterval": 5000000000,
      "initAckTimeout": 3000000000,
      "initRetries": 1,
      "initRetryDelay": 250000000,
      "initOverlap": 2500000000
    }
  }
}
JSON
fi

if [[ "${full_mode}" == "true" && ! -f "${bootstrap_file}" ]]; then
  echo "--full requires HELPER_BOOTSTRAP_FILE" >&2
  exit 2
fi

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

"${ctl_base[@]}" -action schema >/dev/null
declare -a contract_cmd=("${ctl_base[@]}" -action contract.check -payload-file "${bootstrap_file}")
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

status_json="$("${ctl_base[@]}" -action status)"
lease_json="$("${ctl_base[@]}" -action lease.ensure -lease-owner "${lease_owner}" -lease-ttl "${timeout_sec}s")"
lease_id="$(printf '%s' "${lease_json}" | tr -d '\n' | sed -n 's/.*"leaseId":"\([^"]*\)".*/\1/p')"
if [[ -z "${lease_id}" ]]; then
  echo "failed to parse leaseId from lease action output" >&2
  exit 1
fi

status_json="$("${ctl_base[@]}" -action status)"
if ! printf '%s' "${status_json}" | rg -q "\"leaseId\":\"${lease_id}\""; then
  echo "status does not expose current lease snapshot" >&2
  exit 1
fi

if [[ "${full_mode}" == "true" ]]; then
  "${ctl_base[@]}" \
    -action bridge.startup \
    -lease-owner "${lease_owner}" \
    -lease-ttl "${timeout_sec}s" \
    -bridge-wait=true \
    -wait-state established \
    -wait-timeout "${timeout_sec}s" \
    -payload-file "${bootstrap_file}" >/dev/null
else
  set +e
  "${ctl_base[@]}" \
    -action bridge.startup \
    -lease-owner "${lease_owner}" \
    -lease-ttl "${timeout_sec}s" \
    -bridge-wait=false \
    -payload-file "${bootstrap_file}" >/dev/null 2>&1
  start_rc=$?
  set -e
  if [[ "${start_rc}" -ne 0 ]]; then
    echo "bridge.startup failed in default mode (expected on unsupported preflight or non-privileged tunnel start)" >&2
  fi
fi

"${ctl_base[@]}" \
  -action bridge.reconcile \
  -lease-owner "${lease_owner}" \
  -lease-ttl "${timeout_sec}s" >/dev/null

stats_json="$("${ctl_base[@]}" -action stats.read -lease-id "${lease_id}")"
if ! printf '%s' "${stats_json}" | rg -q '"rekey"'; then
  echo "stats.read does not expose rekey aggregate block" >&2
  exit 1
fi
"${ctl_base[@]}" -action lease.heartbeat -lease-id "${lease_id}" -lease-ttl "${timeout_sec}s" >/dev/null

"${ctl_base[@]}" -action diagnostics.export -lease-id "${lease_id}" > "${bundle_file}"

if ! rg -q '"envelope_version"' "${bundle_file}"; then
  echo "diagnostics.export did not return support bundle envelope" >&2
  exit 1
fi

"${ctl_base[@]}" \
  -action bridge.shutdown \
  -lease-owner "${lease_owner}" \
  -lease-ttl "${timeout_sec}s" \
  -bridge-shutdown-best-effort=true >/dev/null

echo "runtime helper smoke passed"
