#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"
VRN_HOST="${2:-vrn}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

EDG_PEERS_JSON="${TMP_DIR}/edg-peers.json"
EDG_UPLINKS_JSON="${TMP_DIR}/edg-uplinks.json"
VRN_PEERS_JSON="${TMP_DIR}/vrn-peers.json"
VRN_UPLINKS_JSON="${TMP_DIR}/vrn-uplinks.json"

echo "[1/4] fetch EDG legacy responses"
ssh "${EDG_HOST}" "TOKEN=\$(sudo awk -F= '/^WG_CONTROL_API_TOKEN=/{print \$2}' /etc/wireguard/wg-portal.env); curl -s -H \"X-API-Token: \${TOKEN}\" http://127.0.0.1:18110/v1/peers" > "${EDG_PEERS_JSON}"
ssh "${EDG_HOST}" "TOKEN=\$(sudo awk -F= '/^WG_CONTROL_API_TOKEN=/{print \$2}' /etc/wireguard/wg-portal.env); curl -s -H \"X-API-Token: \${TOKEN}\" http://127.0.0.1:18110/v1/uplinks" > "${EDG_UPLINKS_JSON}"

echo "[2/4] fetch VRN shadow DB-backed responses"
ssh "${VRN_HOST}" "TOKEN=\$(awk -F= '/^WG_CONTROL_API_TOKEN=/{print \$2}' /etc/jstun-shadow/jstun-shadow.env); curl -s -H \"X-API-Token: \${TOKEN}\" http://127.0.0.1:18190/v1/peers" > "${VRN_PEERS_JSON}"
ssh "${VRN_HOST}" "TOKEN=\$(awk -F= '/^WG_CONTROL_API_TOKEN=/{print \$2}' /etc/jstun-shadow/jstun-shadow.env); curl -s -H \"X-API-Token: \${TOKEN}\" http://127.0.0.1:18190/v1/uplinks" > "${VRN_UPLINKS_JSON}"

echo "[3/4] compare payloads"
python3 - <<'PY' "${EDG_PEERS_JSON}" "${EDG_UPLINKS_JSON}" "${VRN_PEERS_JSON}" "${VRN_UPLINKS_JSON}"
import json, pathlib, sys
from collections import Counter

edg_peers = json.loads(pathlib.Path(sys.argv[1]).read_text())
edg_uplinks = json.loads(pathlib.Path(sys.argv[2]).read_text())
vrn_peers = json.loads(pathlib.Path(sys.argv[3]).read_text())
vrn_uplinks = json.loads(pathlib.Path(sys.argv[4]).read_text())

def norm_ids(payload):
    return sorted(str(item.get("id") or item.get("peer_id") or "") for item in payload.get("items", []))

def norm_statuses(payload):
    return Counter(str(item.get("status") or "") for item in payload.get("items", []))

def norm_uplinks(payload):
    items = payload.get("items")
    result = {
        "nyc_ips": sorted(payload.get("nyc_ips", [])),
        "fra_ips": sorted(payload.get("fra_ips", [])),
    }
    if isinstance(items, list):
        result["items"] = sorted(str(item.get("uplink_id") or "") for item in items)
    return result

print("edg_peers_count", len(edg_peers.get("items", [])))
print("vrn_peers_count", len(vrn_peers.get("items", [])))
print("edg_peer_statuses", dict(norm_statuses(edg_peers)))
print("vrn_peer_statuses", dict(norm_statuses(vrn_peers)))
print("edg_peer_ids_sample", norm_ids(edg_peers)[:10])
print("vrn_peer_ids_sample", norm_ids(vrn_peers)[:10])
print("vrn_peers_source", vrn_peers.get("source"))
print("edg_uplinks_shape", norm_uplinks(edg_uplinks))
print("vrn_uplinks_shape", norm_uplinks(vrn_uplinks))
print("vrn_uplinks_source", vrn_uplinks.get("source"))
PY

echo "[4/4] compare one peer uplink response"
FIRST_VRN_PEER="$(python3 - <<'PY' "${VRN_PEERS_JSON}"
import json, pathlib, sys
obj = json.loads(pathlib.Path(sys.argv[1]).read_text())
items = obj.get("items", [])
print(items[0]["id"] if items else "")
PY
)"
if [[ -n "${FIRST_VRN_PEER}" ]]; then
  ssh "${VRN_HOST}" "TOKEN=\$(awk -F= '/^WG_CONTROL_API_TOKEN=/{print \$2}' /etc/jstun-shadow/jstun-shadow.env); curl -s -H \"X-API-Token: \${TOKEN}\" http://127.0.0.1:18190/v1/peers/${FIRST_VRN_PEER}/uplink"
  printf '\n'
else
  echo "no_vrn_peer_available"
fi
