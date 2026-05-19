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
            return urllib.request.urlopen(url, timeout=15).read().decode("utf-8", "replace")
        except urllib.error.URLError as e:
            last = e
            time.sleep(sleep_sec)
    raise last


out = {"ok": True}
compact = fetch(f"{BASE}/admin/peers/?token={ADMIN_TOKEN}")
full = fetch(f"{BASE}/admin/peers/?token={ADMIN_TOKEN}&view=full")
detail = fetch(f"{BASE}/admin/peers/p36087424df0d3c47/?token={ADMIN_TOKEN}")

out["compact"] = {
    "read_source": ("Read source:" in compact and ">EDG<" in compact and ">VRN<" in compact),
    "current_compact": "current=<code>compact</code>" in compact,
    "compact_columns": all(x in compact for x in ["Маршрут", "Handshake", "IP (реальный)", "Действия"]),
    "compact_absent_dense": "Effective edge" not in compact,
}
out["full"] = {
    "current_full": "current=<code>full</code>" in full,
    "full_columns": all(x in full for x in ["Effective edge", "Preferred", "Failover", "IP (реальный)", "Действия"]),
}
out["detail"] = {
    "migration_reissue_block": "Migration / reissue to target edge" in detail,
    "migration_state_block": "Migration state" in detail,
    "migration_checklist": "client imported" in detail and "old removed" in detail,
}
out["ok"] = all(v for section in out.values() if isinstance(section, dict) for v in section.values())
print(json.dumps(out, ensure_ascii=False))
PY
