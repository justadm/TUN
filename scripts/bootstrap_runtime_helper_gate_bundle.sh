#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/bootstrap_runtime_helper_gate_bundle.sh [options]

Runs runtime-helper unified gate and packs gate artifacts into a bundle.

Options:
  --out-dir <path>                   Output directory (default: ./artifacts/runtime-helper-gate)
  --profile <name>                   Gate profile (default: staging-full-strict)
  --bundle <path>                    Support bundle path (forwarded to gate)
  --active-key <key-id=/path/key>    Forwarded to gate (repeatable)
  --previous-key <key-id=/path/key>  Forwarded to gate (repeatable)
  --retired-key-id <key-id>          Forwarded to gate (repeatable)
  --canary-unix-socket <path>        Forwarded to gate
  --canary-token-file <path>         Forwarded to gate
  --canary-payload-file <path>       Forwarded to gate
  --gate-arg <arg>                   Additional argument forwarded to gate (repeatable)
  -h, --help                         Show help
EOF
}

out_dir="./artifacts/runtime-helper-gate"
profile="staging-full-strict"
support_bundle=""
declare -a active_keys=()
declare -a previous_keys=()
declare -a retired_key_ids=()
canary_unix_socket=""
canary_token_file=""
canary_payload_file=""
declare -a extra_gate_args=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir)
      out_dir="${2:-}"
      shift 2
      ;;
    --profile)
      profile="${2:-}"
      shift 2
      ;;
    --bundle)
      support_bundle="${2:-}"
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
    --gate-arg)
      extra_gate_args+=("${2:-}")
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

mkdir -p "${out_dir}"
report_file="${out_dir}/runtime-helper-gate-report.json"
log_file="${out_dir}/runtime-helper-gate.log"
manifest_file="${out_dir}/manifest.json"
timestamp="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

declare -a gate_cmd=(
  ./scripts/release_gate_runtime_helper.sh
  --profile "${profile}"
  --report-file "${report_file}"
)

if [[ -n "${support_bundle}" ]]; then
  gate_cmd+=(--bundle "${support_bundle}")
fi
for spec in "${active_keys[@]}"; do
  [[ -n "${spec}" ]] || continue
  gate_cmd+=(--active-key "${spec}")
done
for spec in "${previous_keys[@]}"; do
  [[ -n "${spec}" ]] || continue
  gate_cmd+=(--previous-key "${spec}")
done
for id in "${retired_key_ids[@]}"; do
  [[ -n "${id}" ]] || continue
  gate_cmd+=(--retired-key-id "${id}")
done
if [[ -n "${canary_unix_socket}" ]]; then
  gate_cmd+=(--canary-unix-socket "${canary_unix_socket}")
fi
if [[ -n "${canary_token_file}" ]]; then
  gate_cmd+=(--canary-token-file "${canary_token_file}")
fi
if [[ -n "${canary_payload_file}" ]]; then
  gate_cmd+=(--canary-payload-file "${canary_payload_file}")
fi
for arg in "${extra_gate_args[@]}"; do
  [[ -n "${arg}" ]] || continue
  gate_cmd+=("${arg}")
done

set +e
"${gate_cmd[@]}" | tee "${log_file}"
gate_rc=${PIPESTATUS[0]}
set -e

support_bundle_copy=""
if [[ -n "${support_bundle}" && -f "${support_bundle}" ]]; then
  support_bundle_copy="${out_dir}/support-bundle.json"
  cp "${support_bundle}" "${support_bundle_copy}"
fi

ok="false"
if [[ "${gate_rc}" -eq 0 ]]; then
  ok="true"
fi

{
  echo "{"
  echo "  \"ok\": ${ok},"
  echo "  \"timestamp_utc\": \"${timestamp}\","
  echo "  \"profile\": \"${profile}\","
  echo "  \"gate_exit_code\": ${gate_rc},"
  echo "  \"report_file\": \"${report_file}\","
  echo "  \"log_file\": \"${log_file}\","
  if [[ -n "${support_bundle_copy}" ]]; then
    echo "  \"support_bundle_file\": \"${support_bundle_copy}\""
  else
    echo "  \"support_bundle_file\": \"\""
  fi
  echo "}"
} > "${manifest_file}"

bundle_path="${out_dir}/runtime-helper-gate-artifacts.tar.gz"
declare -a tar_items=(
  "$(basename "${report_file}")"
  "$(basename "${log_file}")"
  "$(basename "${manifest_file}")"
)
if [[ -n "${support_bundle_copy}" ]]; then
  tar_items+=("$(basename "${support_bundle_copy}")")
fi
tar -czf "${bundle_path}" -C "${out_dir}" "${tar_items[@]}"

echo "gate artifacts bundle: ${bundle_path}"

exit "${gate_rc}"
