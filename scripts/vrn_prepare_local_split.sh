#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/vrn_prepare_local_split.sh --peer-ip <10.18.0.X> [options]

Options:
  --host <ssh-host>         default: vrn
  --peer-ip <ipv4>          WG client IP in vrn wg0 subnet (required)
  --eth-if <ifname>         default: ens18
  --eth-gw <ipv4>           default: 91.221.109.1
  --tbl-ru <id>             default: 100
  --tbl-ams <id>            default: 101
  --tbl-nyc <id>            default: 102
  --tbl-fra <id>            default: 103
  --dry-run                 default mode
  --apply                   execute changes
  -h, --help

What it does:
  1) removes peer IP from forced sets: peer_ams/peer_nyc/peer_fra/peer_ru_direct
  2) ensures policy route tables and ip rules:
     mark 0x10 -> table RU (direct via eth gw)
     mark 0x11 -> table AMS (wg-ams)
     mark 0x12 -> table NYC (wg-nyc)
     mark 0x13 -> table FRA (wg-fra)
  3) prints verification snapshot
EOF
}

HOST="vrn"
PEER_IP=""
ETH_IF="ens18"
ETH_GW="91.221.109.1"
TBL_RU="100"
TBL_AMS="101"
TBL_NYC="102"
TBL_FRA="103"
DRY_RUN="true"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) HOST="${2:-}"; shift 2 ;;
    --peer-ip) PEER_IP="${2:-}"; shift 2 ;;
    --eth-if) ETH_IF="${2:-}"; shift 2 ;;
    --eth-gw) ETH_GW="${2:-}"; shift 2 ;;
    --tbl-ru) TBL_RU="${2:-}"; shift 2 ;;
    --tbl-ams) TBL_AMS="${2:-}"; shift 2 ;;
    --tbl-nyc) TBL_NYC="${2:-}"; shift 2 ;;
    --tbl-fra) TBL_FRA="${2:-}"; shift 2 ;;
    --dry-run) DRY_RUN="true"; shift ;;
    --apply) DRY_RUN="false"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ -n "${PEER_IP}" ]] || { echo "--peer-ip is required" >&2; exit 2; }
[[ "${PEER_IP}" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || { echo "invalid --peer-ip: ${PEER_IP}" >&2; exit 2; }

remote_cmd=$(
  cat <<EOF
set -euo pipefail
peer_ip="${PEER_IP}"
eth_if="${ETH_IF}"
eth_gw="${ETH_GW}"
tbl_ru="${TBL_RU}"
tbl_ams="${TBL_AMS}"
tbl_nyc="${TBL_NYC}"
tbl_fra="${TBL_FRA}"

for s in peer_ams peer_nyc peer_fra peer_ru_direct; do
  sudo nft delete element inet vrn "\$s" "{ \$peer_ip }" 2>/dev/null || true
done

sudo ip route replace table "\$tbl_ru" default via "\$eth_gw" dev "\$eth_if"
if ip link show wg-ams >/dev/null 2>&1; then
  sudo ip route replace table "\$tbl_ams" default dev wg-ams
fi
if ip link show wg-nyc >/dev/null 2>&1; then
  sudo ip route replace table "\$tbl_nyc" default dev wg-nyc
fi
if ip link show wg-fra >/dev/null 2>&1; then
  sudo ip route replace table "\$tbl_fra" default dev wg-fra
fi

sudo ip rule del fwmark 0x10/0xff table "\$tbl_ru" 2>/dev/null || true
sudo ip rule del fwmark 0x11/0xff table "\$tbl_ams" 2>/dev/null || true
sudo ip rule del fwmark 0x12/0xff table "\$tbl_nyc" 2>/dev/null || true
sudo ip rule del fwmark 0x13/0xff table "\$tbl_fra" 2>/dev/null || true
sudo ip rule add fwmark 0x10/0xff table "\$tbl_ru" priority 10010
sudo ip rule add fwmark 0x11/0xff table "\$tbl_ams" priority 10011
sudo ip rule add fwmark 0x12/0xff table "\$tbl_nyc" priority 10012
sudo ip rule add fwmark 0x13/0xff table "\$tbl_fra" priority 10013

echo "[verify] ip rule"
ip rule show | grep -E '1001[0-3]|fwmark' || true
echo "[verify] tables"
ip route show table "\$tbl_ru" || true
ip route show table "\$tbl_ams" || true
ip route show table "\$tbl_nyc" || true
ip route show table "\$tbl_fra" || true
echo "[verify] peer set membership for \$peer_ip"
for s in peer_ams peer_nyc peer_fra peer_ru_direct; do
  sudo nft list set inet vrn "\$s" | grep -w "\$peer_ip" || true
done
EOF
)

if [[ "${DRY_RUN}" == "true" ]]; then
  echo "[dry-run] host=${HOST} peer_ip=${PEER_IP}"
  echo "${remote_cmd}"
  exit 0
fi

ssh "${HOST}" "${remote_cmd}"
