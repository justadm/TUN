#!/usr/bin/env bash
set -euo pipefail

VRN_HOST="${1:-vrn}"
DB_HOST="${JSTUN_SHADOW_PGHOST:-127.0.0.1}"
DB_PORT="${JSTUN_SHADOW_PGPORT:-15432}"
DB_NAME="${JSTUN_SHADOW_PGDATABASE:-jstun_shadow}"
DB_USER="${JSTUN_SHADOW_PGUSER:-jstun_shadow}"
DB_PASS="${JSTUN_SHADOW_PGPASSWORD:-change-me}"
IMPORT_PLAN="${IMPORT_PLAN:-.tmp/migration-dry-run/edg-import-plan.json}"

if [[ ! -f "${IMPORT_PLAN}" ]]; then
  echo "missing import plan: ${IMPORT_PLAN}" >&2
  exit 1
fi

readarray -t EXPECTED < <(
  python3 - <<'PY' "${IMPORT_PLAN}"
import json, sys, pathlib
plan = json.loads(pathlib.Path(sys.argv[1]).read_text())
print(len(plan["tables"]["peers"]))
print(len(plan["tables"]["peer_routing_policy"]))
print(len(plan["tables"]["peer_runtime_state"]))
print(len(plan["tables"]["billing_records"]))
print(len(plan["tables"]["events"]))
print(",".join(sorted(p["peer_id"] for p in plan["tables"]["peers"][:5])))
PY
)

EXPECTED_PEERS="${EXPECTED[0]}"
EXPECTED_POLICY="${EXPECTED[1]}"
EXPECTED_RUNTIME="${EXPECTED[2]}"
EXPECTED_BILLING="${EXPECTED[3]}"
EXPECTED_EVENTS="${EXPECTED[4]}"
EXPECTED_SAMPLE="${EXPECTED[5]}"

echo "[1/5] verify counts on VRN shadow DB"
ACTUAL_COUNTS="$(
  ssh "${VRN_HOST}" "PGPASSWORD=${DB_PASS} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -At -F '|' -c \"select 'peers', count(*) from peers union all select 'peer_routing_policy', count(*) from peer_routing_policy union all select 'peer_runtime_state', count(*) from peer_runtime_state union all select 'billing_records', count(*) from billing_records union all select 'events', count(*) from events order by 1;\""
)"
printf '%s\n' "${ACTUAL_COUNTS}"

echo "[2/5] verify sample peer ids"
ACTUAL_SAMPLE="$(
  ssh "${VRN_HOST}" "PGPASSWORD=${DB_PASS} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -At -c \"select string_agg(peer_id, ',') from (select peer_id from peers order by peer_id asc limit 5) t;\""
)"
echo "expected_sample=${EXPECTED_SAMPLE}"
echo "actual_sample=${ACTUAL_SAMPLE}"

echo "[3/5] verify routing rows cover all peers"
ssh "${VRN_HOST}" "PGPASSWORD=${DB_PASS} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -At -F '|' -c \"select count(*) from peers p left join peer_routing_policy r using(peer_id) where r.peer_id is null;\" | sed 's/^/missing_routing_rows=/'"
ssh "${VRN_HOST}" "PGPASSWORD=${DB_PASS} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -At -F '|' -c \"select count(*) from peers p left join peer_runtime_state r using(peer_id) where r.peer_id is null;\" | sed 's/^/missing_runtime_rows=/'"

echo "[4/5] verify billing peer linkage"
ssh "${VRN_HOST}" "PGPASSWORD=${DB_PASS} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -At -F '|' -c \"select count(*) from billing_records b left join peers p using(peer_id) where p.peer_id is null;\" | sed 's/^/orphaned_billing_rows=/'"

echo "[5/5] verify historical event fallback markers"
ssh "${VRN_HOST}" "PGPASSWORD=${DB_PASS} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -At -F '|' -c \"select count(*) from events where peer_id is null and metadata ? 'legacy_peer_id_raw';\" | sed 's/^/events_with_legacy_peer_marker=/'"

python3 - <<'PY' "${EXPECTED_PEERS}" "${EXPECTED_POLICY}" "${EXPECTED_RUNTIME}" "${EXPECTED_BILLING}" "${EXPECTED_EVENTS}" "${ACTUAL_COUNTS}" "${EXPECTED_SAMPLE}" "${ACTUAL_SAMPLE}"
import sys

expected = {
    "peers": int(sys.argv[1]),
    "peer_routing_policy": int(sys.argv[2]),
    "peer_runtime_state": int(sys.argv[3]),
    "billing_records": int(sys.argv[4]),
    "events": int(sys.argv[5]),
}
actual_lines = [line for line in sys.argv[6].splitlines() if line.strip()]
actual = {}
for line in actual_lines:
    key, value = line.split("|", 1)
    actual[key] = int(value)

missing = [key for key, value in expected.items() if actual.get(key) != value]
if missing:
    print("count_mismatch=" + ",".join(f"{k}:{expected[k]}!={actual.get(k)}" for k in missing), file=sys.stderr)
    raise SystemExit(1)

if sys.argv[7] != sys.argv[8]:
    print(f"sample_peer_mismatch={sys.argv[7]}!={sys.argv[8]}", file=sys.stderr)
    raise SystemExit(1)

print("shadow_read_smoke=ok")
PY
