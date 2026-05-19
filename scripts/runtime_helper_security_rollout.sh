#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/runtime_helper_security_rollout.sh [options]

Options:
  --endpoint <url>         helper endpoint (default: http://127.0.0.1:19090)
  --token-file <path>      helper token file (optional)
  --default-profile <p>    balanced|strict|permissive (default: balanced)
  --strict-tenants <list>  comma-separated tenant IDs for strict profile
  --timeout <dur>          helperctl timeout (default: 5s)
EOF
}

endpoint="http://127.0.0.1:19090"
token_file=""
default_profile="balanced"
strict_tenants=""
timeout="5s"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --endpoint) endpoint="${2:-}"; shift 2 ;;
    --token-file) token_file="${2:-}"; shift 2 ;;
    --default-profile) default_profile="${2:-}"; shift 2 ;;
    --strict-tenants) strict_tenants="${2:-}"; shift 2 ;;
    --timeout) timeout="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ "${default_profile}" != "balanced" && "${default_profile}" != "strict" && "${default_profile}" != "permissive" ]]; then
  echo "invalid --default-profile: ${default_profile}" >&2
  exit 2
fi

tmp_payload="$(mktemp)"
trap 'rm -f "${tmp_payload}"' EXIT

if [[ -n "${strict_tenants}" ]]; then
  IFS=',' read -r -a arr <<< "${strict_tenants}"
else
  arr=()
fi

{
  printf '{\n'
  printf '  "defaultProfile": "%s",\n' "${default_profile}"
  printf '  "strictTenants": ['
  first=true
  for t in "${arr[@]}"; do
    t="$(echo "${t}" | xargs)"
    [[ -z "${t}" ]] && continue
    if [[ "${first}" == true ]]; then
      first=false
    else
      printf ', '
    fi
    printf '"%s"' "${t}"
  done
  printf ']\n'
  printf '}\n'
} > "${tmp_payload}"

declare -a ctl=(go run ./cmd/runtime-helperctl -endpoint "${endpoint}" -timeout "${timeout}")
if [[ -n "${token_file}" ]]; then
  ctl+=(-token-file "${token_file}")
fi

echo "[security-rollout] apply policy rollout"
"${ctl[@]}" -action security.policy.rollout -payload-file "${tmp_payload}"
echo "[security-rollout] read default policy"
"${ctl[@]}" -action security.policy.get
