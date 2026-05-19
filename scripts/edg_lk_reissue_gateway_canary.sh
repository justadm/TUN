#!/usr/bin/env bash
set -euo pipefail

host="${1:-edg}"
gateway="${2:-vrn}"
uplink="${3:-nyc}"

ssh "$host" "sudo python3 -" <<'PY' "$gateway" "$uplink"
from pathlib import Path
import json
import time
import urllib.request
import urllib.parse
import re
import sys

gateway, uplink = sys.argv[1], sys.argv[2]


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
    with urllib.request.urlopen(req, timeout=20) as resp:
        return {"status": resp.status, "json": json.loads(resp.read().decode())}


def post_form(url, form, headers=None):
    data = urllib.parse.urlencode(form).encode()
    req = urllib.request.Request(url, data=data, method="POST", headers=headers or {})
    with urllib.request.urlopen(req, timeout=30) as resp:
        return {"status": resp.status, "html": resp.read().decode("utf-8", "replace")}


env = load_env("/etc/wireguard/wg-portal.env")
token = env["WG_CONTROL_API_TOKEN"]
shadow_token = env["JSTUN_READ_SHADOW_TOKEN"]
base = "http://10.200.0.4:18090"
shadow = env["JSTUN_READ_SHADOW_BASE"]
local = "http://127.0.0.1:18110/v1"

peers = api("GET", local + "/peers", token)
items = [x for x in (peers.get("json") or {}).get("items", []) if str(x.get("status") or "") == "active" and int(x.get("last_handshake_unix") or 0) > 0]
items.sort(key=lambda x: int(x.get("last_handshake_unix") or 0), reverse=True)
item = items[0] if items else {}
peer_id = str(item.get("id") or "")
client_ip = str(item.get("allowed_ip") or "").split("/", 1)[0]
if not peer_id or not client_ip:
    print(json.dumps({"ok": False, "error": "no_active_peer_for_lk_reissue_canary"}, ensure_ascii=False))
    raise SystemExit(1)

headers = {"X-Forwarded-For": client_ip}
resp = post_form(base + "/lk/reissue/", {"gateway": gateway, "uplink": uplink}, headers=headers)
html = resp["html"]
match = re.search(r"<code id='peerId'>([^<]+)</code>", html)
new_peer_id = (match.group(1).strip() if match else "")
report = {
    "peer_id": peer_id,
    "client_ip": client_ip,
    "gateway": gateway,
    "uplink": uplink,
    "status": resp["status"],
    "new_peer_id": new_peer_id,
    "has_gateway_line": "Gateway:" in html,
    "has_uplink_line": "Uplink:" in html,
}

if not new_peer_id:
    print(json.dumps({"ok": False, "stage": "lk_reissue", "report": report}, ensure_ascii=False))
    raise SystemExit(1)

time.sleep(2)
routing = api("GET", shadow + f"/peers/{new_peer_id}/routing", shadow_token)
report["routing"] = routing
removed = api("POST", local + f"/peers/{new_peer_id}/remove", token, {})
report["remove"] = removed

ok = (
    resp["status"] == 200
    and routing["status"] == 200
    and (routing["json"] or {}).get("peer_id") == new_peer_id
    and (routing["json"] or {}).get("ingress_edge") == gateway
    and (routing["json"] or {}).get("effective_edge") == gateway
    and (routing["json"] or {}).get("preferred_uplink") == uplink
    and bool((removed.get("json") or {}).get("ok"))
)
print(json.dumps({"ok": ok, "report": report}, ensure_ascii=False, sort_keys=True))
raise SystemExit(0 if ok else 1)
PY
