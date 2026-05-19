#!/usr/bin/env bash
set -euo pipefail

host="${1:-edg}"
gateway="${2:-vrn}"
uplink="${3:-nyc}"
wave="${4:-batch-canary}"
note="${5:-batch-note}"

ssh "$host" "sudo python3 -" <<'PY' "$gateway" "$uplink" "$wave" "$note"
from pathlib import Path
import json
import time
import urllib.request
import urllib.parse
import re
import sys

gateway, uplink, wave, note = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]


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
    with urllib.request.urlopen(req, timeout=40) as resp:
        return {"status": resp.status, "html": resp.read().decode("utf-8", "replace")}


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
    print(json.dumps({"ok": False, "error": "need_two_active_peers_for_batch_canary"}, ensure_ascii=False))
    raise SystemExit(1)

peer_ids_text = "\n".join(targets)
report = {"targets": targets, "gateway": gateway, "uplink": uplink, "wave": wave, "note": note}

meta_resp = post_form(base + f"/admin/action/?token={urllib.parse.quote(admin_token)}", {
    "action": "batch_set_migration_meta",
    "peer_ids": peer_ids_text,
    "migration_wave": wave,
    "migration_note": note,
    "next": "/admin/edges/edg/?placement=all",
})
report["meta_status"] = meta_resp["status"]

check_resp = post_form(base + f"/admin/action/?token={urllib.parse.quote(admin_token)}", {
    "action": "batch_set_migration_checklist",
    "peer_ids": peer_ids_text,
    "migration_issued": "1",
    "migration_client_imported": "1",
    "next": "/admin/edges/edg/?placement=all",
})
report["check_status"] = check_resp["status"]

detail = urllib.request.urlopen(urllib.request.Request(base + f"/admin/peers/{targets[0]}/?token={admin_token}"), timeout=25).read().decode("utf-8", "replace")
report["detail_has_wave"] = wave in detail
report["detail_has_note"] = note in detail
report["detail_has_checklist"] = ("client imported" in detail and "issued" in detail)

batch_resp = post_form(base + f"/admin/action/?token={urllib.parse.quote(admin_token)}", {
    "action": "batch_migrate_gateway",
    "peer_ids": peer_ids_text,
    "gateway": gateway,
    "uplink": uplink,
    "migration_wave": wave,
    "migration_note": note,
    "next": "/admin/edges/edg/?placement=all",
})
report["batch_status"] = batch_resp["status"]
html = batch_resp["html"]
pairs = re.findall(r"<li><code>([^<]+)</code> → <code>([^<]+)</code></li>", html)
report["pairs"] = pairs

ok = (
    meta_resp["status"] == 200
    and check_resp["status"] == 200
    and report["detail_has_wave"]
    and report["detail_has_note"]
    and report["detail_has_checklist"]
    and batch_resp["status"] == 200
    and len(pairs) >= 1
)

new_ids = []
for _old, new_id in pairs:
    new_ids.append(new_id)
    time.sleep(1)
    routing = api("GET", shadow + f"/peers/{new_id}/routing", shadow_token)
    report.setdefault("routing", {})[new_id] = routing
    if not (
        routing["status"] == 200
        and (routing["json"] or {}).get("effective_edge") == gateway
        and (routing["json"] or {}).get("preferred_uplink") == uplink
    ):
        ok = False

for new_id in new_ids:
    removed = api("POST", local + f"/peers/{new_id}/remove", api_token, {})
    report.setdefault("removed", {})[new_id] = removed
    if not bool((removed.get("json") or {}).get("ok")):
        ok = False

print(json.dumps({"ok": ok, "report": report}, ensure_ascii=False, sort_keys=True))
raise SystemExit(0 if ok else 1)
PY
