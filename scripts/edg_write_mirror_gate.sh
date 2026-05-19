#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"
VRN_HOST="${2:-vrn}"
LABEL="${LABEL:-edg-write-canary-$(date -u +%Y%m%d%H%M%S)}"
DB_HOST="${JSTUN_SHADOW_PGHOST:-127.0.0.1}"
DB_PORT="${JSTUN_SHADOW_PGPORT:-15432}"
DB_NAME="${JSTUN_SHADOW_PGDATABASE:-jstun_shadow}"
DB_USER="${JSTUN_SHADOW_PGUSER:-jstun_shadow}"
DB_PASS="${JSTUN_SHADOW_PGPASSWORD:-change-me}"

echo "[1/8] create clean canary on EDG"
CREATE_JSON="$(
  ssh "${EDG_HOST}" "TOKEN=\$(sudo awk -F= '/^WG_CONTROL_API_TOKEN=/{print \$2}' /etc/wireguard/wg-portal.env); curl -sS -H \"X-API-Token: \${TOKEN}\" -H 'Content-Type: application/json' -d '{\"label\":\"${LABEL}\",\"ttl_sec\":3600}' http://127.0.0.1:18110/v1/peers/create"
)"
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
CREATE_WARN="${CREATE_FIELDS[2]}"

if [[ -z "${PEER_ID}" || -z "${ALLOWED_IP}" ]]; then
  echo "missing create fields" >&2
  exit 1
fi

echo "[2/8] verify EDG runtime peer file"
RUNTIME_JSON="$(
  ssh "${EDG_HOST}" "sudo python3 - <<'PY' '/var/lib/wg-portal/peers/${PEER_ID}.json'
import json, pathlib, sys
data = json.loads(pathlib.Path(sys.argv[1]).read_text())
print(json.dumps({'status': data.get('status'), 'allowed_ip': data.get('allowed_ip'), 'label': data.get('label')}, ensure_ascii=False))
PY"
)"
printf '%s\n' "${RUNTIME_JSON}"

echo "[3/8] verify VRN mirrored create state"
CREATE_DB_JSON="$(
  ssh "${VRN_HOST}" "PGPASSWORD=${DB_PASS} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -At -c \"select json_build_object('peer', (select row_to_json(t) from (select peer_id, status, allowed_ip::text as allowed_ip, label from peers where peer_id = '${PEER_ID}') t), 'events', coalesce((select json_agg(t order by occurred_at, event_id) from (select event_id, event_type, occurred_at from events where peer_id = '${PEER_ID}' and event_type in ('api_create','api_remove')) t), '[]'::json));\""
)"
printf '%s\n' "${CREATE_DB_JSON}"

echo "[4/8] validate create parity"
python3 - <<'PY' "${CREATE_WARN}" "${ALLOWED_IP}" "${LABEL}" "${RUNTIME_JSON}" "${CREATE_DB_JSON}"
import json, sys
warn, allowed_ip, label = sys.argv[1], sys.argv[2], sys.argv[3]
runtime = json.loads(sys.argv[4])
db = json.loads(sys.argv[5])
errors = []
if warn != "0":
    errors.append("create_mirror_warning_present")
if runtime.get("status") != "pending":
    errors.append("runtime_status_not_pending")
if runtime.get("allowed_ip") != allowed_ip:
    errors.append("runtime_allowed_ip_mismatch")
peer = db.get("peer") or {}
if peer.get("allowed_ip") != allowed_ip:
    errors.append("db_allowed_ip_mismatch")
if peer.get("label") != label:
    errors.append("db_label_mismatch")
if not any(e.get("event_type") == "api_create" for e in (db.get("events") or [])):
    errors.append("missing_db_api_create")
if errors:
    print("create_gate_failed=" + ",".join(errors), file=sys.stderr)
    raise SystemExit(1)
print("create_gate=ok")
PY

echo "[5/8] remove canary on EDG"
REMOVE_JSON="$(
  ssh "${EDG_HOST}" "TOKEN=\$(sudo awk -F= '/^WG_CONTROL_API_TOKEN=/{print \$2}' /etc/wireguard/wg-portal.env); curl -sS -X POST -H \"X-API-Token: \${TOKEN}\" http://127.0.0.1:18110/v1/peers/${PEER_ID}/remove"
)"
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
REMOVE_WARN="${REMOVE_FIELDS[0]}"

echo "[6/8] verify EDG runtime remove state"
RUNTIME_REMOVE_JSON="$(
  ssh "${EDG_HOST}" "sudo python3 - <<'PY' '/var/lib/wg-portal/peers/${PEER_ID}.json'
import json, pathlib, sys
data = json.loads(pathlib.Path(sys.argv[1]).read_text())
print(json.dumps({'status': data.get('status'), 'allowed_ip': data.get('allowed_ip'), 'removed_at': data.get('removed_at')}, ensure_ascii=False))
PY"
)"
printf '%s\n' "${RUNTIME_REMOVE_JSON}"

echo "[7/8] verify VRN mirrored remove state"
REMOVE_DB_JSON="$(
  ssh "${VRN_HOST}" "PGPASSWORD=${DB_PASS} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -At -c \"select json_build_object('peer', (select row_to_json(t) from (select peer_id, status, allowed_ip::text as allowed_ip, removed_at from peers where peer_id = '${PEER_ID}') t), 'events', coalesce((select json_agg(t order by occurred_at, event_id) from (select event_id, event_type, occurred_at from events where peer_id = '${PEER_ID}' and event_type in ('api_create','api_remove')) t), '[]'::json));\""
)"
printf '%s\n' "${REMOVE_DB_JSON}"

echo "[8/8] validate remove parity"
python3 - <<'PY' "${REMOVE_WARN}" "${ALLOWED_IP}" "${RUNTIME_REMOVE_JSON}" "${REMOVE_DB_JSON}" "${PEER_ID}"
import json, sys
warn, allowed_ip, peer_id = sys.argv[1], sys.argv[2], sys.argv[5]
runtime = json.loads(sys.argv[3])
db = json.loads(sys.argv[4])
errors = []
if warn != "0":
    errors.append("remove_mirror_warning_present")
if runtime.get("status") != "removed":
    errors.append("runtime_status_not_removed")
peer = db.get("peer") or {}
if peer.get("status") != "removed":
    errors.append("db_status_not_removed")
if peer.get("allowed_ip") != allowed_ip:
    errors.append("db_allowed_ip_mismatch")
events = db.get("events") or []
if not any(e.get("event_type") == "api_create" for e in events):
    errors.append("missing_db_api_create")
if not any(e.get("event_type") == "api_remove" for e in events):
    errors.append("missing_db_api_remove")
if errors:
    print("remove_gate_failed=" + ",".join(errors), file=sys.stderr)
    raise SystemExit(1)
print(json.dumps({"ok": True, "peer_id": peer_id, "allowed_ip": allowed_ip}))
PY
