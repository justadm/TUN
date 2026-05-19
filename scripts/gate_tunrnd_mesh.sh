#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/gate_tunrnd_mesh.sh [options]

Runs final SRE gate checks for persistent msk mesh deployment:
  msk_d <-> ams
  msk_d <-> fra
  msk_d <-> nyc

Writes gate evidence into artifacts directory.

Options:
  --out-dir <path>      default: ./artifacts/tunrnd-mesh-gate
  --msk-host <host>     default: bx_msk_d
  --ams-host <host>     default: ams
  --fra-host <host>     default: fra
  --nyc-host <host>     default: nyc
  --help                show this help
EOF
}

out_dir="./artifacts/tunrnd-mesh-gate"
msk_host="bx_msk_d"
ams_host="ams"
fra_host="fra"
nyc_host="nyc"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir) out_dir="${2:-}"; shift 2 ;;
    --msk-host) msk_host="${2:-}"; shift 2 ;;
    --ams-host) ams_host="${2:-}"; shift 2 ;;
    --fra-host) fra_host="${2:-}"; shift 2 ;;
    --nyc-host) nyc_host="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

mkdir -p "${out_dir}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
report="${out_dir}/mesh-gate-${stamp}.txt"

{
  echo "[mesh-gate] started at ${stamp}"
  echo
  echo "== msk services =="
  ssh "${msk_host}" "sudo -n systemctl --no-pager --full status tun-runtime-server@ams.service tun-runtime-server@fra.service tun-runtime-server@nyc.service"
  echo
  echo "== client services =="
  ssh "${ams_host}" "sudo -n systemctl --no-pager --full status tun-runtime-client@msk.service"
  ssh -J "${ams_host}" "${fra_host}" "sudo -n systemctl --no-pager --full status tun-runtime-client@msk.service"
  ssh -J "${ams_host}" "${nyc_host}" "sudo -n systemctl --no-pager --full status tun-runtime-client@msk.service"
  echo
  echo "== interface addresses =="
  ssh "${msk_host}" "sudo -n ip -brief addr show | grep -E 'trsrv-ams|trsrv-fra|trsrv-nyc'"
  ssh "${ams_host}" "sudo -n ip -brief addr show trcli-msk"
  ssh -J "${ams_host}" "${fra_host}" "sudo -n ip -brief addr show trcli-msk"
  ssh -J "${ams_host}" "${nyc_host}" "sudo -n ip -brief addr show trcli-msk"
  echo
  echo "== end-to-end pings =="
  ssh "${ams_host}" "sudo -n ping -I trcli-msk -c 3 -W 2 10.251.1.1"
  ssh -J "${ams_host}" "${fra_host}" "sudo -n ping -I trcli-msk -c 3 -W 2 10.251.2.1"
  ssh -J "${ams_host}" "${nyc_host}" "sudo -n ping -I trcli-msk -c 3 -W 2 10.251.3.1"
  echo
  echo "== msk ufw mesh rules =="
  ssh "${msk_host}" "sudo -n ufw status numbered | grep -E '18443|18444|18445|tun-rnd-mesh' || true"
  echo
  echo "[mesh-gate] passed"
} | tee "${report}"

echo "${report}"
