#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"

ssh "${EDG_HOST}" "sudo python3 -" <<'PY'
import json
import re
import socket
import urllib.parse
import urllib.request
import urllib.error
from pathlib import Path


def read_env_value(path, key):
    for line in Path(path).read_text(encoding="utf-8", errors="replace").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        if k.strip() == key:
            return v.strip().strip('"').strip("'")
    return ""


portal_token = read_env_value("/etc/wireguard/wg-portal.env", "WG_PORTAL_ADMIN_TOKEN")
shadow_base = read_env_value("/etc/wireguard/wg-portal.env", "JSTUN_READ_SHADOW_BASE")
shadow_token = read_env_value("/etc/wireguard/wg-portal.env", "JSTUN_READ_SHADOW_TOKEN")
portal_base = "http://10.200.0.4:18090"
opener = urllib.request.build_opener()


def fetch_text(url, headers=None):
    req = urllib.request.Request(url, headers=headers or {"User-Agent": "edg-admin-routing-shadow-smoke/1.0"})
    try:
        with opener.open(req, timeout=10) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except (urllib.error.URLError, TimeoutError, socket.timeout) as e:
        return 599, f"fetch_error:{type(e).__name__}:{e}"


def fetch_json(url, headers=None):
    status, body = fetch_text(url, headers=headers)
    try:
        data = json.loads(body)
    except Exception:
        data = {"ok": False, "raw": body}
    return status, data


def extract_code_value(label, body):
    pattern = re.compile(
        rf"<span class='k'>{re.escape(label)}</span><div class='v'><code>([^<]*)</code>",
        re.IGNORECASE,
    )
    m = pattern.search(body)
    return (m.group(1).strip() if m else "")


peers_status, peers_body = fetch_text(f"{portal_base}/admin/peers/?token={urllib.parse.quote(portal_token)}&read_mode=shadow")
peer_candidates = re.findall(r"/admin/peers/(p[A-Za-z0-9_-]+)/", peers_body)
peer_id = peer_candidates[0] if peer_candidates else ""
detail_status = 0
detail_body = ""
if peer_id:
    detail_status, detail_body = fetch_text(
        f"{portal_base}/admin/peers/{urllib.parse.quote(peer_id, safe='')}/?token={urllib.parse.quote(portal_token)}&read_mode=shadow"
    )

routing_status, routing_json = fetch_json(
    f"{shadow_base}/peers/{urllib.parse.quote(peer_id, safe='')}/routing",
    headers={"X-API-Token": shadow_token, "User-Agent": "edg-admin-routing-shadow-smoke/1.0"},
)

detail_values = {
    "policy_mode": extract_code_value("Policy mode", detail_body),
    "preferred_uplink": extract_code_value("Preferred uplink", detail_body),
    "effective_uplink": extract_code_value("Effective uplink", detail_body),
    "failover_uplink": extract_code_value("Failover uplink", detail_body),
    "routing_state": extract_code_value("Routing state", detail_body),
}

api_values = {
    "policy_mode": str(routing_json.get("policy_mode") or ""),
    "preferred_uplink": str(routing_json.get("preferred_uplink") or ""),
    "effective_uplink": str(routing_json.get("effective_uplink") or ""),
    "failover_uplink": str(routing_json.get("failover_uplink") or ""),
}

required_blocks = {
    "routing_block": "Routing policy vs effective" in detail_body,
    "routing_form": "set_policy_mode" in detail_body,
    "preferred_form": "set_preferred_uplink" in detail_body,
}

matches = {
    "policy_mode": detail_values["policy_mode"] == api_values["policy_mode"],
    "preferred_uplink": detail_values["preferred_uplink"] == api_values["preferred_uplink"],
    "effective_uplink": detail_values["effective_uplink"] == api_values["effective_uplink"],
    "failover_uplink": detail_values["failover_uplink"] == api_values["failover_uplink"],
}

summary = {
    "ok": bool(
        peers_status == 200
        and detail_status == 200
        and routing_status == 200
        and peer_id
        and all(required_blocks.values())
        and all(matches.values())
    ),
    "peer_id": peer_id,
    "peers_status": peers_status,
    "detail_status": detail_status,
    "routing_status": routing_status,
    "required_blocks": required_blocks,
    "matches": matches,
    "detail_values": detail_values,
    "api_values": api_values,
}

print(json.dumps(summary, ensure_ascii=False, sort_keys=True))
PY
