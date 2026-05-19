#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"
VRN_HOST="${2:-vrn}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

FAST_LOG="${TMP_DIR}/fast-cycle.log"
ADMIN_JSON="${TMP_DIR}/admin-compare.json"
SUMMARY_JSON="${TMP_DIR}/summary.json"
ROUTING_JSON="${TMP_DIR}/edg-routing-shadow.json"

echo "[1/5] run full VRN shadow fast cycle"
scripts/vrn_shadow_fast_cycle.sh "${EDG_HOST}" "${VRN_HOST}" | tee "${FAST_LOG}"

echo "[2/5] capture admin surface compare"
scripts/compare_portal_admin_surfaces.sh "${EDG_HOST}" "${VRN_HOST}" | tail -n1 > "${ADMIN_JSON}"

echo "[3/5] capture compact summary"
scripts/vrn_shadow_status_summary.sh "${VRN_HOST}" > "${SUMMARY_JSON}"

echo "[4/5] run EDG routing shadow smoke"
scripts/edg_admin_routing_shadow_smoke.sh "${EDG_HOST}" > "${ROUTING_JSON}"

echo "[5/5] evaluate canary gate"
python3 - <<'PY' "${FAST_LOG}" "${ADMIN_JSON}" "${SUMMARY_JSON}" "${ROUTING_JSON}"
import json
import pathlib
import sys

fast_log = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8", errors="replace")
admin = json.loads(pathlib.Path(sys.argv[2]).read_text(encoding="utf-8"))
summary = json.loads(pathlib.Path(sys.argv[3]).read_text(encoding="utf-8"))
routing = json.loads(pathlib.Path(sys.argv[4]).read_text(encoding="utf-8"))

checks = {
    "shadow_read_smoke_ok": "shadow_read_smoke=ok" in fast_log,
    "vrn_peers_source_db": summary.get("vrn", {}).get("peers_source") == "db",
    "vrn_uplinks_source_db": summary.get("vrn", {}).get("uplinks_source") == "db",
    "vrn_events_source_db": summary.get("vrn", {}).get("events_source") == "db",
    "vrn_live_online_positive": int(summary.get("vrn", {}).get("live_online_count") or 0) >= 1,
    "vrn_live_events_positive": int(summary.get("vrn", {}).get("live_events_count") or 0) >= 1,
    "admin_status_ok": bool(admin.get("summary", {}).get("vrn_status_ok")),
    "dashboard_has_match": bool(admin.get("summary", {}).get("dashboard_has_match")),
    "peers_has_match": bool(admin.get("summary", {}).get("peers_has_match")),
    "events_has_match": bool(admin.get("summary", {}).get("events_has_match")),
    "live_has_match": bool(admin.get("summary", {}).get("live_has_match")),
    "detail_has_match": bool(admin.get("summary", {}).get("detail_has_match")),
    "routing_shadow_detail_ok": bool(routing.get("ok")),
}

ok = all(checks.values())
result = {
    "ok": ok,
    "checks": checks,
    "summary": summary.get("vrn", {}),
    "admin_compare": admin.get("summary", {}),
    "routing": routing,
}
print(json.dumps(result, ensure_ascii=False, sort_keys=True))
raise SystemExit(0 if ok else 1)
PY
