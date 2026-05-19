#!/usr/bin/env bash
set -euo pipefail

host="${1:-edg}"

ssh "$host" "sudo python3 -" <<'PY'
from pathlib import Path
import json
import time
import urllib.request
import urllib.parse


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
    with urllib.request.urlopen(req, timeout=30) as resp:
        return {"status": resp.status, "html": resp.read().decode("utf-8", "replace")}


env = load_env("/etc/wireguard/wg-portal.env")
admin_token = env["WG_PORTAL_ADMIN_TOKEN"]
api_token = env["WG_CONTROL_API_TOKEN"]
local = "http://127.0.0.1:18110/v1"
base = "http://10.200.0.4:18090"

created_ids = []
report = {}
for idx in (1, 2):
    label = f"finalize-canary-{idx}-{int(time.time())}"
    out = api("POST", local + "/peers/create", api_token, {"label": label, "gateway": "edg", "uplink": "ams"})
    if not (out["status"] == 200 and (out["json"] or {}).get("ok")):
        print(json.dumps({"ok": False, "stage": "create", "report": out}, ensure_ascii=False))
        raise SystemExit(1)
    created_ids.append(str((out["json"] or {}).get("id") or ""))

peer_ids_text = "\n".join(created_ids)
resp = post_form(base + f"/admin/action/?token={urllib.parse.quote(admin_token)}", {
    "action": "batch_finalize_old_peers",
    "peer_ids": peer_ids_text,
    "next": "/admin/edges/edg/?placement=all",
})
report["status"] = resp["status"]
report["created_ids"] = created_ids

ok = (resp["status"] == 200 and "Batch finalize finished" in resp["html"])
listed = api("GET", local + "/peers", api_token)
items = list((listed.get("json") or {}).get("items", []) or [])
status_map = {str(x.get("id") or ""): str(x.get("status") or "") for x in items}
report["list_status"] = listed["status"]
report["status_after"] = {pid: status_map.get(pid, "") for pid in created_ids}
bad = [pid for pid in created_ids if status_map.get(pid) != "removed"]
report["not_removed"] = bad
if bad:
    ok = False

print(json.dumps({"ok": ok, "report": report}, ensure_ascii=False, sort_keys=True))
raise SystemExit(0 if ok else 1)
PY
