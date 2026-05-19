#!/usr/bin/env bash
set -euo pipefail

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

API_TOKEN="$(awk -F= '/^WG_CONTROL_API_TOKEN=/{print $2}' /etc/wireguard/wg-portal.env)"
VRN_SSH_TARGET="${JSTUN_SHADOW_SYNC_SSH_TARGET:-user@91.221.109.60}"
VRN_SSH_PORT="${JSTUN_SHADOW_SYNC_SSH_PORT:-65022}"
VRN_SSH_KEY="${JSTUN_SHADOW_SYNC_SSH_KEY:-/home/opsadmin/.ssh/jstun_shadow_read_ed25519}"
VRN_SSH_KNOWN_HOSTS="${JSTUN_SHADOW_SYNC_SSH_KNOWN_HOSTS:-/var/lib/wg-portal/shadow_known_hosts}"
VRN_DB_HOST="${JSTUN_SHADOW_SYNC_DB_HOST:-127.0.0.1}"
VRN_DB_PORT="${JSTUN_SHADOW_SYNC_DB_PORT:-15432}"
VRN_DB_NAME="${JSTUN_SHADOW_SYNC_DB_NAME:-jstun_shadow}"
VRN_DB_USER="${JSTUN_SHADOW_SYNC_DB_USER:-jstun_shadow}"
VRN_DB_PASSWORD="${JSTUN_SHADOW_SYNC_DB_PASSWORD:-change-me}"

EDG_PEERS_JSON="${TMP_DIR}/edg-peers.json"
EDG_UPLINKS_JSON="${TMP_DIR}/edg-uplinks.json"
EDG_REAL_IPS_JSON="${TMP_DIR}/edg-real-ips.json"
VRN_PEERS_JSON="${TMP_DIR}/vrn-peers.json"
VRN_RUNTIME_JSON="${TMP_DIR}/vrn-runtime.json"
SYNC_SQL="${TMP_DIR}/sync-effective-routing.sql"

mkdir -p "$(dirname "${VRN_SSH_KNOWN_HOSTS}")"
touch "${VRN_SSH_KNOWN_HOSTS}"

curl -s -H "X-API-Token: ${API_TOKEN}" http://127.0.0.1:18110/v1/peers > "${EDG_PEERS_JSON}"
curl -s -H "X-API-Token: ${API_TOKEN}" http://127.0.0.1:18110/v1/uplinks > "${EDG_UPLINKS_JSON}"
ssh -p "${VRN_SSH_PORT}" -i "${VRN_SSH_KEY}" -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile="${VRN_SSH_KNOWN_HOSTS}" "${VRN_SSH_TARGET}" "TOKEN=\$(sudo awk -F= '/^WG_CONTROL_API_TOKEN=/{print \$2}' /etc/jstun-shadow/jstun-shadow.env); curl -s -H \"X-API-Token: \${TOKEN}\" http://127.0.0.1:18190/v1/peers" > "${VRN_PEERS_JSON}"
ssh -p "${VRN_SSH_PORT}" -i "${VRN_SSH_KEY}" -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile="${VRN_SSH_KNOWN_HOSTS}" "${VRN_SSH_TARGET}" "sudo python3 - <<'PY'
import json
import subprocess
import re

def endpoint_host(raw):
    s = str(raw or '').strip()
    if not s or s == '(none)':
        return ''
    if s.startswith('['):
        right = s.find(']')
        return s[1:right] if right > 1 else ''
    if ':' in s:
        return s.rsplit(':', 1)[0]
    return s

def read_set(name):
    try:
        p = subprocess.run(['nft', 'list', 'set', 'inet', 'vrn', name], capture_output=True, text=True, timeout=5)
        text = str(p.stdout or '')
    except Exception:
        return set()
    return set(re.findall(r'\\b(?:\\d{1,3}\\.){3}\\d{1,3}\\b', text))

set_to_uplink = {
    'peer_ru_direct': 'vrn/ams',
    'peer_ams': 'vrn/ams',
    'peer_fra': 'vrn/fra',
    'peer_nyc': 'vrn/nyc',
}
ip_to_uplink = {}
for set_name, uplink_id in set_to_uplink.items():
    for ip in read_set(set_name):
        ip_to_uplink[ip] = uplink_id

runtime = {}
try:
    p = subprocess.run(['wg', 'show', 'wg0', 'dump'], capture_output=True, text=True, timeout=5)
    dump_out = str(p.stdout or '')
except Exception:
    dump_out = ''
for line in dump_out.splitlines():
    cols = line.strip().split('\\t')
    if len(cols) < 8:
        continue
    allowed = str(cols[3] or '').strip()
    if not allowed or '/' not in allowed:
        continue
    ip_short = allowed.split('/', 1)[0].strip()
    if not ip_short:
        continue
    hs_unix = 0
    rx = 0
    tx = 0
    try:
        hs_unix = int(cols[4] or 0)
    except Exception:
        pass
    try:
        rx = int(cols[5] or 0)
    except Exception:
        pass
    try:
        tx = int(cols[6] or 0)
    except Exception:
        pass
    runtime[ip_short] = {
        'allowed_ip': ip_short,
        'real_ip': endpoint_host(cols[2]),
        'last_handshake_unix': hs_unix,
        'rx_bytes': rx,
        'tx_bytes': tx,
        'uplink_id': ip_to_uplink.get(ip_short, 'vrn/nyc'),
    }
print(json.dumps(runtime, ensure_ascii=False, sort_keys=True))
PY" > "${VRN_RUNTIME_JSON}"

sudo python3 - <<'PY' > "${EDG_REAL_IPS_JSON}"
import json
from pathlib import Path

audit = Path('/var/lib/wg-portal/audit.jsonl')
latest = {}
if audit.exists():
    for line in reversed(audit.read_text(encoding='utf-8', errors='replace').splitlines()):
        line = (line or '').strip()
        if not line:
            continue
        try:
            row = json.loads(line)
        except Exception:
            continue
        peer_id = str(row.get('peer_id') or '').strip()
        ip = str(row.get('ip') or '').strip()
        if not peer_id or not ip or peer_id in latest:
            continue
        if ip in ('0.0.0.0', '127.0.0.1', '::1'):
            continue
        latest[peer_id] = ip
print(json.dumps(latest, ensure_ascii=False, sort_keys=True))
PY

python3 - <<'PY' "${EDG_PEERS_JSON}" "${EDG_UPLINKS_JSON}" "${EDG_REAL_IPS_JSON}" "${VRN_PEERS_JSON}" "${VRN_RUNTIME_JSON}" "${SYNC_SQL}"
import json
import pathlib
import sys
import datetime as dt

peers = json.loads(pathlib.Path(sys.argv[1]).read_text())
uplinks = json.loads(pathlib.Path(sys.argv[2]).read_text())
real_ips = json.loads(pathlib.Path(sys.argv[3]).read_text())
vrn_peers = json.loads(pathlib.Path(sys.argv[4]).read_text())
vrn_runtime = json.loads(pathlib.Path(sys.argv[5]).read_text())
out = pathlib.Path(sys.argv[6])
now_unix = int(dt.datetime.now(dt.timezone.utc).timestamp())

fra_ips = set(uplinks.get("fra_ips", []))
nyc_ips = set(uplinks.get("nyc_ips", []))

def sql_str(value):
    if value is None:
        return "NULL"
    return "'" + str(value).replace("'", "''") + "'"

def int_or_zero(value):
    try:
        return int(value or 0)
    except Exception:
        return 0

def sql_int(value):
    try:
        return str(int(value))
    except Exception:
        return "0"

def normalize_ip(value):
    raw = str(value or "").strip()
    if not raw:
        return None
    return raw.split("/", 1)[0].strip() or None

active = []
for item in peers.get("items", []):
    if str(item.get("status") or "") != "active":
        continue
    peer_id = str(item.get("id") or "").strip()
    allowed_ip = str(item.get("allowed_ip") or "").strip()
    ip_short = allowed_ip.split("/", 1)[0].strip() if allowed_ip else ""
    if not peer_id or not ip_short:
        continue
    if ip_short in fra_ips:
        effective_uplink_id = "edg/fra"
    elif ip_short in nyc_ips:
        effective_uplink_id = "edg/nyc"
    else:
        effective_uplink_id = "edg/ams"
    active.append(
        {
            "peer_id": peer_id,
            "edge_id": "edg",
            "uplink_id": effective_uplink_id,
            "last_handshake_unix": int_or_zero(item.get("last_handshake_unix")),
            "last_handshake_at": item.get("last_handshake_at") or None,
            "connected_at": item.get("connected_at") or None,
            "rx_bytes": int_or_zero(item.get("rx_bytes")),
            "tx_bytes": int_or_zero(item.get("tx_bytes")),
            "real_ip": normalize_ip(real_ips.get(peer_id)),
        }
    )

vrn_peers_by_ip = {}
for item in vrn_peers.get("items", []):
    allowed_ip = str(item.get("allowed_ip") or "").strip()
    ip_short = allowed_ip.split("/", 1)[0].strip() if allowed_ip else ""
    if ip_short:
        vrn_peers_by_ip[ip_short] = item

for ip_short, runtime in sorted(vrn_runtime.items()):
    item = vrn_peers_by_ip.get(ip_short)
    if not item:
        continue
    peer_id = str(item.get("id") or "").strip()
    if not peer_id:
        continue
    hs_unix = int_or_zero(runtime.get("last_handshake_unix"))
    last_handshake_at = None
    if hs_unix > 0:
        last_handshake_at = dt.datetime.fromtimestamp(hs_unix, dt.timezone.utc).isoformat()
    active.append(
        {
            "peer_id": peer_id,
            "edge_id": "vrn",
            "uplink_id": str(runtime.get("uplink_id") or "vrn/nyc"),
            "last_handshake_unix": hs_unix,
            "last_handshake_at": last_handshake_at,
            "connected_at": item.get("connected_at") or item.get("created_at") or None,
            "rx_bytes": int_or_zero(runtime.get("rx_bytes")),
            "tx_bytes": int_or_zero(runtime.get("tx_bytes")),
            "real_ip": normalize_ip(runtime.get("real_ip")),
        }
    )

uplink_counts = {k: 0 for k in ("edg/ams", "edg/fra", "edg/nyc", "vrn/ams", "vrn/fra", "vrn/nyc")}
uplink_stats = {k: {"rx": 0, "tx": 0, "min_handshake_age_sec": None} for k in uplink_counts}
for item in active:
    uplink_id = item["uplink_id"]
    uplink_counts[uplink_id] = uplink_counts.get(uplink_id, 0) + 1
    stats = uplink_stats.setdefault(uplink_id, {"rx": 0, "tx": 0, "min_handshake_age_sec": None})
    stats["rx"] += item["rx_bytes"]
    stats["tx"] += item["tx_bytes"]
    hs_unix = item["last_handshake_unix"]
    if hs_unix > 0:
        age = max(0, now_unix - hs_unix)
        prev = stats.get("min_handshake_age_sec")
        stats["min_handshake_age_sec"] = age if prev is None else min(prev, age)

lines = []
lines.append("begin;")
lines.append("delete from peer_effective_routing;")
for item in active:
    peer_id = item["peer_id"]
    edge_id = item["edge_id"]
    uplink_id = item["uplink_id"]
    fallback_uplink_id = "vrn/ams" if edge_id == "vrn" else "edg/ams"
    lines.append(
        "insert into peer_effective_routing "
        "(peer_id, effective_edge_id, effective_uplink_id, effective_ru_path, effective_notru_path, effective_fallback_path, observed_at) values "
        f"({sql_str(peer_id)}, {sql_str(edge_id)}, {sql_str(uplink_id)}, 'direct', {sql_str(uplink_id)}, {sql_str(fallback_uplink_id)}, now()) "
        "on conflict (peer_id) do update set "
        "effective_edge_id = excluded.effective_edge_id, "
        "effective_uplink_id = excluded.effective_uplink_id, "
        "effective_ru_path = excluded.effective_ru_path, "
        "effective_notru_path = excluded.effective_notru_path, "
        "effective_fallback_path = excluded.effective_fallback_path, "
        "observed_at = excluded.observed_at;"
    )
    status_sql = "'active'" if int_or_zero(item["last_handshake_unix"]) > 0 else "status"
    lines.append(f"update peers set connected_at = {sql_str(item['connected_at'])}, status = {status_sql} where peer_id = {sql_str(peer_id)};")
    lines.append(
        "insert into peer_runtime_state "
        "(peer_id, last_handshake_at, last_handshake_unix, rx_bytes, tx_bytes, real_ip, active_uplink_id, health_status, health_note, observed_at) values "
        f"({sql_str(peer_id)}, {sql_str(item['last_handshake_at'])}, {sql_int(item['last_handshake_unix'])}, {sql_int(item['rx_bytes'])}, {sql_int(item['tx_bytes'])}, {sql_str(item['real_ip'])}, {sql_str(uplink_id)}, 'healthy', {sql_str('synced from ' + edge_id + ' runtime push')}, now()) "
        "on conflict (peer_id) do update set "
        "last_handshake_at = excluded.last_handshake_at, "
        "last_handshake_unix = excluded.last_handshake_unix, "
        "rx_bytes = excluded.rx_bytes, "
        "tx_bytes = excluded.tx_bytes, "
        "real_ip = excluded.real_ip, "
        "active_uplink_id = excluded.active_uplink_id, "
        "health_status = excluded.health_status, "
        "health_note = excluded.health_note, "
        "observed_at = excluded.observed_at;"
    )

for uplink_id, count in sorted(uplink_counts.items()):
    stats = uplink_stats.get(uplink_id, {})
    health = "healthy" if (uplink_id.startswith("edg/") or count > 0 or stats.get("min_handshake_age_sec") is not None) else "unknown"
    note = f"active peers={count}"
    lines.append(
        "insert into uplink_runtime_state "
        "(uplink_id, health_status, health_note, handshake_age_sec, traffic_rx_bytes, traffic_tx_bytes, observed_at) values "
        f"({sql_str(uplink_id)}, {sql_str(health)}, {sql_str(note)}, {sql_str(stats.get('min_handshake_age_sec')) if stats.get('min_handshake_age_sec') is not None else 'NULL'}, {sql_int(stats.get('rx', 0))}, {sql_int(stats.get('tx', 0))}, now()) "
        "on conflict (uplink_id) do update set "
        "health_status = excluded.health_status, "
        "health_note = excluded.health_note, "
        "handshake_age_sec = excluded.handshake_age_sec, "
        "traffic_rx_bytes = excluded.traffic_rx_bytes, "
        "traffic_tx_bytes = excluded.traffic_tx_bytes, "
        "observed_at = excluded.observed_at;"
    )

lines.append("commit;")
out.write_text("\n".join(lines) + "\n", encoding="utf-8")
PY

scp -P "${VRN_SSH_PORT}" -i "${VRN_SSH_KEY}" -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile="${VRN_SSH_KNOWN_HOSTS}" "${SYNC_SQL}" "${VRN_SSH_TARGET}:/tmp/sync-effective-routing.sql"
ssh -p "${VRN_SSH_PORT}" -i "${VRN_SSH_KEY}" -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile="${VRN_SSH_KNOWN_HOSTS}" "${VRN_SSH_TARGET}" "sudo mkdir -p /opt/jstun-shadow/sql && sudo mv /tmp/sync-effective-routing.sql /opt/jstun-shadow/sql/sync-effective-routing.sql && PGPASSWORD='${VRN_DB_PASSWORD}' psql -h '${VRN_DB_HOST}' -p '${VRN_DB_PORT}' -U '${VRN_DB_USER}' -d '${VRN_DB_NAME}' -f /opt/jstun-shadow/sql/sync-effective-routing.sql >/tmp/jstun-shadow-sync-effective-routing.log"
