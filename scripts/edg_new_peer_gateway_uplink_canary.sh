#!/usr/bin/env bash
set -euo pipefail

host="${1:-edg}"
gateway="${2:-vrn}"
uplink="${3:-nyc}"
label="${LABEL:-new-gw-uplink-canary-$(date -u +%Y%m%d%H%M%S)}"

ssh "$host" "sudo python3 -" <<'PY' "$label" "$gateway" "$uplink"
import json
import sys
import time
import urllib.request
import urllib.parse
import urllib.error
from pathlib import Path

label, gateway, uplink = sys.argv[1], sys.argv[2], sys.argv[3]


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


def http_post_form(url, form, headers=None):
    data = urllib.parse.urlencode(form).encode()
    req = urllib.request.Request(url, data=data, method="POST", headers=headers or {})
    with urllib.request.urlopen(req, timeout=20) as resp:
        return {"status": resp.status, "html": resp.read().decode("utf-8", "replace")}


env = load_env("/etc/wireguard/wg-portal.env")
token = env["WG_CONTROL_API_TOKEN"]
shadow_token = env["JSTUN_READ_SHADOW_TOKEN"]
local = "http://127.0.0.1:18110/v1"
shadow = env["JSTUN_READ_SHADOW_BASE"]
portal = "http://10.200.0.4:18090/new/"
xff = (env.get("WG_PORTAL_NEW_IP_EXEMPT_IPS", "").split(",")[0] or "147.45.238.121").strip() or "147.45.238.121"

report = {"label": label, "gateway": gateway, "uplink": uplink}
created = http_post_form(portal, {"label": label, "gateway": gateway, "uplink": uplink}, headers={"X-Forwarded-For": xff})
report["create_status"] = created["status"]
report["xff"] = xff
html = created["html"]

def extract_between(text, left, right):
    if left not in text:
        return ""
    part = text.split(left, 1)[1]
    return part.split(right, 1)[0]

peer_id = extract_between(html, "<code id='peerId'>", "</code>").strip()
report["peer_id"] = peer_id
report["has_gateway_line"] = "Gateway:" in html
report["has_uplink_line"] = "Uplink:" in html
report["has_vrn_edge_line"] = "Gateway:" in html and "VRN" in html if gateway == "vrn" else True

if not peer_id:
    print(json.dumps({"ok": False, "stage": "create", "report": report}, ensure_ascii=False))
    raise SystemExit(1)

time.sleep(2)
routing = api("GET", shadow + f"/peers/{peer_id}/routing", shadow_token)
report["routing"] = routing

ok = (
    created["status"] == 200
    and routing["status"] == 200
    and (routing["json"] or {}).get("peer_id") == peer_id
    and (routing["json"] or {}).get("preferred_uplink") == uplink
    and (routing["json"] or {}).get("ingress_edge") == gateway
    and (routing["json"] or {}).get("effective_edge") == gateway
)

removed = api("POST", local + f"/peers/{peer_id}/remove", token, {})
report["remove"] = removed
ok = ok and bool((removed.get("json") or {}).get("ok"))
print(json.dumps({"ok": ok, "report": report}, ensure_ascii=False, sort_keys=True))
raise SystemExit(0 if ok else 1)
PY
