#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"
LABEL="${LABEL:-routing-write-gate-$(date -u +%Y%m%d%H%M%S)}"

ssh "${EDG_HOST}" "sudo python3 -" <<'PY' "${LABEL}"
import json
import sys
import time
import urllib.request
import urllib.error
from pathlib import Path

label = sys.argv[1]


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
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return {"status": resp.status, "json": json.loads(resp.read().decode())}
    except urllib.error.HTTPError as e:
        body = e.read().decode()
        try:
            parsed = json.loads(body)
        except Exception:
            parsed = {"raw": body}
        return {"status": e.code, "json": parsed}


env = load_env("/etc/wireguard/wg-portal.env")
local_token = env["WG_CONTROL_API_TOKEN"]
shadow_token = env["JSTUN_READ_SHADOW_TOKEN"]
local = "http://127.0.0.1:18110/v1"
shadow = env["JSTUN_READ_SHADOW_BASE"]

report = {"label": label, "steps": []}
created = api("POST", local + "/peers/create", local_token, {"label": label})
report["create"] = created
peer_id = (created.get("json") or {}).get("id") or (created.get("json") or {}).get("peer_id")
report["peer_id"] = peer_id

if not ((created.get("json") or {}).get("ok") and peer_id):
    print(json.dumps({"ok": False, "stage": "create", "report": report}, ensure_ascii=False))
    raise SystemExit(1)


def fetch_shadow():
    last = None
    for _ in range(20):
        last = api("GET", shadow + f"/peers/{peer_id}/routing", shadow_token)
        if (last.get("json") or {}).get("ok"):
            return last
        time.sleep(1)
    return last


def check_step(name, write_res, routing_res, expected):
    routing = routing_res.get("json") or {}
    errors = []
    if int(write_res.get("status") or 0) != expected["write_status"]:
        errors.append("write_status")
    if not (write_res.get("json") or {}).get("ok"):
        errors.append("write_ok")
    for key, value in expected["write_fields"].items():
        if (write_res.get("json") or {}).get(key) != value:
            errors.append("write_" + key)
    if int(routing_res.get("status") or 0) != 200:
        errors.append("routing_status")
    for key, value in expected["routing_fields"].items():
        if routing.get(key) != value:
            errors.append("routing_" + key)
    return {
        "name": name,
        "write": write_res,
        "routing": routing_res,
        "expected": expected,
        "ok": not errors,
        "errors": errors,
    }


try:
    initial = fetch_shadow()
    report["steps"].append(
        check_step(
            "initial",
            {"status": 200, "json": {"ok": True}},
            initial,
            {
                "write_status": 200,
                "write_fields": {},
                "routing_fields": {
                    "policy_mode": "auto",
                    "preferred_uplink": "ams",
                    "effective_uplink": "ams",
                },
            },
        )
    )

    auto_pref = api(
        "POST",
        local + f"/peers/{peer_id}/preferred-uplink",
        local_token,
        {"preferred_uplink": "fra", "current_policy_mode": "auto"},
    )
    time.sleep(1)
    auto_pref_routing = fetch_shadow()
    report["steps"].append(
        check_step(
            "auto_preferred_fra",
            auto_pref,
            auto_pref_routing,
            {
                "write_status": 200,
                "write_fields": {
                    "policy_mode": "auto",
                    "preferred_uplink": "fra",
                    "effective_apply": "ams",
                    "intent_only": True,
                },
                "routing_fields": {
                    "policy_mode": "auto",
                    "preferred_uplink": "fra",
                    "effective_uplink": "ams",
                },
            },
        )
    )

    manual_fra = api(
        "POST",
        local + f"/peers/{peer_id}/routing",
        local_token,
        {"policy_mode": "manual", "preferred_uplink": "fra"},
    )
    time.sleep(1)
    manual_fra_routing = fetch_shadow()
    report["steps"].append(
        check_step(
            "manual_fra",
            manual_fra,
            manual_fra_routing,
            {
                "write_status": 200,
                "write_fields": {
                    "policy_mode": "manual",
                    "preferred_uplink": "fra",
                    "effective_apply": "fra",
                },
                "routing_fields": {
                    "policy_mode": "manual",
                    "preferred_uplink": "fra",
                    "effective_uplink": "fra",
                },
            },
        )
    )

    manual_nyc = api(
        "POST",
        local + f"/peers/{peer_id}/preferred-uplink",
        local_token,
        {"preferred_uplink": "nyc", "current_policy_mode": "manual"},
    )
    time.sleep(1)
    manual_nyc_routing = fetch_shadow()
    report["steps"].append(
        check_step(
            "manual_preferred_nyc",
            manual_nyc,
            manual_nyc_routing,
            {
                "write_status": 200,
                "write_fields": {
                    "policy_mode": "manual",
                    "preferred_uplink": "nyc",
                    "effective_apply": "nyc",
                },
                "routing_fields": {
                    "policy_mode": "manual",
                    "preferred_uplink": "nyc",
                    "effective_uplink": "nyc",
                },
            },
        )
    )
finally:
    report["remove"] = api("POST", local + f"/peers/{peer_id}/remove", local_token, {})

ok = all(bool(step.get("ok")) for step in report["steps"]) and bool((report.get("remove", {}).get("json") or {}).get("ok"))
print(json.dumps({"ok": ok, "report": report}, ensure_ascii=False, sort_keys=True))
raise SystemExit(0 if ok else 1)
PY
