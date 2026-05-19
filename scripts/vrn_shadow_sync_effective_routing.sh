#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"
VRN_HOST="${2:-vrn}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

EDG_PEERS_JSON="${TMP_DIR}/edg-peers.json"
EDG_UPLINKS_JSON="${TMP_DIR}/edg-uplinks.json"
EDG_REAL_IPS_JSON="${TMP_DIR}/edg-real-ips.json"
SYNC_SQL="${TMP_DIR}/sync-effective-routing.sql"

echo "[1/5] fetch EDG legacy peers and uplinks"
ssh "${EDG_HOST}" "TOKEN=\$(sudo awk -F= '/^WG_CONTROL_API_TOKEN=/{print \$2}' /etc/wireguard/wg-portal.env); curl -s -H \"X-API-Token: \${TOKEN}\" http://127.0.0.1:18110/v1/peers" > "${EDG_PEERS_JSON}"
ssh "${EDG_HOST}" "TOKEN=\$(sudo awk -F= '/^WG_CONTROL_API_TOKEN=/{print \$2}' /etc/wireguard/wg-portal.env); curl -s -H \"X-API-Token: \${TOKEN}\" http://127.0.0.1:18110/v1/uplinks" > "${EDG_UPLINKS_JSON}"
ssh "${EDG_HOST}" "sudo python3 - <<'PY'
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
PY" > "${EDG_REAL_IPS_JSON}"

echo "[2/5] build shadow sync SQL"
python3 - <<'PY' "${EDG_PEERS_JSON}" "${EDG_UPLINKS_JSON}" "${EDG_REAL_IPS_JSON}" "${SYNC_SQL}"
import json
import pathlib
import sys
import datetime as dt

peers = json.loads(pathlib.Path(sys.argv[1]).read_text())
uplinks = json.loads(pathlib.Path(sys.argv[2]).read_text())
real_ips = json.loads(pathlib.Path(sys.argv[3]).read_text())
out = pathlib.Path(sys.argv[4])
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
    last_handshake_unix = int_or_zero(item.get("last_handshake_unix"))
    rx_bytes = int_or_zero(item.get("rx_bytes"))
    tx_bytes = int_or_zero(item.get("tx_bytes"))
    connected_at = item.get("connected_at") or None
    last_handshake_at = item.get("last_handshake_at") or None
    active.append(
        {
            "peer_id": peer_id,
            "uplink_id": effective_uplink_id,
            "last_handshake_unix": last_handshake_unix,
            "last_handshake_at": last_handshake_at,
            "connected_at": connected_at,
            "rx_bytes": rx_bytes,
            "tx_bytes": tx_bytes,
            "real_ip": normalize_ip(real_ips.get(peer_id)),
        }
    )

uplink_counts = {
    "edg/ams": 0,
    "edg/fra": 0,
    "edg/nyc": 0,
    "vrn/ams": 0,
    "vrn/fra": 0,
    "vrn/nyc": 0,
}
uplink_stats = {
    "edg/ams": {"rx": 0, "tx": 0, "min_handshake_age_sec": None},
    "edg/fra": {"rx": 0, "tx": 0, "min_handshake_age_sec": None},
    "edg/nyc": {"rx": 0, "tx": 0, "min_handshake_age_sec": None},
    "vrn/ams": {"rx": 0, "tx": 0, "min_handshake_age_sec": None},
    "vrn/fra": {"rx": 0, "tx": 0, "min_handshake_age_sec": None},
    "vrn/nyc": {"rx": 0, "tx": 0, "min_handshake_age_sec": None},
}
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
    uplink_id = item["uplink_id"]
    lines.append(
        "insert into peer_effective_routing "
        "(peer_id, effective_edge_id, effective_uplink_id, effective_ru_path, effective_notru_path, effective_fallback_path, observed_at) values "
        f"({sql_str(peer_id)}, 'edg', {sql_str(uplink_id)}, 'direct', {sql_str(uplink_id)}, 'edg/ams', now()) "
        "on conflict (peer_id) do update set "
        "effective_edge_id = excluded.effective_edge_id, "
        "effective_uplink_id = excluded.effective_uplink_id, "
        "effective_ru_path = excluded.effective_ru_path, "
        "effective_notru_path = excluded.effective_notru_path, "
        "effective_fallback_path = excluded.effective_fallback_path, "
        "observed_at = excluded.observed_at;"
    )
    lines.append(
        f"update peers set connected_at = {sql_str(item['connected_at'])} where peer_id = {sql_str(peer_id)};"
    )
    lines.append(
        "insert into peer_runtime_state "
        "(peer_id, last_handshake_at, last_handshake_unix, rx_bytes, tx_bytes, real_ip, active_uplink_id, health_status, health_note, observed_at) values "
        f"({sql_str(peer_id)}, {sql_str(item['last_handshake_at'])}, {sql_int(item['last_handshake_unix'])}, {sql_int(item['rx_bytes'])}, {sql_int(item['tx_bytes'])}, {sql_str(item['real_ip'])}, {sql_str(uplink_id)}, 'healthy', 'synced from edg legacy peer runtime', now()) "
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
    health = "healthy" if uplink_id.startswith("edg/") else "unknown"
    note = f"active peers={count}"
    stats = uplink_stats.get(uplink_id, {})
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
out.write_text("\n".join(lines) + "\n")
print(f"active_peers={len(active)}")
print(f"fra_peers={sum(1 for x in active if x['uplink_id'] == 'edg/fra')}")
print(f"nyc_peers={sum(1 for x in active if x['uplink_id'] == 'edg/nyc')}")
print(f"ams_peers={sum(1 for x in active if x['uplink_id'] == 'edg/ams')}")
print(f"runtime_with_handshake={sum(1 for x in active if x['last_handshake_unix'] > 0)}")
PY

echo "[3/5] upload sync SQL to VRN"
scp "${SYNC_SQL}" "${VRN_HOST}:/tmp/sync-effective-routing.sql"
ssh "${VRN_HOST}" "sudo mv /tmp/sync-effective-routing.sql /opt/jstun-shadow/sql/sync-effective-routing.sql"

echo "[4/5] apply sync SQL on VRN shadow DB"
ssh "${VRN_HOST}" "PGPASSWORD=change-me psql -h 127.0.0.1 -p 15432 -U jstun_shadow -d jstun_shadow -f /opt/jstun-shadow/sql/sync-effective-routing.sql >/tmp/jstun-shadow-sync-effective-routing.log"

echo "[5/5] verify synced effective state"
ssh "${VRN_HOST}" "PGPASSWORD=change-me psql -h 127.0.0.1 -p 15432 -U jstun_shadow -d jstun_shadow -At -F '|' -c \"select split_part(effective_uplink_id, '/', 2) as uplink, count(*) from peer_effective_routing group by 1 order by 1;\""
