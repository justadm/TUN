#!/usr/bin/env bash
set -euo pipefail

if [ "${1:-}" = "" ]; then
  echo "usage: $0 <link_id> [command_type]"
  echo "example: $0 client:dev-01:tun-main reconnect"
  exit 2
fi

LINK_ID="$1"
COMMAND_TYPE="${2:-reconnect}"
API_BASE="${MONITORING_API_BASE:-http://127.0.0.1:18070}"
REQUEST_SOURCE="${MONITORING_COMMAND_SOURCE:-operator_ui}"
REQUESTED_BY="${MONITORING_REQUESTED_BY:-monitoring-smoke}"
IDEMPOTENCY_KEY="${MONITORING_IDEMPOTENCY_KEY:-smoke-${COMMAND_TYPE}-$(date +%s)}"
TIMEOUT_SEC="${MONITORING_SMOKE_TIMEOUT_SEC:-60}"
POLL_SEC="${MONITORING_SMOKE_POLL_SEC:-2}"

echo "[smoke] api=${API_BASE} link_id=${LINK_ID} command=${COMMAND_TYPE}"

REQ_BODY=$(cat <<JSON
{
  "target_type": "link",
  "target_id": "${LINK_ID}",
  "command_type": "${COMMAND_TYPE}",
  "reason": "monitoring smoke",
  "requested_by": "${REQUESTED_BY}",
  "request_source": "${REQUEST_SOURCE}",
  "idempotency_key": "${IDEMPOTENCY_KEY}",
  "args": {}
}
JSON
)

CREATE_JSON=$(curl -sS -X POST "${API_BASE}/v1/monitor/commands" \
  -H "Content-Type: application/json" \
  --data "${REQ_BODY}")

COMMAND_ID=$(python3 -c 'import json,sys; print((json.loads(sys.stdin.read() or "{}").get("command") or {}).get("command_id",""))' <<<"${CREATE_JSON}")
CREATE_STATUS=$(python3 -c 'import json,sys; print((json.loads(sys.stdin.read() or "{}").get("command") or {}).get("status",""))' <<<"${CREATE_JSON}")

if [ "${COMMAND_ID}" = "" ]; then
  echo "[smoke] failed to create command"
  echo "${CREATE_JSON}"
  exit 1
fi

echo "[smoke] command_id=${COMMAND_ID} initial_status=${CREATE_STATUS}"

START_TS=$(date +%s)
while true; do
  DETAIL_JSON=$(curl -sS "${API_BASE}/v1/monitor/commands/${COMMAND_ID}")
  STATUS=$(python3 -c 'import json,sys; print((json.loads(sys.stdin.read() or "{}").get("command") or {}).get("status",""))' <<<"${DETAIL_JSON}")
  echo "[smoke] status=${STATUS}"
  if [ "${STATUS}" = "succeeded" ]; then
    echo "[smoke] success"
    exit 0
  fi
  if [ "${STATUS}" = "failed" ] || [ "${STATUS}" = "timed_out" ] || [ "${STATUS}" = "canceled" ]; then
    echo "[smoke] command failed"
    echo "${DETAIL_JSON}"
    exit 1
  fi
  NOW_TS=$(date +%s)
  if [ $((NOW_TS - START_TS)) -ge "${TIMEOUT_SEC}" ]; then
    echo "[smoke] timeout waiting for completion"
    echo "${DETAIL_JSON}"
    exit 1
  fi
  sleep "${POLL_SEC}"
done
