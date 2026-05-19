#!/usr/bin/env bash
set -euo pipefail

host="${1:-edg}"
wave="${2:-rollback-canary}"

ssh "$host" "sudo python3 -" <<'PY' "$wave"
from pathlib import Path
import json
import time
import urllib.request
import urllib.parse
import re
import sys

wave = sys.argv[1]

def load_env(path):
    env = {}
    for line in Path(path).read_text(encoding="utf-8", errors="replace").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        env[k] = v.strip()
    return env

def api(method, url, token, payload=None):
    data = None
    headers = {"X-API-Token": token}
    if payload is not None:
        data = json.dumps(payload).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=25) as resp:
        return {"status": resp.status, "json": json.loads(resp.read().decode())}

def post_form(url, form):
    data = urllib.parse.urlencode(form).encode()
    req = urllib.request.Request(url, data=data, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=40) as resp:
            return {"status": resp.status, "html": resp.read().decode("utf-8", "replace")}
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", "replace")
        return {"status": exc.code, "html": body, "error": f"http_{exc.code}"}

env = load_env("/etc/wireguard/wg-portal.env")
admin_token = env["WG_PORTAL_ADMIN_TOKEN"]
api_token = env["WG_CONTROL_API_TOKEN"]
shadow_token = env["JSTUN_READ_SHADOW_TOKEN"]
shadow = env["JSTUN_READ_SHADOW_BASE"]
local = "http://127.0.0.1:18110/v1"
base = "http://10.200.0.4:18090"

peers = api("GET", local + "/peers", api_token)
items = [x for x in (peers.get("json") or {}).get("items", []) if str(x.get("status") or "") == "active"]
items.sort(key=lambda x: int(x.get("last_handshake_unix") or 0), reverse=True)
targets = [str(x.get("id") or "") for x in items[:2] if str(x.get("id") or "")]
if len(targets) < 2:
    print(json.dumps({"ok": False, "error": "need_two_active_peers_for_wave_rollback"}, ensure_ascii=False))
    raise SystemExit(1)

peer_ids_text = "\n".join(targets)
report = {"targets": targets, "wave": wave}

meta = post_form(base + f"/admin/action/?token={urllib.parse.quote(admin_token)}", {
    "action": "batch_set_migration_meta",
    "peer_ids": peer_ids_text,
    "migration_wave": wave,
    "migration_note": "wave-rollback-setup",
    "next": f"/admin/waves/?wave={urllib.parse.quote(wave)}",
})
report["meta_status"] = meta["status"]
report["meta_error"] = meta.get("error")

rollback = post_form(base + f"/admin/action/?token={urllib.parse.quote(admin_token)}", {
    "action": "batch_wave_rollback_issue",
    "peer_ids": peer_ids_text,
    "migration_wave": wave,
    "gateway": "edg",
    "uplink": "ams",
    "migration_note": f"rollback:{wave}",
    "next": f"/admin/waves/?wave={urllib.parse.quote(wave)}",
})
report["rollback_status"] = rollback["status"]
report["rollback_error"] = rollback.get("error")
html = rollback["html"]
pairs = re.findall(r"<li><code>([^<]+)</code> → <code>([^<]+)</code></li>", html)
report["pairs"] = pairs
report["rollback_html_excerpt"] = html[:800]

check = post_form(base + f"/admin/action/?token={urllib.parse.quote(admin_token)}", {
    "action": "batch_wave_set_rollback_checklist",
    "peer_ids": peer_ids_text,
    "migration_rollback_issued": "1",
    "migration_rollback_validated": "1",
    "next": f"/admin/waves/?wave={urllib.parse.quote(wave)}",
})
report["check_status"] = check["status"]
report["check_error"] = check.get("error")

waves_html = urllib.request.urlopen(urllib.request.Request(base + f"/admin/waves/?token={admin_token}&wave={urllib.parse.quote(wave)}"), timeout=25).read().decode("utf-8", "replace")
report["waves_has_filter"] = f"Current wave filter: <code>{wave}</code>" in waves_html
report["waves_has_rollback"] = ("Rollback issued:" in waves_html and "Rollback validated:" in waves_html)
report["waves_has_policy"] = ("Recovery policy" in waves_html and "Current queue filter:" in waves_html and "Rollback candidates:" in waves_html)

ok = (meta["status"] == 200 and rollback["status"] == 200 and check["status"] == 200 and report["waves_has_filter"] and report["waves_has_rollback"] and report["waves_has_policy"] and len(pairs) >= 1)
for _old, new_id in pairs:
    time.sleep(1)
    routing = api("GET", shadow + f"/peers/{new_id}/routing", shadow_token)
    report.setdefault("routing", {})[new_id] = routing
    if not (routing["status"] == 200 and (routing["json"] or {}).get("effective_edge") == "edg" and (routing["json"] or {}).get("preferred_uplink") == "ams"):
        ok = False
    removed = api("POST", local + f"/peers/{new_id}/remove", api_token, {})
    report.setdefault("removed", {})[new_id] = removed
    if not bool((removed.get("json") or {}).get("ok")):
        ok = False

print(json.dumps({"ok": ok, "report": report}, ensure_ascii=False, sort_keys=True))
raise SystemExit(0 if ok else 1)
PY
