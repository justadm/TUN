#!/usr/bin/env bash
set -euo pipefail

VRN_HOST="${1:-vrn}"
API_HOST="${JSTUN_SHADOW_API_HOST:-127.0.0.1}"
API_PORT="${JSTUN_SHADOW_API_PORT:-18190}"
API_TOKEN="${JSTUN_SHADOW_CONTROL_API_TOKEN:-shadow-read-smoke-token}"
DB_HOST="${JSTUN_SHADOW_PGHOST:-127.0.0.1}"
DB_PORT="${JSTUN_SHADOW_PGPORT:-15432}"
DB_NAME="${JSTUN_SHADOW_PGDATABASE:-jstun_shadow}"
DB_USER="${JSTUN_SHADOW_PGUSER:-jstun_shadow}"
DB_PASS="${JSTUN_SHADOW_PGPASSWORD:-change-me}"
STAMP="$(date -u +%Y%m%d%H%M%S)"
LABEL="api-write-canary-${STAMP}"

echo "[1/6] create peer through shadow control-api"
CREATE_JSON="$(ssh "${VRN_HOST}" "curl -fsS -H 'X-API-Token: ${API_TOKEN}' -H 'Content-Type: application/json' -d '{\"label\":\"${LABEL}\",\"ttl_sec\":3600}' http://${API_HOST}:${API_PORT}/v1/peers/create")"
printf '%s\n' "${CREATE_JSON}"

readarray -t CREATE_FIELDS < <(
  python3 - <<'PY' "${CREATE_JSON}"
import json, sys
data = json.loads(sys.argv[1])
if not data.get("ok"):
    raise SystemExit("create failed")
print(data.get("id") or "")
print(data.get("allowed_ip") or "")
print("1" if data.get("mirror_warning") else "0")
PY
)
PEER_ID="${CREATE_FIELDS[0]}"
ALLOWED_IP="${CREATE_FIELDS[1]}"
HAS_WARNING="${CREATE_FIELDS[2]}"

if [[ -z "${PEER_ID}" || -z "${ALLOWED_IP}" ]]; then
  echo "missing create peer fields" >&2
  exit 1
fi

echo "[2/6] verify shadow runtime state"
ssh "${VRN_HOST}" "test -f /var/lib/jstun-shadow/runtime/peers/${PEER_ID}.json && python3 - <<'PY' '/var/lib/jstun-shadow/runtime/peers/${PEER_ID}.json' '${LABEL}' '${ALLOWED_IP}'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
label = sys.argv[2]
allowed_ip = sys.argv[3]
data = json.loads(path.read_text())
assert data['label'] == label
assert data['allowed_ip'] == allowed_ip
assert data['status'] == 'pending'
print('runtime_peer=ok')
PY"

echo "[3/6] verify mirrored create rows"
CREATE_VERIFY_SQL="
select json_build_object(
  'peer_pending', exists(select 1 from peers where peer_id = '${PEER_ID}' and label = '${LABEL}' and status = 'pending'),
  'policy_exists', exists(select 1 from peer_routing_policy where peer_id = '${PEER_ID}' and preferred_uplink_id = 'edg/ams'),
  'effective_exists', exists(select 1 from peer_effective_routing where peer_id = '${PEER_ID}' and effective_uplink_id = 'edg/ams'),
  'event_exists', exists(select 1 from events where peer_id = '${PEER_ID}' and event_type = 'api_create')
);
"
CREATE_VERIFY_JSON="$(ssh "${VRN_HOST}" "PGPASSWORD=${DB_PASS} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -At -c \"${CREATE_VERIFY_SQL}\"")"
printf '%s\n' "${CREATE_VERIFY_JSON}"
python3 - <<'PY' "${CREATE_VERIFY_JSON}" "${HAS_WARNING}"
import json, sys
data = json.loads(sys.argv[1])
missing = [k for k, v in data.items() if not v]
if missing:
    print("create_mirror_failed=" + ",".join(missing), file=sys.stderr)
    raise SystemExit(1)
if sys.argv[2] != "0":
    print("create_mirror_warning_present", file=sys.stderr)
    raise SystemExit(1)
print("create_mirror=ok")
PY

echo "[4/6] remove peer through shadow control-api"
REMOVE_JSON="$(ssh "${VRN_HOST}" "curl -fsS -X POST -H 'X-API-Token: ${API_TOKEN}' http://${API_HOST}:${API_PORT}/v1/peers/${PEER_ID}/remove")"
printf '%s\n' "${REMOVE_JSON}"

readarray -t REMOVE_FIELDS < <(
  python3 - <<'PY' "${REMOVE_JSON}"
import json, sys
data = json.loads(sys.argv[1])
if not data.get("ok"):
    raise SystemExit("remove failed")
print("1" if data.get("mirror_warning") else "0")
PY
)
REMOVE_WARNING="${REMOVE_FIELDS[0]}"

echo "[5/6] verify mirrored remove rows"
REMOVE_VERIFY_SQL="
select json_build_object(
  'peer_removed', exists(select 1 from peers where peer_id = '${PEER_ID}' and status = 'removed'),
  'remove_event_exists', exists(select 1 from events where peer_id = '${PEER_ID}' and event_type = 'api_remove'),
  'runtime_cleared', exists(select 1 from peer_runtime_state where peer_id = '${PEER_ID}' and active_uplink_id is null)
);
"
REMOVE_VERIFY_JSON="$(ssh "${VRN_HOST}" "PGPASSWORD=${DB_PASS} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -At -c \"${REMOVE_VERIFY_SQL}\"")"
printf '%s\n' "${REMOVE_VERIFY_JSON}"
python3 - <<'PY' "${REMOVE_VERIFY_JSON}" "${REMOVE_WARNING}"
import json, sys
data = json.loads(sys.argv[1])
missing = [k for k, v in data.items() if not v]
if missing:
    print("remove_mirror_failed=" + ",".join(missing), file=sys.stderr)
    raise SystemExit(1)
if sys.argv[2] != "0":
    print("remove_mirror_warning_present", file=sys.stderr)
    raise SystemExit(1)
print("remove_mirror=ok")
PY

echo "[6/6] confirm shadow peer file persisted as removed history"
ssh "${VRN_HOST}" "python3 - <<'PY' '/var/lib/jstun-shadow/runtime/peers/${PEER_ID}.json'
import json, pathlib, sys
data = json.loads(pathlib.Path(sys.argv[1]).read_text())
assert data['status'] == 'removed'
print('runtime_remove=ok')
PY"

echo "write_api_canary=ok peer_id=${PEER_ID}"
