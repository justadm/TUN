#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/provision_support_signing_k2.sh [options]

Provision shared support-bundle signing key (k2) to server hosts.
The key is installed as /etc/tun/support-signing-k2.key with root:root 0600.

Options:
  --key-file <path>      local key material file to distribute (default: generate ephemeral key)
  --out-key <path>       save generated key locally (only when --key-file is omitted)
  --host <ssh-host>      target host (repeatable); default: bx_msk_d,vrn,edg
  --remote-path <path>   default: /etc/tun/support-signing-k2.key
  --help                 show this help
EOF
}

declare -a hosts=("bx_msk_d" "vrn" "edg")
key_file=""
out_key=""
remote_path="/etc/tun/support-signing-k2.key"
tmp_key=""

cleanup() {
  if [[ -n "${tmp_key}" && -f "${tmp_key}" ]]; then
    rm -f "${tmp_key}"
  fi
}
trap cleanup EXIT

while [[ $# -gt 0 ]]; do
  case "$1" in
    --key-file) key_file="${2:-}"; shift 2 ;;
    --out-key) out_key="${2:-}"; shift 2 ;;
    --host) hosts+=("${2:-}"); shift 2 ;;
    --remote-path) remote_path="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -n "${key_file}" && -n "${out_key}" ]]; then
  echo "--out-key is only valid when key is generated internally" >&2
  exit 2
fi

if [[ -n "${key_file}" ]]; then
  if [[ ! -f "${key_file}" ]]; then
    echo "missing key file: ${key_file}" >&2
    exit 1
  fi
else
  tmp_key="$(mktemp)"
  umask 077
  openssl rand -hex 32 > "${tmp_key}"
  key_file="${tmp_key}"
  if [[ -n "${out_key}" ]]; then
    install -m 600 "${tmp_key}" "${out_key}"
  fi
fi

for h in "${hosts[@]}"; do
  [[ -n "${h}" ]] || continue
  echo "[k2] install on ${h}"
  scp "${key_file}" "${h}:/tmp/support-signing-k2.key"
  ssh "${h}" "sudo -n install -d -m 700 /etc/tun && sudo -n install -m 600 /tmp/support-signing-k2.key ${remote_path} && sudo -n chown root:root ${remote_path}"
  ssh "${h}" "sudo -n test -f ${remote_path} && sudo -n stat -c '%U:%G %a %n' ${remote_path}"
done

echo "[k2] done"
