#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/runtime_helper_contract_check_matrix.sh [options]

Runs helper contract.check matrix against an ephemeral local runtime-helper:
  - current schema version (must pass)
  - next schema version (advisory by default, configurable)

Options:
  --current-schema-version <ver>   default: 2026-04-13
  --next-schema-version <ver>      default: 2026-04-14
  --allow-next-fail true|false     default: true
  --timeout <dur>                  default: 5s
  --transport <tcp|unix>           default: tcp
  --port <n>                       default: 19093 (for tcp transport)
  -h, --help                       show help
EOF
}

current_schema_version="2026-04-19"
next_schema_version="2026-04-20"
allow_next_fail="true"
timeout="5s"
transport="tcp"
port="19093"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --current-schema-version) current_schema_version="${2:-}"; shift 2 ;;
    --next-schema-version) next_schema_version="${2:-}"; shift 2 ;;
    --allow-next-fail) allow_next_fail="${2:-}"; shift 2 ;;
    --timeout) timeout="${2:-}"; shift 2 ;;
    --transport) transport="${2:-}"; shift 2 ;;
    --port) port="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

case "${allow_next_fail}" in
  true|false) ;;
  *) echo "invalid --allow-next-fail: ${allow_next_fail}" >&2; exit 2 ;;
esac

case "${transport}" in
  tcp|unix) ;;
  *) echo "invalid --transport: ${transport}" >&2; exit 2 ;;
esac

tmp_dir="$(mktemp -d)"
socket_path="${tmp_dir}/runtime-helper.sock"
endpoint="http://127.0.0.1:${port}"
state_file="${tmp_dir}/runtime-helper-state.json"
token_file="${tmp_dir}/runtime-helper.token"
bootstrap_file="${tmp_dir}/bootstrap.json"
helper_log="${tmp_dir}/runtime-helper.log"

cleanup() {
  if [[ -n "${helper_pid:-}" ]]; then
    kill "${helper_pid}" >/dev/null 2>&1 || true
    wait "${helper_pid}" >/dev/null 2>&1 || true
  fi
  rm -rf "${tmp_dir}"
}
trap cleanup EXIT

printf 'contract-matrix-token\n' > "${token_file}"
chmod 600 "${token_file}"

cat > "${bootstrap_file}" <<'JSON'
{
  "deviceID": "contract-matrix-device",
  "profileBootstrap": {
    "clientID": "00112233445566778899aabbccddeeff",
    "serverStaticPub": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
    "serverName": "localhost",
    "gateways": [
      {
        "gatewayID": "gw-1",
        "health": "healthy",
        "endpoints": [
          { "addr": "127.0.0.1:8443", "serverName": "localhost" }
        ],
        "hints": { "priority": 10, "loadScore": 10, "rttScore": 10 }
      }
    ],
    "gatewayPolicy": {
      "autoSelectEnabled": true,
      "forceGatewayID": "gw-1",
      "stickyDuration": 1000000000,
      "cooldownMin": 1000000000,
      "cooldownMax": 2000000000
    },
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

declare -a helper_cmd=(go run ./cmd/runtime-helper -state-file "${state_file}" -auth-token-file "${token_file}")
declare -a ctl_base=(go run ./cmd/runtime-helperctl -token-file "${token_file}" -timeout "${timeout}")

if [[ "${transport}" == "unix" ]]; then
  helper_cmd+=(-unix-socket "${socket_path}")
  ctl_base+=(-unix-socket "${socket_path}")
else
  helper_cmd+=(-listen "127.0.0.1:${port}")
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

run_contract_check() {
  local version="$1"
  "${ctl_base[@]}" \
    -action contract.check \
    --require-gateway-pool \
    --require-gateway-policy \
    --require-rekey-policy \
    --require-bootstrap-schema-version "${version}" \
    -payload-file "${bootstrap_file}" >/dev/null
}

echo "[contract-matrix] current schema check: ${current_schema_version}"
run_contract_check "${current_schema_version}"
echo "[contract-matrix] current schema check passed"

if [[ -n "${next_schema_version}" ]]; then
  echo "[contract-matrix] next schema check: ${next_schema_version}"
  if run_contract_check "${next_schema_version}"; then
    echo "[contract-matrix] next schema check passed"
  else
    if [[ "${allow_next_fail}" == "true" ]]; then
      echo "[contract-matrix] next schema check failed (allowed)"
    else
      echo "[contract-matrix] next schema check failed (not allowed)" >&2
      exit 1
    fi
  fi
fi

echo "runtime helper contract matrix passed"
