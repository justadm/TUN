#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/provision_antifilter_bgp_frr.sh [options] --host <ssh-host> --local-as <asn> --router-id <ipv4>

Options:
  --host <ssh-host>             SSH host alias/name (required)
  --local-as <asn>              Local ASN for this gateway (required)
  --router-id <ipv4>            BGP router-id on gateway (required)
  --neighbor <ipv4>             Antifilter BGP service IP (default: 45.154.73.71)
  --neighbor-as <asn>           Antifilter ASN (default: 65432)
  --hold-time <sec>             BGP hold timer (default: 240)
  --keepalive <sec>             BGP keepalive timer (default: 80)
  --max-prefix <num>            Max accepted prefixes (default: 50000)
  --prefix-set <name>           Prefix-list name (default: ANTIFILTER-IN)
  --route-map <name>            Route-map name (default: ANTIFILTER-IN)
  --import-policy <mode>        prefix-only|deny-all (default: prefix-only)
  --ebgp-multihop <hops>        eBGP multihop hops (default: 32)
  --dry-run                     Print plan only (default)
  --apply                       Apply on remote host
  -h, --help

Notes:
  - Script manages a dedicated block in /etc/frr/frr.conf:
    ! BEGIN TUN-ANTIFILTER-BGP ... ! END TUN-ANTIFILTER-BGP
  - Existing unmanaged FRR config remains untouched.
EOF
}

HOST=""
LOCAL_AS=""
ROUTER_ID=""
NEIGHBOR="45.154.73.71"
NEIGHBOR_AS="65432"
HOLD_TIME="240"
KEEPALIVE="80"
MAX_PREFIX="50000"
PREFIX_SET="ANTIFILTER-IN"
ROUTE_MAP="ANTIFILTER-IN"
IMPORT_POLICY="prefix-only"
EBGP_MULTIHOP="32"
DRY_RUN="true"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) HOST="${2:-}"; shift 2 ;;
    --local-as) LOCAL_AS="${2:-}"; shift 2 ;;
    --router-id) ROUTER_ID="${2:-}"; shift 2 ;;
    --neighbor) NEIGHBOR="${2:-}"; shift 2 ;;
    --neighbor-as) NEIGHBOR_AS="${2:-}"; shift 2 ;;
    --hold-time) HOLD_TIME="${2:-}"; shift 2 ;;
    --keepalive) KEEPALIVE="${2:-}"; shift 2 ;;
    --max-prefix) MAX_PREFIX="${2:-}"; shift 2 ;;
    --prefix-set) PREFIX_SET="${2:-}"; shift 2 ;;
    --route-map) ROUTE_MAP="${2:-}"; shift 2 ;;
    --import-policy) IMPORT_POLICY="${2:-}"; shift 2 ;;
    --ebgp-multihop) EBGP_MULTIHOP="${2:-}"; shift 2 ;;
    --dry-run) DRY_RUN="true"; shift ;;
    --apply) DRY_RUN="false"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ -n "${HOST}" ]] || { echo "--host is required" >&2; exit 2; }
[[ -n "${LOCAL_AS}" ]] || { echo "--local-as is required" >&2; exit 2; }
[[ -n "${ROUTER_ID}" ]] || { echo "--router-id is required" >&2; exit 2; }
[[ "${HOLD_TIME}" =~ ^[0-9]+$ && "${HOLD_TIME}" -ge 3 ]] || { echo "--hold-time must be integer >= 3" >&2; exit 2; }
[[ "${KEEPALIVE}" =~ ^[0-9]+$ && "${KEEPALIVE}" -ge 1 ]] || { echo "--keepalive must be integer >= 1" >&2; exit 2; }
[[ "${MAX_PREFIX}" =~ ^[0-9]+$ && "${MAX_PREFIX}" -ge 1 ]] || { echo "--max-prefix must be integer >= 1" >&2; exit 2; }
[[ "${EBGP_MULTIHOP}" =~ ^[0-9]+$ && "${EBGP_MULTIHOP}" -ge 1 ]] || { echo "--ebgp-multihop must be integer >= 1" >&2; exit 2; }
[[ "${IMPORT_POLICY}" == "prefix-only" || "${IMPORT_POLICY}" == "deny-all" ]] || {
  echo "--import-policy must be prefix-only|deny-all" >&2
  exit 2
}

START_MARKER="! BEGIN TUN-ANTIFILTER-BGP"
END_MARKER="! END TUN-ANTIFILTER-BGP"

route_map_block() {
  local route_map_out="${ROUTE_MAP}-OUT"
  if [[ "${IMPORT_POLICY}" == "deny-all" ]]; then
    cat <<EOF
route-map ${ROUTE_MAP} deny 10
route-map ${route_map_out} deny 10
EOF
    return
  fi
  cat <<EOF
ip prefix-list ${PREFIX_SET} seq 5 permit 0.0.0.0/0 le 32
route-map ${ROUTE_MAP} permit 10
 match ip address prefix-list ${PREFIX_SET}
route-map ${route_map_out} deny 10
EOF
}

render_block() {
  local route_map_out="${ROUTE_MAP}-OUT"
  cat <<EOF
${START_MARKER}
ip nht resolve-via-default
!
$(route_map_block)
!
router bgp ${LOCAL_AS}
 bgp router-id ${ROUTER_ID}
 neighbor ${NEIGHBOR} remote-as ${NEIGHBOR_AS}
 neighbor ${NEIGHBOR} ebgp-multihop ${EBGP_MULTIHOP}
 neighbor ${NEIGHBOR} disable-connected-check
 neighbor ${NEIGHBOR} timers ${KEEPALIVE} ${HOLD_TIME}
 !
 address-family ipv4 unicast
  neighbor ${NEIGHBOR} activate
  neighbor ${NEIGHBOR} route-map ${ROUTE_MAP} in
  neighbor ${NEIGHBOR} route-map ${route_map_out} out
  neighbor ${NEIGHBOR} maximum-prefix ${MAX_PREFIX} restart 60
 exit-address-family
!
${END_MARKER}
EOF
}

echo "[plan] host=${HOST} local_as=${LOCAL_AS} router_id=${ROUTER_ID} neighbor=${NEIGHBOR} neighbor_as=${NEIGHBOR_AS} hold=${HOLD_TIME} keepalive=${KEEPALIVE} max_prefix=${MAX_PREFIX} ebgp_multihop=${EBGP_MULTIHOP} import_policy=${IMPORT_POLICY}"

if [[ "${DRY_RUN}" == "true" ]]; then
  echo "[dry-run] managed FRR block:"
  render_block
  exit 0
fi

tmp_dir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmp_dir}"
}
trap cleanup EXIT

current="${tmp_dir}/frr.conf.current"
managed="${tmp_dir}/frr.conf.managed"
merged="${tmp_dir}/frr.conf.merged"
block="${tmp_dir}/bgp.block"

ssh "${HOST}" "sudo test -f /etc/frr/frr.conf"
ssh "${HOST}" "sudo cat /etc/frr/frr.conf" > "${current}"
render_block > "${block}"

python3 - "${current}" "${block}" "${managed}" "${START_MARKER}" "${END_MARKER}" <<'PY'
import sys
from pathlib import Path

current = Path(sys.argv[1]).read_text(encoding="utf-8")
block = Path(sys.argv[2]).read_text(encoding="utf-8").rstrip() + "\n"
out = Path(sys.argv[3])
start = sys.argv[4]
end = sys.argv[5]

lines = current.splitlines()
i = 0
kept = []
while i < len(lines):
    line = lines[i]
    if line.strip() == start:
        i += 1
        while i < len(lines) and lines[i].strip() != end:
            i += 1
        if i < len(lines) and lines[i].strip() == end:
            i += 1
        continue
    kept.append(line)
    i += 1

text = "\n".join(kept).rstrip()
if text:
    text += "\n\n"
text += block
out.write_text(text, encoding="utf-8")
PY

cp "${managed}" "${merged}"
scp "${merged}" "${HOST}:/tmp/tun-antifilter-frr.conf"

ssh "${HOST}" "ts=\$(date +%Y%m%d%H%M%S); sudo cp /etc/frr/frr.conf /etc/frr/frr.conf.bak.\${ts}"
ssh "${HOST}" "sudo install -m 0640 -o frr -g frr /tmp/tun-antifilter-frr.conf /etc/frr/frr.conf"
ssh "${HOST}" "sudo systemctl reload frr || sudo systemctl restart frr"
ssh "${HOST}" "sudo vtysh -c 'clear bgp ipv4 unicast ${NEIGHBOR}' >/dev/null 2>&1 || true"

echo "[ok] applied. summary:"
ssh "${HOST}" "sudo vtysh -c 'show bgp ipv4 unicast summary'"
