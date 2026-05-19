#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/runtime_helper_staging_strict_gate_remote.sh --host <ssh-host> [options]

Runs runtime-helper strict staging gate on a remote host over SSH:
  ./scripts/release_gate_runtime_helper.sh --profile staging-full-strict ...

Options:
  --host <ssh-host>                SSH host alias or user@host (required)
  --remote-repo <path>             Remote repo path (default: /home/just/projects/TUN)
  --bundle <path>                  Support bundle path (default: /var/tmp/support-bundle.json)
  --active-key <id=path>           Active signing key spec (default: k2=/etc/tun/support-signing-k2.key)
  --report-file <path>             Gate report file path (default: /var/tmp/runtime-helper-gate-report.json)
  --require-signature true|false   Verify signature (default: true)
  --contract-schema-version <ver>  Contract schema version (default: 2026-04-13)
  --contract-require-rekey-policy true|false (default: true)
  --skip-go-test                   Pass --skip-go-test to remote gate
  -h, --help                       Show help
EOF
}

host=""
remote_repo="/home/just/projects/TUN"
bundle="/var/tmp/support-bundle.json"
active_key="k2=/etc/tun/support-signing-k2.key"
report_file="/var/tmp/runtime-helper-gate-report.json"
require_signature="true"
contract_schema_version="2026-04-13"
contract_require_rekey_policy="true"
skip_go_test="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) host="${2:-}"; shift 2 ;;
    --remote-repo) remote_repo="${2:-}"; shift 2 ;;
    --bundle) bundle="${2:-}"; shift 2 ;;
    --active-key) active_key="${2:-}"; shift 2 ;;
    --report-file) report_file="${2:-}"; shift 2 ;;
    --require-signature) require_signature="${2:-}"; shift 2 ;;
    --contract-schema-version) contract_schema_version="${2:-}"; shift 2 ;;
    --contract-require-rekey-policy) contract_require_rekey_policy="${2:-}"; shift 2 ;;
    --skip-go-test) skip_go_test="true"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ -z "${host}" ]]; then
  echo "--host is required" >&2
  usage >&2
  exit 2
fi

if [[ "${require_signature}" != "true" && "${require_signature}" != "false" ]]; then
  echo "invalid --require-signature: ${require_signature}" >&2
  exit 2
fi

if [[ "${contract_require_rekey_policy}" != "true" && "${contract_require_rekey_policy}" != "false" ]]; then
  echo "invalid --contract-require-rekey-policy: ${contract_require_rekey_policy}" >&2
  exit 2
fi

echo "[remote-gate] host=${host}"
echo "[remote-gate] repo=${remote_repo}"
echo "[remote-gate] bundle=${bundle}"
echo "[remote-gate] report=${report_file}"

ssh "${host}" "test -d '${remote_repo}'"
ssh "${host}" "test -x '${remote_repo}/scripts/release_gate_runtime_helper.sh'"
ssh "${host}" "test -S /run/tun/runtime-helper.sock"
ssh "${host}" "test -f /etc/tun/runtime-helper.token"
ssh "${host}" "test -f /etc/tun/bootstrap.json"
ssh "${host}" "test -f '${bundle}'"

active_key_path="${active_key#*=}"
if [[ "${active_key}" != *"="* || -z "${active_key_path}" ]]; then
  echo "invalid --active-key format, expected id=/path/to/key" >&2
  exit 2
fi
ssh "${host}" "test -f '${active_key_path}'"

declare -a remote_gate=(
  "${remote_repo}/scripts/release_gate_runtime_helper.sh"
  "--profile" "staging-full-strict"
  "--bundle" "${bundle}"
  "--active-key" "${active_key}"
  "--require-signature" "${require_signature}"
  "--contract-require-rekey-policy" "${contract_require_rekey_policy}"
  "--contract-schema-version" "${contract_schema_version}"
  "--report-file" "${report_file}"
)
if [[ "${skip_go_test}" == "true" ]]; then
  remote_gate+=("--skip-go-test")
fi

remote_cmd="$(printf "cd '%s' && %q" "${remote_repo}" "${remote_gate[0]}")"
for ((i=1; i<${#remote_gate[@]}; i++)); do
  remote_cmd+=" $(printf "%q" "${remote_gate[$i]}")"
done

echo "[remote-gate] running strict gate on ${host}"
ssh "${host}" "${remote_cmd}"
echo "[remote-gate] success on ${host}"
echo "[remote-gate] report: ${report_file}"
