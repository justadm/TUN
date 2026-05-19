#!/usr/bin/env bash
set -euo pipefail

VRN_HOST="${1:-vrn}"
shift || true

if [[ "$#" -lt 1 ]]; then
  echo "usage: $0 <vrn-host> <peer-id> [peer-id ...]" >&2
  exit 1
fi

DB_HOST="${JSTUN_SHADOW_PGHOST:-127.0.0.1}"
DB_PORT="${JSTUN_SHADOW_PGPORT:-15432}"
DB_NAME="${JSTUN_SHADOW_PGDATABASE:-jstun_shadow}"
DB_USER="${JSTUN_SHADOW_PGUSER:-jstun_shadow}"
DB_PASS="${JSTUN_SHADOW_PGPASSWORD:-change-me}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

printf '%s\n' "$@" > "${TMP_DIR}/peer_ids.txt"
scp "${TMP_DIR}/peer_ids.txt" "${VRN_HOST}:/tmp/jstun-peer-ids.txt" >/dev/null

ssh "${VRN_HOST}" \
  "DB_HOST='${DB_HOST}' DB_PORT='${DB_PORT}' DB_NAME='${DB_NAME}' DB_USER='${DB_USER}' DB_PASS='${DB_PASS}' python3 -" <<'PY'
import json
import os
import pathlib
import subprocess

peer_ids = [line.strip() for line in pathlib.Path('/tmp/jstun-peer-ids.txt').read_text().splitlines() if line.strip()]
audit_path = pathlib.Path('/var/lib/jstun-shadow/runtime/audit.jsonl')
peer_dir = pathlib.Path('/var/lib/jstun-shadow/runtime/peers')
db_cmd = [
    'psql',
    '-h', os.environ['DB_HOST'],
    '-p', os.environ['DB_PORT'],
    '-U', os.environ['DB_USER'],
    '-d', os.environ['DB_NAME'],
    '-At',
    '-F', '|',
]
env = dict(os.environ)
env['PGPASSWORD'] = os.environ['DB_PASS']


def db_one(sql: str):
    p = subprocess.run(db_cmd + ['-c', sql], capture_output=True, text=True, env=env, check=False)
    if p.returncode != 0:
        return {'db_error': (p.stderr or p.stdout or 'db query failed').strip()}
    raw = (p.stdout or '').strip()
    return raw


audit_records = []
if audit_path.exists():
    for line in audit_path.read_text(encoding='utf-8').splitlines():
        try:
            audit_records.append(json.loads(line))
        except Exception:
            continue

report = {
    'format': 'jstun-write-parity-report/v1',
    'scope': 'vrn_shadow',
    'items': [],
}

for peer_id in peer_ids:
    item = {'peer_id': peer_id}
    peer_file = peer_dir / f'{peer_id}.json'
    if peer_file.exists():
        try:
            data = json.loads(peer_file.read_text(encoding='utf-8'))
            item['runtime'] = {
                'exists': True,
                'status': data.get('status'),
                'allowed_ip': data.get('allowed_ip'),
                'label': data.get('label'),
                'created_at': data.get('created_at'),
                'removed_at': data.get('removed_at'),
            }
        except Exception as e:
            item['runtime'] = {'exists': True, 'parse_error': str(e)}
    else:
        item['runtime'] = {'exists': False}

    audit_hits = [r for r in audit_records if str(r.get('peer_id') or '') == peer_id and str(r.get('event') or '').startswith('api_')]
    item['audit'] = {
        'count': len(audit_hits),
        'events': [
            {
                'ts': r.get('ts'),
                'event': r.get('event'),
                'ok': r.get('ok'),
                'label': r.get('label'),
            }
            for r in audit_hits[-10:]
        ],
    }

    safe_peer_id = peer_id.replace("'", "''")
    rows = db_one(f"""
select json_build_object(
  'peer', (select row_to_json(t) from (
      select peer_id, status, allowed_ip::text as allowed_ip, label, created_at, removed_at
      from peers where peer_id = '{safe_peer_id}'
  ) t),
  'runtime', (select row_to_json(t) from (
      select peer_id, active_uplink_id, last_handshake_at, rx_bytes, tx_bytes
      from peer_runtime_state where peer_id = '{safe_peer_id}'
  ) t),
  'policy', (select row_to_json(t) from (
      select peer_id, policy_mode, preferred_uplink_id, failover_uplink_id
      from peer_routing_policy where peer_id = '{safe_peer_id}'
  ) t),
  'effective', (select row_to_json(t) from (
      select peer_id, effective_uplink_id, effective_fallback_path
      from peer_effective_routing where peer_id = '{safe_peer_id}'
  ) t),
  'events', coalesce((select json_agg(t order by occurred_at, event_id) from (
      select event_id, event_type, occurred_at, reason
      from events where peer_id = '{safe_peer_id}' and event_type in ('api_create','api_remove','api_block','api_reissue','api_set_uplink')
  ) t), '[]'::json)
);
""")
    if isinstance(rows, str) and rows:
        item['db'] = json.loads(rows)
    elif rows:
        item['db'] = rows
    else:
        item['db'] = {'db_error': 'no rows'}

    warnings = []
    runtime_allowed_ip = ((item.get('runtime') or {}).get('allowed_ip') or '')
    db_peer = ((item.get('db') or {}).get('peer') or {})
    db_allowed_ip = db_peer.get('allowed_ip') or ''
    if runtime_allowed_ip and db_allowed_ip and runtime_allowed_ip != db_allowed_ip:
        warnings.append(f'allowed_ip_mismatch:{runtime_allowed_ip}!={db_allowed_ip}')
    if runtime_allowed_ip.startswith('10.8.') or db_allowed_ip.startswith('10.8.'):
        warnings.append('legacy_pool_address')
    if runtime_allowed_ip.startswith('10.250.') or db_allowed_ip.startswith('10.250.'):
        warnings.append('shadow_pool_address')
    if not ((item.get('db') or {}).get('events') or []):
        warnings.append('missing_db_write_events')
    item['warnings'] = warnings
    report['items'].append(item)

print(json.dumps(report, ensure_ascii=False, indent=2))
PY
