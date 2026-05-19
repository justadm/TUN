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
    with urllib.request.urlopen(req, timeout=20) as resp:
        return {"status": resp.status, "json": json.loads(resp.read().decode())}


env = load_env("/etc/wireguard/wg-portal.env")
token = env["WG_CONTROL_API_TOKEN"]
shadow_token = env["JSTUN_READ_SHADOW_TOKEN"]
local = "http://127.0.0.1:18110/v1"
shadow = env["JSTUN_READ_SHADOW_BASE"]
base = "http://10.200.0.4:18090"

peers = api("GET", local + "/peers", token)
items = [x for x in (peers.get("json") or {}).get("items", []) if str(x.get("status") or "") == "active" and int(x.get("last_handshake_unix") or 0) > 0]
items.sort(key=lambda x: int(x.get("last_handshake_unix") or 0), reverse=True)
item = items[0] if items else {}
peer_id = str(item.get("id") or "")
client_ip = str(item.get("allowed_ip") or "").split("/", 1)[0]
if not peer_id or not client_ip:
    print(json.dumps({"ok": False, "error": "no_active_peer_for_lk_canary"}, ensure_ascii=False))
    raise SystemExit(1)

routing_before = api("GET", shadow + f"/peers/{peer_id}/routing", shadow_token)
before_preferred = str((routing_before.get("json") or {}).get("preferred_uplink") or "ams").strip().lower() or "ams"
before_effective = str((routing_before.get("json") or {}).get("effective_uplink") or before_preferred).strip().lower() or before_preferred
target_uplink = {"ams": "fra", "fra": "nyc", "nyc": "ams"}.get(before_effective, "ams")

headers = {"X-Forwarded-For": client_ip}
lk_html = urllib.request.urlopen(urllib.request.Request(base + "/lk/", headers=headers), timeout=20).read().decode("utf-8", "replace")

form = urllib.parse.urlencode({"uplink": target_uplink}).encode()
post_req = urllib.request.Request(base + "/lk/uplink/", data=form, headers=headers, method="POST")
lk_post = urllib.request.urlopen(post_req, timeout=20).read().decode("utf-8", "replace")
time.sleep(2)
routing_changed = api("GET", shadow + f"/peers/{peer_id}/routing", shadow_token)

revert_form = urllib.parse.urlencode({"uplink": before_preferred}).encode()
revert_req = urllib.request.Request(base + "/lk/uplink/", data=revert_form, headers=headers, method="POST")
lk_revert = urllib.request.urlopen(revert_req, timeout=20).read().decode("utf-8", "replace")
time.sleep(2)
routing_reverted = api("GET", shadow + f"/peers/{peer_id}/routing", shadow_token)

ok = (
    "WG auto" in lk_html
    and "/lk/uplink/" in lk_html
    and "Аплинк обновлён" in lk_post
    and str((routing_changed.get("json") or {}).get("preferred_uplink") or "") == target_uplink
    and "Аплинк обновлён" in lk_revert
    and str((routing_reverted.get("json") or {}).get("preferred_uplink") or "") == before_preferred
)
print(json.dumps({
    "ok": ok,
    "peer_id": peer_id,
    "client_ip": client_ip,
    "before_preferred": before_preferred,
    "before_effective": before_effective,
    "target_uplink": target_uplink,
}, ensure_ascii=False))
raise SystemExit(0 if ok else 1)
PY
