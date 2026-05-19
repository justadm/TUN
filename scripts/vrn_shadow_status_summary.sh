#!/usr/bin/env bash
set -euo pipefail

VRN_HOST="${1:-vrn}"
SHADOW_TOKEN="${JSTUN_SHADOW_CONTROL_API_TOKEN:-shadow-read-smoke-token}"
ADMIN_TOKEN="${JSTUN_SHADOW_ADMIN_TOKEN:-shadow-admin-token}"

ssh "${VRN_HOST}" "SHADOW_TOKEN='${SHADOW_TOKEN}' ADMIN_TOKEN='${ADMIN_TOKEN}' python3 - <<'PY'
import json
import os
import urllib.request

token = os.environ['SHADOW_TOKEN']
admin_token = os.environ['ADMIN_TOKEN']

def get_json(url, headers=None):
    req = urllib.request.Request(url, headers=headers or {})
    with urllib.request.urlopen(req, timeout=5) as resp:
        return json.loads(resp.read().decode('utf-8', errors='replace') or '{}')

peers = get_json('http://127.0.0.1:18190/v1/peers', {'X-API-Token': token})
uplinks = get_json('http://127.0.0.1:18190/v1/uplinks', {'X-API-Token': token})
events = get_json('http://127.0.0.1:18190/v1/events?limit=5', {'X-API-Token': token})
live = get_json(f'http://127.0.0.1:18210/admin/live/data/?token={admin_token}')

summary = {
    'ok': True,
    'vrn': {
        'peers_source': peers.get('source'),
        'peers_count': len(peers.get('items', [])),
        'uplinks_source': uplinks.get('source'),
        'fra_ips': sorted(uplinks.get('fra_ips', [])),
        'nyc_ips': sorted(uplinks.get('nyc_ips', [])),
        'uplink_ids': [str(x.get('uplink_id') or '') for x in uplinks.get('items', [])],
        'events_source': events.get('source'),
        'events_sample_count': len(events.get('items', [])),
        'live_online_count': live.get('online_count'),
        'live_events_count': len(live.get('events', [])),
    },
}
print(json.dumps(summary, ensure_ascii=False, sort_keys=True))
PY"
