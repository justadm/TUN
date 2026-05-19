#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"
VRN_HOST="${2:-vrn}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

EDG_JSON="${TMP_DIR}/edg-admin.json"
VRN_JSON="${TMP_DIR}/vrn-admin.json"

echo "[1/3] fetch EDG admin surfaces"
ssh "${EDG_HOST}" "sudo python3 -" <<'PY' > "${EDG_JSON}"
import json
import re
import socket
import urllib.request
import urllib.parse
import urllib.error

def read_env_value(path, key):
    with open(path, 'r', encoding='utf-8', errors='replace') as fh:
        for line in fh:
            line = line.strip()
            if not line or line.startswith('#') or '=' not in line:
                continue
            k, v = line.split('=', 1)
            if k.strip() == key:
                return v.strip().strip('"').strip("'")
    return ''

token = read_env_value('/etc/wireguard/wg-portal.env', 'WG_PORTAL_ADMIN_TOKEN')
base = 'http://10.200.0.4:18090'
opener = urllib.request.build_opener()

def fetch_text(path):
    req = urllib.request.Request(base + path, headers={'User-Agent': 'portal-admin-compare/1.0'})
    try:
        with opener.open(req, timeout=10) as resp:
            return {'status': resp.status, 'body': resp.read().decode('utf-8', 'replace')}
    except urllib.error.HTTPError as e:
        return {'status': e.code, 'body': e.read().decode('utf-8', 'replace')}
    except (urllib.error.URLError, TimeoutError, socket.timeout) as e:
        return {'status': 599, 'body': f'fetch_error:{type(e).__name__}:{e}'}

dashboard = fetch_text(f'/admin/?token={token}')
peers = fetch_text(f'/admin/peers/?token={token}')
events = fetch_text(f'/admin/events/?token={token}')
live = fetch_text(f'/admin/live/?token={token}')
live_data = fetch_text(f'/admin/live/data/?token={token}')

peer_candidates = re.findall(r"/admin/peers/([^/]+)/", peers['body'])
peer_id = ''
detail = {'status': 0, 'body': ''}
for candidate in peer_candidates:
    probe = fetch_text(f"/admin/peers/{urllib.parse.quote(candidate, safe='')}/?token={token}")
    if int(probe.get('status') or 0) == 200:
        peer_id = candidate
        detail = probe
        break

out = {
    'host': 'edg',
    'peer_id': peer_id,
    'dashboard_status': dashboard['status'],
    'peers_status': peers['status'],
    'events_status': events['status'],
    'live_status': live['status'],
    'detail_status': detail['status'],
    'dashboard_has': {
        'title': 'Admin Dashboard' in dashboard['body'],
        'open_peers': 'Открыть peers' in dashboard['body'],
        'open_events': 'Открыть events' in dashboard['body'],
        'open_live': 'Открыть live' in dashboard['body'],
    },
    'peers_has': {
        'real_ip': 'IP (реальный)' in peers['body'],
        'uplink': 'Uplink' in peers['body'],
        'last_hs': 'Последний HS' in peers['body'],
    },
    'events_has': {
        'title': 'Админ: лента событий' in events['body'],
        'quick_api': 'API события' in events['body'],
        'peer_filter': 'Peer ID:' in events['body'],
    },
    'live_has': {
        'title': 'Admin: live-мониторинг' in live['body'],
        'online_label': 'Онлайн сейчас:' in live['body'],
        'events_table': 'Пока нет событий.' in live['body'] or '<th>Событие</th>' in live['body'],
    },
    'detail_has': {
        'real_ip': 'IP (реальный)' in detail['body'],
        'uplink': 'Uplink' in detail['body'],
        'last_handshake': 'Last handshake' in detail['body'],
        'related_events': 'Связанные события' in detail['body'],
    },
}

try:
    live_obj = json.loads(live_data['body'] or '{}')
except Exception:
    live_obj = {}
out['live_data'] = {
    'ok': bool(live_obj.get('ok')),
    'online_count': int(live_obj.get('online_count') or 0),
    'events_count': len(live_obj.get('events') or []),
}
print(json.dumps(out, ensure_ascii=False, sort_keys=True))
PY

echo "[2/3] fetch VRN admin surfaces"
ssh "${VRN_HOST}" "python3 -" <<'PY' > "${VRN_JSON}"
import json
import re
import socket
import urllib.request
import urllib.parse
import urllib.error

def read_env_value(path, key):
    with open(path, 'r', encoding='utf-8', errors='replace') as fh:
        for line in fh:
            line = line.strip()
            if not line or line.startswith('#') or '=' not in line:
                continue
            k, v = line.split('=', 1)
            if k.strip() == key:
                return v.strip().strip('"').strip("'")
    return ''

token = read_env_value('/etc/jstun-shadow/jstun-shadow.env', 'WG_PORTAL_ADMIN_TOKEN')
base = 'http://127.0.0.1:18210'
opener = urllib.request.build_opener()

def fetch_text(path):
    req = urllib.request.Request(base + path, headers={'User-Agent': 'portal-admin-compare/1.0'})
    try:
        with opener.open(req, timeout=10) as resp:
            return {'status': resp.status, 'body': resp.read().decode('utf-8', 'replace')}
    except urllib.error.HTTPError as e:
        return {'status': e.code, 'body': e.read().decode('utf-8', 'replace')}
    except (urllib.error.URLError, TimeoutError, socket.timeout) as e:
        return {'status': 599, 'body': f'fetch_error:{type(e).__name__}:{e}'}

dashboard = fetch_text(f'/admin/?token={token}')
peers = fetch_text(f'/admin/peers/?token={token}')
events = fetch_text(f'/admin/events/?token={token}')
live = fetch_text(f'/admin/live/?token={token}')
live_data = fetch_text(f'/admin/live/data/?token={token}')

peer_candidates = re.findall(r"/admin/peers/([^/]+)/", peers['body'])
peer_id = ''
detail = {'status': 0, 'body': ''}
for candidate in peer_candidates:
    probe = fetch_text(f"/admin/peers/{urllib.parse.quote(candidate, safe='')}/?token={token}")
    if int(probe.get('status') or 0) == 200:
        peer_id = candidate
        detail = probe
        break

out = {
    'host': 'vrn',
    'peer_id': peer_id,
    'dashboard_status': dashboard['status'],
    'peers_status': peers['status'],
    'events_status': events['status'],
    'live_status': live['status'],
    'detail_status': detail['status'],
    'dashboard_has': {
        'title': 'Admin Dashboard' in dashboard['body'],
        'open_peers': 'Открыть peers' in dashboard['body'],
        'open_events': 'Открыть events' in dashboard['body'],
        'open_live': 'Открыть live' in dashboard['body'],
    },
    'peers_has': {
        'real_ip': 'IP (реальный)' in peers['body'],
        'uplink': 'Uplink' in peers['body'],
        'last_hs': 'Последний HS' in peers['body'],
    },
    'events_has': {
        'title': 'Админ: лента событий' in events['body'],
        'quick_api': 'API события' in events['body'],
        'peer_filter': 'Peer ID:' in events['body'],
    },
    'live_has': {
        'title': 'Admin: live-мониторинг' in live['body'],
        'online_label': 'Онлайн сейчас:' in live['body'],
        'events_table': 'Пока нет событий.' in live['body'] or '<th>Событие</th>' in live['body'],
    },
    'detail_has': {
        'real_ip': 'IP (реальный)' in detail['body'],
        'uplink': 'Uplink' in detail['body'],
        'last_handshake': 'Last handshake' in detail['body'],
        'related_events': 'Связанные события' in detail['body'],
    },
}

try:
    live_obj = json.loads(live_data['body'] or '{}')
except Exception:
    live_obj = {}
out['live_data'] = {
    'ok': bool(live_obj.get('ok')),
    'online_count': int(live_obj.get('online_count') or 0),
    'events_count': len(live_obj.get('events') or []),
}
print(json.dumps(out, ensure_ascii=False, sort_keys=True))
PY

echo "[3/3] compare admin surfaces"
python3 - <<'PY' "${EDG_JSON}" "${VRN_JSON}"
import json
import pathlib
import sys

edg = json.loads(pathlib.Path(sys.argv[1]).read_text())
vrn = json.loads(pathlib.Path(sys.argv[2]).read_text())

def status_ok(data):
    return all(int(data.get(key) or 0) == 200 for key in ['dashboard_status', 'peers_status', 'events_status', 'live_status', 'detail_status'])

summary = {
    'edg_status_ok': status_ok(edg),
    'vrn_status_ok': status_ok(vrn),
    'edg_peer_id': edg.get('peer_id'),
    'vrn_peer_id': vrn.get('peer_id'),
    'dashboard_has_match': edg.get('dashboard_has') == vrn.get('dashboard_has'),
    'peers_has_match': edg.get('peers_has') == vrn.get('peers_has'),
    'events_has_match': edg.get('events_has') == vrn.get('events_has'),
    'live_has_match': edg.get('live_has') == vrn.get('live_has'),
    'detail_has_match': edg.get('detail_has') == vrn.get('detail_has'),
    'edg_live_data': edg.get('live_data'),
    'vrn_live_data': vrn.get('live_data'),
}

print(json.dumps({'ok': True, 'summary': summary, 'edg': edg, 'vrn': vrn}, ensure_ascii=False, sort_keys=True))
PY
