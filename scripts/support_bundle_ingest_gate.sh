#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/support_bundle_ingest_gate.sh \
    --bundle <path> \
    [--require-signature true|false] \
    [--active-key key-id=/path/key ...] \
    [--previous-key key-id=/path/key ...] \
    [--retired-key-id key-id ...]

Environment fallbacks:
  SUPPORT_BUNDLE_PATH            Bundle envelope path (used when --bundle is omitted)
  SUPPORT_REQUIRE_SIGNATURE      true|false (default: true)
  SUPPORT_ACTIVE_KEYS            Comma-separated key specs: key-id=/path/key,key-id=/path/key
  SUPPORT_PREVIOUS_KEYS          Comma-separated key specs
  SUPPORT_RETIRED_KEY_IDS        Comma-separated key ids

Example:
  scripts/support_bundle_ingest_gate.sh \
    --bundle ./support-bundle.json \
    --require-signature true \
    --active-key k2=/etc/tun/support-signing-k2.key \
    --previous-key k1=/etc/tun/support-signing-k1.key \
    --retired-key-id k0
EOF
}

split_csv_to_array() {
  local csv="$1"
  local -n out_ref="$2"
  out_ref=()
  [[ -z "$csv" ]] && return 0
  local item
  IFS=',' read -r -a out_ref <<<"$csv"
  for item in "${!out_ref[@]}"; do
    out_ref[$item]="$(echo "${out_ref[$item]}" | xargs)"
  done
}

bundle="${SUPPORT_BUNDLE_PATH:-}"
require_signature="${SUPPORT_REQUIRE_SIGNATURE:-true}"
declare -a active_keys=()
declare -a previous_keys=()
declare -a retired_key_ids=()

if [[ -n "${SUPPORT_ACTIVE_KEYS:-}" ]]; then
  split_csv_to_array "${SUPPORT_ACTIVE_KEYS}" active_keys
fi
if [[ -n "${SUPPORT_PREVIOUS_KEYS:-}" ]]; then
  split_csv_to_array "${SUPPORT_PREVIOUS_KEYS}" previous_keys
fi
if [[ -n "${SUPPORT_RETIRED_KEY_IDS:-}" ]]; then
  split_csv_to_array "${SUPPORT_RETIRED_KEY_IDS}" retired_key_ids
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bundle)
      [[ $# -ge 2 ]] || { echo "missing value for --bundle" >&2; exit 2; }
      bundle="$2"
      shift 2
      ;;
    --require-signature)
      [[ $# -ge 2 ]] || { echo "missing value for --require-signature" >&2; exit 2; }
      require_signature="$2"
      shift 2
      ;;
    --active-key)
      [[ $# -ge 2 ]] || { echo "missing value for --active-key" >&2; exit 2; }
      active_keys+=("$2")
      shift 2
      ;;
    --previous-key)
      [[ $# -ge 2 ]] || { echo "missing value for --previous-key" >&2; exit 2; }
      previous_keys+=("$2")
      shift 2
      ;;
    --retired-key-id)
      [[ $# -ge 2 ]] || { echo "missing value for --retired-key-id" >&2; exit 2; }
      retired_key_ids+=("$2")
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

if [[ -z "$bundle" ]]; then
  echo "bundle path is required (--bundle or SUPPORT_BUNDLE_PATH)" >&2
  exit 2
fi
if [[ ! -f "$bundle" ]]; then
  echo "bundle file not found: $bundle" >&2
  exit 2
fi

declare -a cmd=(go run ./cmd/support-bundle-verify -in "$bundle" "-require-signature=${require_signature}")

for spec in "${active_keys[@]}"; do
  [[ -n "$spec" ]] || continue
  cmd+=(-active-key "$spec")
done
for spec in "${previous_keys[@]}"; do
  [[ -n "$spec" ]] || continue
  cmd+=(-previous-key "$spec")
done
for id in "${retired_key_ids[@]}"; do
  [[ -n "$id" ]] || continue
  cmd+=(-retired-key-id "$id")
done

"${cmd[@]}"
