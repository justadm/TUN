#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"
VRN_HOST="${2:-vrn}"
DB_HOST="${JSTUN_SHADOW_PGHOST:-127.0.0.1}"
DB_PORT="${JSTUN_SHADOW_PGPORT:-15432}"
DB_NAME="${JSTUN_SHADOW_PGDATABASE:-jstun_shadow}"
DB_USER="${JSTUN_SHADOW_PGUSER:-jstun_shadow}"
DB_PASS="${JSTUN_SHADOW_PGPASSWORD:-change-me}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

EDG_JSON="${TMP_DIR}/edg.json"
VRN_JSON="${TMP_DIR}/vrn.json"

echo "[1/2] collect EDG write-mirror status"
ssh "${EDG_HOST}" "sudo python3 -" <<'PY' > "${EDG_JSON}"
import json
import pathlib
import subprocess

env_path = pathlib.Path('/etc/wireguard/wg-portal.env')
audit_path = pathlib.Path('/var/lib/wg-portal/audit.jsonl')
env = {}
if env_path.exists():
    for line in env_path.read_text(encoding='utf-8', errors='replace').splitlines():
        line = line.strip()
        if not line or line.startswith('#') or '=' not in line:
            continue
        k, v = line.split('=', 1)
        env[k.strip()] = v.strip()

def is_active(unit: str) -> str:
    p = subprocess.run(['systemctl', 'is-active', unit], capture_output=True, text=True, check=False)
    return (p.stdout or p.stderr or '').strip()

events = []
if audit_path.exists():
    for raw in audit_path.read_text(encoding='utf-8', errors='replace').splitlines():
        try:
            item = json.loads(raw)
        except Exception:
            continue
        if item.get('event') in ('api_create', 'api_remove', 'api_block', 'api_reissue', 'api_set_uplink'):
            events.append({
                'ts': item.get('ts'),
                'event': item.get('event'),
                'peer_id': item.get('peer_id'),
                'ok': item.get('ok'),
            })

print(json.dumps({
    'write_mirror_enabled': env.get('JSTUN_DB_WRITE_MIRROR_ENABLED', ''),
    'db_host': env.get('JSTUN_DB_HOST', ''),
    'db_port': env.get('JSTUN_DB_PORT', ''),
    'db_name': env.get('JSTUN_DB_NAME', ''),
    'db_user': env.get('JSTUN_DB_USER', ''),
    'db_tunnel_unit': is_active('jstun-shadow-write-db-tunnel.service'),
    'control_api_unit': is_active('wg-control-api.service'),
    'recent_write_events': events[-10:],
}, ensure_ascii=False))
PY

echo "[2/2] collect VRN mirrored DB status"
ssh "${VRN_HOST}" "PGPASSWORD=${DB_PASS} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -At -c \"select json_build_object('event_counts', json_build_object('api_create', (select count(*) from events where event_type = 'api_create'), 'api_remove', (select count(*) from events where event_type = 'api_remove')), 'recent_events', coalesce((select json_agg(t order by occurred_at desc, event_id desc) from (select event_id, event_type, occurred_at, peer_id from events where event_type in ('api_create','api_remove','api_block','api_reissue','api_set_uplink') order by occurred_at desc, event_id desc limit 10) t), '[]'::json));\"" > "${VRN_JSON}"

python3 - <<'PY' "${EDG_JSON}" "${VRN_JSON}"
import json
import pathlib
import sys

edg = json.loads(pathlib.Path(sys.argv[1]).read_text())
vrn = json.loads(pathlib.Path(sys.argv[2]).read_text())

ok = (
    edg.get('write_mirror_enabled') == '1'
    and edg.get('db_tunnel_unit') == 'active'
    and edg.get('control_api_unit') == 'active'
)

print(json.dumps({
    'ok': ok,
    'edg': edg,
    'vrn': vrn,
}, ensure_ascii=False, indent=2))
PY
