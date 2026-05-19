#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/gate_tunrnd_prod_mesh.sh [options]

Runs unified prod mesh gate for both contours:
  vrn <-> ams/fra/nyc
  edg <-> ams/fra/nyc

Options:
  --out-dir <path>      default: ./artifacts/tunrnd-prod-mesh-gate
  --strict-ssh          propagate strict ssh behavior to child gates
  --edg-host <host>     edg host/alias for edg contour checks (default: edg)
  --fra-via <host>      SSH jump-host for fra checks (applies to both contours)
  --nyc-via <host>      SSH jump-host for nyc checks (applies to both contours)
  --edg-via <host>      SSH jump-host for edg checks (edg contour only)
  --ssh-retries <n>     retries per SSH check (applies to both contours)
  --ssh-delay <sec>     delay between retries (applies to both contours)
  --ssh-timeout <sec>   connect timeout seconds (applies to both contours)
  --help                show this help
EOF
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out_dir="./artifacts/tunrnd-prod-mesh-gate"
strict_ssh=false
edg_host="edg"
fra_via=""
nyc_via=""
edg_via=""
ssh_retries=""
ssh_delay=""
ssh_timeout=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir) out_dir="${2:-}"; shift 2 ;;
    --strict-ssh) strict_ssh=true; shift ;;
    --edg-host) edg_host="${2:-}"; shift 2 ;;
    --fra-via) fra_via="${2:-}"; shift 2 ;;
    --nyc-via) nyc_via="${2:-}"; shift 2 ;;
    --edg-via) edg_via="${2:-}"; shift 2 ;;
    --ssh-retries) ssh_retries="${2:-}"; shift 2 ;;
    --ssh-delay) ssh_delay="${2:-}"; shift 2 ;;
    --ssh-timeout) ssh_timeout="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

mkdir -p "${out_dir}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
report="${out_dir}/prod-mesh-gate-${stamp}.txt"
vrn_dir="${out_dir}/vrn"
edg_dir="${out_dir}/edg"
mkdir -p "${vrn_dir}" "${edg_dir}"
vrn_status="not-run"
edg_status="not-run"

vrn_cmd=("${script_dir}/gate_tunrnd_vrn_mesh.sh" --out-dir "${vrn_dir}")
edg_cmd=("${script_dir}/gate_tunrnd_edg_mesh.sh" --out-dir "${edg_dir}" --edg-host "${edg_host}")
if [[ "${strict_ssh}" == "true" ]]; then
  vrn_cmd+=(--strict-ssh)
  edg_cmd+=(--strict-ssh)
fi
if [[ -n "${fra_via}" ]]; then
  vrn_cmd+=(--fra-via "${fra_via}")
  edg_cmd+=(--fra-via "${fra_via}")
fi
if [[ -n "${nyc_via}" ]]; then
  vrn_cmd+=(--nyc-via "${nyc_via}")
  edg_cmd+=(--nyc-via "${nyc_via}")
fi
if [[ -n "${edg_via}" ]]; then
  edg_cmd+=(--edg-via "${edg_via}")
fi
if [[ -n "${ssh_retries}" ]]; then
  vrn_cmd+=(--ssh-retries "${ssh_retries}")
  edg_cmd+=(--ssh-retries "${ssh_retries}")
fi
if [[ -n "${ssh_delay}" ]]; then
  vrn_cmd+=(--ssh-delay "${ssh_delay}")
  edg_cmd+=(--ssh-delay "${ssh_delay}")
fi
if [[ -n "${ssh_timeout}" ]]; then
  vrn_cmd+=(--ssh-timeout "${ssh_timeout}")
  edg_cmd+=(--ssh-timeout "${ssh_timeout}")
fi

{
  echo "[prod-mesh-gate] started at ${stamp}"
  echo "[prod-mesh-gate] strict_ssh=${strict_ssh}"
  echo "[prod-mesh-gate] edg_host=${edg_host} fra_via=${fra_via:-none} nyc_via=${nyc_via:-none} edg_via=${edg_via:-none} ssh_retries=${ssh_retries:-default} ssh_timeout=${ssh_timeout:-default}"
  echo
  echo "== vrn contour =="
  if "${vrn_cmd[@]}"; then
    vrn_status="passed"
  else
    vrn_status="failed"
  fi
  echo
  echo "== edg contour =="
  if "${edg_cmd[@]}"; then
    edg_status="passed"
  else
    edg_status="failed"
  fi
  echo
  echo "[prod-mesh-gate] summary: vrn=${vrn_status} edg=${edg_status}"
  if [[ "${vrn_status}" == "passed" && "${edg_status}" == "passed" ]]; then
    echo "[prod-mesh-gate] completed"
  else
    echo "[prod-mesh-gate] FAILED"
  fi
} > >(tee "${report}") 2>&1

echo "${report}"
if [[ "${vrn_status}" != "passed" || "${edg_status}" != "passed" ]]; then
  exit 1
fi
