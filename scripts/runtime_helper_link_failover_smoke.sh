#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/runtime_helper_link_failover_smoke.sh --link-id <id> --gateway-id <id> [options]

Options:
  --endpoint <url>       helper endpoint (default: http://127.0.0.1:19090)
  --token-file <path>    helper token file (optional)
  --timeout <duration>   helperctl timeout (default: 5s)
  --request-id <id>      request id prefix for idempotent failover flow (default: smoke-failover)
EOF
}

link_id=""
gateway_id=""
endpoint="http://127.0.0.1:19090"
token_file=""
timeout="5s"
request_id="smoke-failover"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --link-id)
      link_id="${2:-}"
      shift 2
      ;;
    --gateway-id)
      gateway_id="${2:-}"
      shift 2
      ;;
    --endpoint)
      endpoint="${2:-}"
      shift 2
      ;;
    --token-file)
      token_file="${2:-}"
      shift 2
      ;;
    --timeout)
      timeout="${2:-}"
      shift 2
      ;;
    --request-id)
      request_id="${2:-}"
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

if [[ -z "${link_id}" ]]; then
  echo "--link-id is required" >&2
  exit 2
fi
if [[ -z "${gateway_id}" ]]; then
  echo "--gateway-id is required" >&2
  exit 2
fi

declare -a ctl=(go run ./cmd/runtime-helperctl -endpoint "${endpoint}" -timeout "${timeout}")
if [[ -n "${token_file}" ]]; then
  ctl+=(-token-file "${token_file}")
fi

echo "[failover-smoke] executing link.failover link_id=${link_id} gateway_id=${gateway_id}"
out="$("${ctl[@]}" \
  -action link.failover \
  -link-id "${link_id}" \
  -gateway-id "${gateway_id}" \
  -request-id "${request_id}")"
echo "[failover-smoke] response: ${out}"

read_out="$("${ctl[@]}" -action link.read -link-id "${link_id}")"
echo "[failover-smoke] link.read: ${read_out}"
if ! printf '%s' "${read_out}" | rg -q "\"gatewayID\":\"${gateway_id}\""; then
  echo "[failover-smoke] expected gatewayID=${gateway_id} in link.read output" >&2
  exit 1
fi

echo "[failover-smoke] passed"
