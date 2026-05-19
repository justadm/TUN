#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/runtime_helper_bridge_autopilot_canary.sh [options]

Canary-gate for runtime-helper bridge autopilot daemon mode.
Runs helperctl bridge.autopilot.daemon against an existing helper endpoint and
validates schema/payload contract before startup and validates JSON tick output shape.

Options:
  --endpoint <url>          Helper endpoint (default: http://127.0.0.1:19090)
  --unix-socket <path>      Use unix socket instead of HTTP endpoint
  --token-file <path>       Helper API token file (required)
  --payload-file <path>     Bootstrap payload file for startup/restart plans (required)
  --lease-owner <owner>     Lease owner (default: canary-autopilot)
  --lease-ttl <dur>         Lease TTL (default: 60s)
  --timeout <dur>           Helper request timeout (default: 10s)
  --interval <dur>          Daemon interval (default: 2s)
  --duration <dur>          Daemon run duration (default: 8s)
  --max-steps <n>           Autopilot max steps per run (default: 2)
  --allow-restart <bool>    Allow restart in autopilot (default: true)
  --continue-on-error <b>   Continue daemon loop on errors (default: true)
  --contract-require-gateway-pool true|false   (default: true)
  --contract-require-gateway-policy true|false (default: true)
  --contract-require-rekey-policy true|false   (default: true)
  --contract-schema-version <ver>              (default: 2026-04-13; empty disables version check)
  -h, --help                Show help
EOF
}

endpoint="http://127.0.0.1:19090"
unix_socket=""
token_file=""
payload_file=""
lease_owner="canary-autopilot"
lease_ttl="60s"
timeout="10s"
interval="2s"
duration="8s"
max_steps="2"
allow_restart="true"
continue_on_error="true"
contract_require_gateway_pool="true"
contract_require_gateway_policy="true"
contract_require_rekey_policy="true"
contract_schema_version="2026-04-13"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --endpoint)
      endpoint="${2:-}"
      shift 2
      ;;
    --unix-socket)
      unix_socket="${2:-}"
      shift 2
      ;;
    --token-file)
      token_file="${2:-}"
      shift 2
      ;;
    --payload-file)
      payload_file="${2:-}"
      shift 2
      ;;
    --lease-owner)
      lease_owner="${2:-}"
      shift 2
      ;;
    --lease-ttl)
      lease_ttl="${2:-}"
      shift 2
      ;;
    --timeout)
      timeout="${2:-}"
      shift 2
      ;;
    --interval)
      interval="${2:-}"
      shift 2
      ;;
    --duration)
      duration="${2:-}"
      shift 2
      ;;
    --max-steps)
      max_steps="${2:-}"
      shift 2
      ;;
    --allow-restart)
      allow_restart="${2:-}"
      shift 2
      ;;
    --continue-on-error)
      continue_on_error="${2:-}"
      shift 2
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

if [[ -z "${token_file}" ]]; then
  echo "--token-file is required" >&2
  exit 2
fi
if [[ ! -f "${token_file}" ]]; then
  echo "token file not found: ${token_file}" >&2
  exit 2
fi
if [[ -z "${payload_file}" ]]; then
  echo "--payload-file is required" >&2
  exit 2
fi
if [[ ! -f "${payload_file}" ]]; then
  echo "payload file not found: ${payload_file}" >&2
  exit 2
fi

tmp_dir="$(mktemp -d)"
out_file="${tmp_dir}/autopilot-daemon.out"
cleanup() {
  rm -rf "${tmp_dir}"
}
trap cleanup EXIT

declare -a contract_cmd=(
  go run ./cmd/runtime-helperctl
  -action contract.check
  -token-file "${token_file}"
  -payload-file "${payload_file}"
  -timeout "${timeout}"
)
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
if [[ -n "${unix_socket}" ]]; then
  contract_cmd+=(-unix-socket "${unix_socket}")
else
  contract_cmd+=(-endpoint "${endpoint}")
fi
"${contract_cmd[@]}" >/dev/null

declare -a cmd=(
  go run ./cmd/runtime-helperctl
  -action bridge.autopilot.daemon
  -token-file "${token_file}"
  -payload-file "${payload_file}"
  -lease-owner "${lease_owner}"
  -lease-ttl "${lease_ttl}"
  -timeout "${timeout}"
  -bridge-autopilot-max-steps "${max_steps}"
  -bridge-autopilot-allow-restart="${allow_restart}"
  -bridge-autopilot-interval "${interval}"
  -bridge-autopilot-duration "${duration}"
  -bridge-autopilot-continue-on-error="${continue_on_error}"
  -bridge-reconcile-ensure-lease=true
  -bridge-wait=true
  -wait-state established
  -wait-timeout 20s
)
if [[ -n "${unix_socket}" ]]; then
  cmd+=(-unix-socket "${unix_socket}")
else
  cmd+=(-endpoint "${endpoint}")
fi

"${cmd[@]}" > "${out_file}"

tick_count="$(wc -l < "${out_file}" | tr -d ' ')"
if [[ "${tick_count}" -lt 1 ]]; then
  echo "autopilot daemon produced no ticks" >&2
  cat "${out_file}" >&2 || true
  exit 1
fi

if ! rg -q '"run":' "${out_file}" || ! rg -q '"at":' "${out_file}" || ! rg -q '"autopilot":' "${out_file}"; then
  echo "autopilot daemon output shape mismatch (missing run/at/autopilot)" >&2
  cat "${out_file}" >&2 || true
  exit 1
fi

echo "runtime helper bridge autopilot canary passed"
