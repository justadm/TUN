#!/usr/bin/env bash
set -euo pipefail

host="${1:-edg}"

ssh "$host" python3 - <<'PY'
import json
import time
import urllib.request
import urllib.error

ADMIN_TOKEN = "ee43b23332c280d9cee147d829dc29201dee0f227065578d"
BASE = "http://10.200.0.4:18090"


def fetch(url, attempts=15, sleep_sec=1):
    last = None
    for _ in range(attempts):
        try:
            return urllib.request.urlopen(url, timeout=10).read().decode("utf-8", "replace")
        except urllib.error.URLError as e:
            last = e
            time.sleep(sleep_sec)
    raise last

out = {"ok": True, "checks": []}
for mode in ("local", "shadow"):
    url = f"{BASE}/admin/edges/?token={ADMIN_TOKEN}&read_mode={mode}"
    html = fetch(url)
    check = {
        "mode": mode,
        "has_title": "Админ: edges" in html,
        "has_columns": "Clients active" in html and "NotRU primary" in html and "NotRU fallback" in html,
        "has_edg": "<code>edg</code>" in html,
        "has_vrn": "<code>vrn</code>" in html,
        "has_notru_primary": "edg/ams" in html,
        "has_open_link": "/admin/edges/edg/" in html,
        "has_shadow_toggle": "read_mode=shadow" in html,
        "has_local_toggle": "read_mode=local" in html,
    }
    detail = fetch(f"{BASE}/admin/edges/edg/?token={ADMIN_TOKEN}&read_mode={mode}")
    check["detail_title"] = "Админ: edge `edg`" in detail
    check["detail_uplinks"] = "Аплинки edge" in detail and "edg/ams" in detail
    check["detail_peer_placement"] = "Peer placement" in detail and "Intent peers:" in detail and "Effective peers:" in detail
    check["detail_operator_queues"] = "Operator queues" in detail and "Incoming queue:" in detail and "Outgoing queue:" in detail and "Cutover ready:" in detail
    check["detail_batch_actions"] = "Batch actions" in detail and "Issue migration profiles" in detail and "Set wave / notes" in detail and "Set checklist state" in detail and "Finalize old peers" in detail
    check["detail_bulk_ids"] = "Bulk peer IDs" in detail
    check["detail_drift_only"] = "Drift only" in detail and "Filtered placement" in detail
    check["detail_filters"] = "placement=all" in detail and "placement=intent" in detail and "placement=effective" in detail and "placement=drift" in detail and "placement=incoming" in detail and "placement=outgoing" in detail and "placement=cutover" in detail and "Current filter:" in detail
    check["detail_status_counters"] = "Active:" in detail and "Pending:" in detail and "Blocked:" in detail and "Expired:" in detail and "Removed:" in detail
    check["detail_back_link"] = "Назад к edges" in detail
    drift_only = fetch(f"{BASE}/admin/edges/edg/?token={ADMIN_TOKEN}&read_mode={mode}&placement=drift")
    check["detail_drift_filter_selected"] = "Current filter: <code>drift</code>" in drift_only
    incoming_only = fetch(f"{BASE}/admin/edges/edg/?token={ADMIN_TOKEN}&read_mode={mode}&placement=incoming")
    check["detail_incoming_filter_selected"] = "Current filter: <code>incoming</code>" in incoming_only
    waves = fetch(f"{BASE}/admin/waves/?token={ADMIN_TOKEN}&read_mode={mode}")
    check["waves_page"] = "Админ: waves" in waves and "Issued" in waves and "Old removed" in waves
    check["waves_csv_link"] = "/admin/waves.csv/" in waves and "Export CSV" in waves
    check["waves_recovery_policy"] = "Recovery policy" in waves and "Current queue filter:" in waves and "Rollback candidates" in waves and "Rollback ready" in waves and "Rollback finalized" in waves
    if "wave=" in waves:
        import re
        m = re.search(r"/admin/waves/\?wave=([^'\"&<>]+)", waves)
        if m:
            wave_value = m.group(1)
            wave_filtered = fetch(f"{BASE}/admin/waves/?token={ADMIN_TOKEN}&read_mode={mode}&wave={wave_value}")
            check["waves_filter"] = f"Current wave filter: <code>{wave_value}</code>" in wave_filtered
            check["waves_queue_filter"] = "Current queue filter:" in wave_filtered
            wave_csv = fetch(f"{BASE}/admin/waves.csv/?token={ADMIN_TOKEN}&read_mode={mode}&wave={wave_value}")
            check["waves_csv_content"] = "wave,peer_id,label,status" in wave_csv and "rollback_issued,rollback_validated,rollback_finalized" in wave_csv
        else:
            check["waves_filter"] = False
            check["waves_queue_filter"] = False
            check["waves_csv_content"] = False
    else:
        check["waves_filter"] = True
        check["waves_queue_filter"] = "Current queue filter:" in waves
        wave_csv = fetch(f"{BASE}/admin/waves.csv/?token={ADMIN_TOKEN}&read_mode={mode}")
        check["waves_csv_content"] = "wave,peer_id,label,status" in wave_csv and "rollback_issued,rollback_validated,rollback_finalized" in wave_csv
    check["ok"] = all(v for k, v in check.items() if k not in ("mode", "ok"))
    out["checks"].append(check)
    out["ok"] = out["ok"] and check["ok"]

print(json.dumps(out, ensure_ascii=False))
PY
