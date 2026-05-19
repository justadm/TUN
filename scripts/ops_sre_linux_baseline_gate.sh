#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/ops_sre_linux_baseline_gate.sh [options]

Linux host baseline gate for tun-rnd ops/security/SRE readiness.

Checks:
  1) supported OS/version matrix
  2) required files exist + secure permissions
  3) systemd units enabled/active (unless skipped)
  4) helper socket health/auth contract
  5) helper bridge status stream sanity

Options:
  --helper-socket <path>       default: /run/tun/runtime-helper.sock
  --helper-token-file <path>   default: /etc/tun/runtime-helper.token
  --bootstrap-file <path>      default: /etc/tun/bootstrap.json
  --support-key-file <path>    support signing key file to validate permissions (repeatable)
  --unit <name>                systemd unit to check (repeatable)
  --replace-units              ignore default units and use only explicit --unit entries
  --allow-ubuntu <version>     allowed Ubuntu version prefix (repeatable; default: 20.04,22.04,24.04)
  --skip-unit-check            skip enabled/active checks for units
  --skip-os-check              skip OS/version matrix check
  --skip-helper-check          skip helper schema/status/stream checks
  -h, --help                   show this help
EOF
}

helper_socket="/run/tun/runtime-helper.sock"
helper_token_file="/etc/tun/runtime-helper.token"
bootstrap_file="/etc/tun/bootstrap.json"
declare -a support_key_files=()
declare -a units=(
  "tun-runtime-helper.service"
  "tun-runtime-helper-autopilot.service"
  "tun-runtime-server.service"
)
declare -a allowed_ubuntu=("20.04" "22.04" "24.04")
skip_unit_check=false
skip_os_check=false
skip_helper_check=false
replace_units=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --helper-socket)
      helper_socket="${2:-}"
      shift 2
      ;;
    --helper-token-file)
      helper_token_file="${2:-}"
      shift 2
      ;;
    --bootstrap-file)
      bootstrap_file="${2:-}"
      shift 2
      ;;
    --support-key-file)
      support_key_files+=("${2:-}")
      shift 2
      ;;
    --unit)
      units+=("${2:-}")
      shift 2
      ;;
    --replace-units)
      replace_units=true
      shift
      ;;
    --allow-ubuntu)
      allowed_ubuntu+=("${2:-}")
      shift 2
      ;;
    --skip-unit-check)
      skip_unit_check=true
      shift
      ;;
    --skip-os-check)
      skip_os_check=true
      shift
      ;;
    --skip-helper-check)
      skip_helper_check=true
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

if [[ "${replace_units}" == "true" ]]; then
  units=("${units[@]:3}")
fi

have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

file_mode() {
  local p="$1"
  if stat -c "%a" "$p" >/dev/null 2>&1; then
    stat -c "%a" "$p"
    return 0
  fi
  stat -f "%Lp" "$p"
}

assert_os_matrix() {
  local id version
  id=""
  version=""
  if [[ -r /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    id="${ID:-}"
    version="${VERSION_ID:-}"
  fi
  if [[ "${id}" != "ubuntu" ]]; then
    echo "[gate] unsupported OS id: ${id:-unknown} (expected ubuntu)" >&2
    exit 1
  fi
  local allowed=false
  for v in "${allowed_ubuntu[@]}"; do
    [[ -n "${v}" ]] || continue
    if [[ "${version}" == "${v}"* ]]; then
      allowed=true
      break
    fi
  done
  if [[ "${allowed}" != "true" ]]; then
    echo "[gate] unsupported Ubuntu version: ${version:-unknown}; allowed: ${allowed_ubuntu[*]}" >&2
    exit 1
  fi
  echo "[gate] os check: ubuntu ${version} allowed"
}

assert_secure_file() {
  local p="$1"
  local name="$2"
  if [[ ! -f "$p" ]]; then
    echo "[gate] missing ${name}: ${p}" >&2
    exit 1
  fi
  local m
  m="$(file_mode "$p")"
  case "$m" in
    400|600)
      ;;
    *)
      echo "[gate] insecure mode for ${name}: ${p} mode=${m} (expected 400 or 600)" >&2
      exit 1
      ;;
  esac
}

assert_readable_json() {
  local p="$1"
  local name="$2"
  if [[ ! -f "$p" ]]; then
    echo "[gate] missing ${name}: ${p}" >&2
    exit 1
  fi
  if ! have_cmd python3; then
    echo "[gate] python3 is required to validate JSON files" >&2
    exit 1
  fi
  if ! python3 -m json.tool "$p" >/dev/null 2>&1; then
    echo "[gate] invalid JSON in ${name}: ${p}" >&2
    exit 1
  fi
}

assert_unit_state() {
  local unit="$1"
  systemctl is-enabled "$unit" >/dev/null
  systemctl is-active "$unit" >/dev/null
}

assert_helper_contract() {
  local token
  token="$(tr -d '\r\n' < "$helper_token_file")"
  if [[ -z "$token" ]]; then
    echo "[gate] helper token file is empty: ${helper_token_file}" >&2
    exit 1
  fi
  if [[ ! -S "$helper_socket" ]]; then
    echo "[gate] helper socket not found: ${helper_socket}" >&2
    exit 1
  fi
  if ! have_cmd curl; then
    echo "[gate] curl is required" >&2
    exit 1
  fi

  local schema
  schema="$(curl -sS --unix-socket "$helper_socket" http://localhost/v1/helper/schema)"
  if [[ "$schema" != *'"apiVersion":"v1"'* ]]; then
    echo "[gate] helper schema apiVersion mismatch" >&2
    exit 1
  fi
  if [[ "$schema" != *'"authRequired":true'* ]]; then
    echo "[gate] helper schema authRequired=false (expected true in prod baseline)" >&2
    exit 1
  fi

  local status
  status="$(curl -sS --unix-socket "$helper_socket" -H "Authorization: Bearer ${token}" http://localhost/v1/helper/status)"
  if [[ "$status" != *'"running":'* ]]; then
    echo "[gate] helper status response malformed" >&2
    exit 1
  fi
}

assert_bridge_status_stream() {
  local token
  token="$(tr -d '\r\n' < "$helper_token_file")"
  local out
  out="$(curl -sS --unix-socket "$helper_socket" -H "Authorization: Bearer ${token}" "http://localhost/v1/helper/bridge.status.stream?interval=200ms&duration=1s")"
  if [[ "$out" != *"event: status"* ]]; then
    echo "[gate] bridge.status.stream missing status events" >&2
    exit 1
  fi
  if [[ "$out" != *"event: done"* ]]; then
    echo "[gate] bridge.status.stream missing done event" >&2
    exit 1
  fi
}

echo "[gate] linux ops/security/SRE baseline started"

if [[ "${skip_os_check}" != "true" ]]; then
  assert_os_matrix
fi

if [[ "${skip_helper_check}" != "true" ]]; then
  assert_secure_file "$helper_token_file" "helper token"
  assert_readable_json "$bootstrap_file" "bootstrap payload"
fi
for k in "${support_key_files[@]}"; do
  [[ -n "$k" ]] || continue
  assert_secure_file "$k" "support signing key"
done

if [[ "$skip_unit_check" != "true" ]]; then
  if ! have_cmd systemctl; then
    echo "[gate] systemctl is required for unit checks" >&2
    exit 1
  fi
  declare -A seen=()
  for u in "${units[@]}"; do
    [[ -n "$u" ]] || continue
    if [[ -n "${seen[$u]:-}" ]]; then
      continue
    fi
    seen["$u"]=1
    echo "[gate] unit check: ${u}"
    assert_unit_state "$u"
  done
fi

if [[ "${skip_helper_check}" != "true" ]]; then
  assert_helper_contract
  assert_bridge_status_stream
fi

echo "[gate] linux ops/security/SRE baseline passed"
