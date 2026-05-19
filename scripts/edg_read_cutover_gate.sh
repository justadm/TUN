#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

SHADOW_SMOKE_LOG="${TMP_DIR}/edg-shadow-smoke.log"
COMPARE_JSON="${TMP_DIR}/edg-local-vs-shadow.json"
ROUTING_JSON="${TMP_DIR}/edg-routing-shadow.json"

echo "[1/4] run EDG shadow smoke"
scripts/edg_admin_shadow_smoke.sh "${EDG_HOST}" | tee "${SHADOW_SMOKE_LOG}"

echo "[2/4] compare EDG local vs shadow"
scripts/compare_edg_local_shadow.sh "${EDG_HOST}" | tail -n1 > "${COMPARE_JSON}"

echo "[3/4] run EDG routing shadow smoke"
scripts/edg_admin_routing_shadow_smoke.sh "${EDG_HOST}" > "${ROUTING_JSON}"

echo "[4/4] evaluate cutover gate"
python3 - <<'PY' "${SHADOW_SMOKE_LOG}" "${COMPARE_JSON}" "${ROUTING_JSON}"
import json
import pathlib
import sys

smoke_log = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8", errors="replace")
compare = json.loads(pathlib.Path(sys.argv[2]).read_text(encoding="utf-8"))
routing = json.loads(pathlib.Path(sys.argv[3]).read_text(encoding="utf-8"))
summary = compare.get("summary", {})
detail_shape = summary.get("detail_shape_match") or {}
detail_core_match = all(
    bool(detail_shape.get(key))
    for key in ("real_ip", "uplink", "related_events")
)

checks = {
    "page_has_shadow": "page_has_shadow=yes" in smoke_log,
    "local_status_ok": bool(summary.get("local_status_ok")),
    "shadow_status_ok": bool(summary.get("shadow_status_ok")),
    "dashboard_match": bool(summary.get("dashboard_match")),
    "peers_match": bool(summary.get("peers_match")),
    "events_match": bool(summary.get("events_match")),
    "live_match": bool(summary.get("live_match")),
    "detail_core_match": bool(detail_core_match),
    "shadow_live_online_positive": int((summary.get("shadow_live_data") or {}).get("online_count") or 0) >= 1,
    "shadow_live_events_positive": int((summary.get("shadow_live_data") or {}).get("events_count") or 0) >= 1,
    "routing_shadow_detail_ok": bool(routing.get("ok")),
}

ok = all(checks.values())
result = {
    "ok": ok,
    "checks": checks,
    "summary": summary,
    "routing": routing,
}
print(json.dumps(result, ensure_ascii=False, sort_keys=True))
raise SystemExit(0 if ok else 1)
PY
