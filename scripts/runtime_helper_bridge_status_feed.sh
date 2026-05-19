#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/runtime_helper_bridge_status_feed.sh [options]

Starts machine-friendly bridge status feed from runtime-helper:
  1) contract.check (schema/api guard)
  2) bridge.status.stream (JSONL + retry by default)

Options:
  --endpoint <url>                 helper endpoint (default: http://127.0.0.1:19090)
  --unix-socket <path>             optional helper unix socket path
  --token-file <path>              optional helper token file
  --timeout <dur>                  helperctl timeout (default: 5s)
  --lease-owner <owner>            lease owner for contract/check context (default: feed-monitor)
  --stream-interval <dur>          bridge status stream interval (default: 5s)
  --stream-duration <dur>          bridge status stream duration (default: 0s, unlimited)
  --retry true|false               reconnect stream on unexpected EOF (default: true)
  --retry-max <n>                  max reconnect attempts, 0 = unlimited (default: 0)
  --retry-backoff-min <dur>        reconnect min backoff (default: 500ms)
  --retry-backoff-max <dur>        reconnect max backoff (default: 10s)
  --jsonl true|false               normalized JSONL envelope output (default: true)
  --request-id <id>                optional X-Request-ID
  --require-gateway-pool true|false      default: true
  --require-gateway-policy true|false    default: true
  --require-rekey-policy true|false      default: true
  --require-gateway-pool-version <ver>   default: 2026-04-10
  --require-bootstrap-schema-version <v> default: 2026-04-13
  -h, --help                       show help
EOF
}

endpoint="http://127.0.0.1:19090"
unix_socket=""
token_file=""
timeout="5s"
lease_owner="feed-monitor"
stream_interval="5s"
stream_duration="0s"
retry="true"
retry_max="0"
retry_backoff_min="500ms"
retry_backoff_max="10s"
jsonl="true"
request_id=""
require_gateway_pool="true"
require_gateway_policy="true"
require_rekey_policy="true"
require_gateway_pool_version="2026-04-10"
require_bootstrap_schema_version="2026-04-13"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --endpoint) endpoint="${2:-}"; shift 2 ;;
    --unix-socket) unix_socket="${2:-}"; shift 2 ;;
    --token-file) token_file="${2:-}"; shift 2 ;;
    --timeout) timeout="${2:-}"; shift 2 ;;
    --lease-owner) lease_owner="${2:-}"; shift 2 ;;
    --stream-interval) stream_interval="${2:-}"; shift 2 ;;
    --stream-duration) stream_duration="${2:-}"; shift 2 ;;
    --retry) retry="${2:-}"; shift 2 ;;
    --retry-max) retry_max="${2:-}"; shift 2 ;;
    --retry-backoff-min) retry_backoff_min="${2:-}"; shift 2 ;;
    --retry-backoff-max) retry_backoff_max="${2:-}"; shift 2 ;;
    --jsonl) jsonl="${2:-}"; shift 2 ;;
    --request-id) request_id="${2:-}"; shift 2 ;;
    --require-gateway-pool) require_gateway_pool="${2:-}"; shift 2 ;;
    --require-gateway-policy) require_gateway_policy="${2:-}"; shift 2 ;;
    --require-rekey-policy) require_rekey_policy="${2:-}"; shift 2 ;;
    --require-gateway-pool-version) require_gateway_pool_version="${2:-}"; shift 2 ;;
    --require-bootstrap-schema-version) require_bootstrap_schema_version="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

for v in retry jsonl require_gateway_pool require_gateway_policy require_rekey_policy; do
  case "${!v}" in
    true|false) ;;
    *)
      echo "invalid boolean for --${v//_/-}: ${!v}" >&2
      exit 2
      ;;
  esac
done

ctl=(go run ./cmd/runtime-helperctl -timeout "${timeout}")
if [[ -n "${token_file}" ]]; then
  ctl+=(-token-file "${token_file}")
fi
if [[ -n "${unix_socket}" ]]; then
  ctl+=(-unix-socket "${unix_socket}")
else
  ctl+=(-endpoint "${endpoint}")
fi
if [[ -n "${request_id}" ]]; then
  ctl+=(-request-id "${request_id}")
fi

contract_cmd=("${ctl[@]}" -action contract.check)
if [[ "${require_gateway_pool}" == "true" ]]; then
  contract_cmd+=(--require-gateway-pool)
fi
if [[ "${require_gateway_policy}" == "true" ]]; then
  contract_cmd+=(--require-gateway-policy)
fi
if [[ "${require_rekey_policy}" == "true" ]]; then
  contract_cmd+=(--require-rekey-policy)
fi
if [[ -n "${require_gateway_pool_version}" ]]; then
  contract_cmd+=(--require-gateway-pool-version "${require_gateway_pool_version}")
fi
if [[ -n "${require_bootstrap_schema_version}" ]]; then
  contract_cmd+=(--require-bootstrap-schema-version "${require_bootstrap_schema_version}")
fi

"${contract_cmd[@]}" >/dev/null

"${ctl[@]}" \
  -action bridge.status.stream \
  -lease-owner "${lease_owner}" \
  -bridge-status-interval "${stream_interval}" \
  -bridge-status-duration "${stream_duration}" \
  -bridge-status-jsonl="${jsonl}" \
  -bridge-status-retry="${retry}" \
  -bridge-status-retry-max "${retry_max}" \
  -bridge-status-retry-backoff-min "${retry_backoff_min}" \
  -bridge-status-retry-backoff-max "${retry_backoff_max}"
