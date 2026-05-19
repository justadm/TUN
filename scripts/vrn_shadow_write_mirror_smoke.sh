#!/usr/bin/env bash
set -euo pipefail

VRN_HOST="${1:-vrn}"
REMOTE_API="${JSTUN_SHADOW_REMOTE_API:-/opt/jstun-shadow/control-api/wg_control_api_server.py}"
DB_HOST="${JSTUN_SHADOW_PGHOST:-127.0.0.1}"
DB_PORT="${JSTUN_SHADOW_PGPORT:-15432}"
DB_NAME="${JSTUN_SHADOW_PGDATABASE:-jstun_shadow}"
DB_USER="${JSTUN_SHADOW_PGUSER:-jstun_shadow}"
DB_PASS="${JSTUN_SHADOW_PGPASSWORD:-change-me}"
STAMP="$(date -u +%Y%m%d%H%M%S)"
RND="$(python3 - <<'PY'
import secrets
print(secrets.token_hex(4))
PY
)"
PEER_ID="pwm${STAMP}${RND}"
LK_TOKEN="lk${STAMP}${RND}"
PUBLIC_KEY="mirror-smoke-${STAMP}-${RND}"
ALLOWED_IP="10.255.255.$(( (16#${RND:0:2} % 200) + 10 ))/32"
LABEL="mirror-smoke-${STAMP}"
EVENT_TYPE="mirror_smoke_create"

cleanup() {
  ssh "${VRN_HOST}" "PGPASSWORD=${DB_PASS} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -v ON_ERROR_STOP=1 -c \"begin; delete from events where peer_id = '${PEER_ID}' or (event_type = '${EVENT_TYPE}' and metadata->>'smoke_peer_id' = '${PEER_ID}'); delete from peer_effective_routing where peer_id = '${PEER_ID}'; delete from peer_routing_policy where peer_id = '${PEER_ID}'; delete from peer_runtime_state where peer_id = '${PEER_ID}'; delete from peers where peer_id = '${PEER_ID}'; commit;\" >/dev/null"
}
trap cleanup EXIT

echo "[1/4] mirror synthetic peer on ${VRN_HOST}"
ssh "${VRN_HOST}" "JSTUN_DB_WRITE_MIRROR_ENABLED=1 JSTUN_DB_WRITE_MIRROR_EVENTS_ENABLED=1 JSTUN_DB_HOST=${DB_HOST} JSTUN_DB_PORT=${DB_PORT} JSTUN_DB_NAME=${DB_NAME} JSTUN_DB_USER=${DB_USER} JSTUN_DB_PASSWORD=${DB_PASS} python3 - <<'PY' '${REMOTE_API}' '${PEER_ID}' '${LABEL}' '${PUBLIC_KEY}' '${ALLOWED_IP}' '${LK_TOKEN}' '${EVENT_TYPE}'
import datetime as dt
import importlib.util
import json
import pathlib
import sys

module_path = pathlib.Path(sys.argv[1])
peer_id = sys.argv[2]
label = sys.argv[3]
public_key = sys.argv[4]
allowed_ip = sys.argv[5]
lk_token = sys.argv[6]
event_type = sys.argv[7]
now = dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace('+00:00', 'Z')
spec = importlib.util.spec_from_file_location('wg_control_api_server', module_path)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
peer = {
    'id': peer_id,
    'label': label,
    'peer_type': 'default',
    'status': 'active',
    'public_key': public_key,
    'allowed_ip': allowed_ip,
    'endpoint': 'mirror-smoke.invalid:51820',
    'created_at': now,
    'ttl_sec': 3600,
    'lk_token': lk_token,
    'lk_token_created_at': now,
    'connected_at': now,
    'last_handshake_at': now,
    'last_handshake_unix': int(dt.datetime.now(dt.timezone.utc).timestamp()),
    'rx_bytes': 12345,
    'tx_bytes': 67890,
}
res_peer = mod.mirror_peer_to_db(peer, preferred_uplink='fra', policy_mode='fra', change_reason='mirror_smoke')
res_event = mod.mirror_event_to_db(event_type, peer_id=peer_id, uplink='fra', reason='mirror_smoke', metadata={'smoke_peer_id': peer_id, 'allowed_ip': allowed_ip}, actor_id='vrn_shadow_write_mirror_smoke')
print(json.dumps({'peer': res_peer, 'event': res_event}, ensure_ascii=False))
if not res_peer.get('ok') or not res_event.get('ok'):
    raise SystemExit(1)
PY"

echo "[2/4] verify mirrored rows"
VERIFY_SQL="
select json_build_object(
  'peer_exists', exists(select 1 from peers where peer_id = '${PEER_ID}' and status = 'active'),
  'runtime_exists', exists(select 1 from peer_runtime_state where peer_id = '${PEER_ID}' and active_uplink_id = 'edg/fra'),
  'policy_ok', exists(select 1 from peer_routing_policy where peer_id = '${PEER_ID}' and preferred_uplink_id = 'edg/fra' and failover_uplink_id = 'edg/ams' and policy_mode = 'fra'),
  'effective_ok', exists(select 1 from peer_effective_routing where peer_id = '${PEER_ID}' and effective_uplink_id = 'edg/fra' and effective_fallback_path = 'edg/ams'),
  'event_exists', exists(select 1 from events where peer_id = '${PEER_ID}' and event_type = '${EVENT_TYPE}')
);
"
VERIFY_JSON="$(ssh "${VRN_HOST}" "PGPASSWORD=${DB_PASS} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -At -c \"${VERIFY_SQL}\"")"
printf '%s\n' "${VERIFY_JSON}"

echo "[3/4] validate mirror result"
python3 - <<'PY' "${VERIFY_JSON}"
import json
import sys

data = json.loads(sys.argv[1])
missing = [k for k, v in data.items() if not v]
if missing:
    print("write_mirror_smoke_failed=" + ",".join(missing), file=sys.stderr)
    raise SystemExit(1)
print("write_mirror_smoke=ok")
PY

echo "[4/4] cleanup synthetic rows"
cleanup
trap - EXIT
