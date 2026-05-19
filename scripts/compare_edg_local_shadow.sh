#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

LOCAL_JSON="${TMP_DIR}/edg-local.json"
SHADOW_JSON="${TMP_DIR}/edg-shadow.json"

echo "[1/3] fetch EDG local admin surfaces"
ssh "${EDG_HOST}" "sudo python3 -" <<'PY' > "${LOCAL_JSON}"
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
    req = urllib.request.Request(base + path, headers={'User-Agent': 'edg-local-shadow-compare/1.0'})
    try:
        with opener.open(req, timeout=10) as resp:
            return {'status': resp.status, 'body': resp.read().decode('utf-8', 'replace')}
    except urllib.error.HTTPError as e:
        return {'status': e.code, 'body': e.read().decode('utf-8', 'replace')}
    except (urllib.error.URLError, TimeoutError, socket.timeout) as e:
        return {'status': 599, 'body': f'fetch_error:{type(e).__name__}:{e}'}

dashboard = fetch_text(f'/admin/?token={token}&read_mode=local')
peers = fetch_text(f'/admin/peers/?token={token}&read_mode=local')
events = fetch_text(f'/admin/events/?token={token}&read_mode=local')
live = fetch_text(f'/admin/live/?token={token}&read_mode=local')
live_data = fetch_text(f'/admin/live/data/?token={token}&read_mode=local')

peer_candidates = re.findall(r"/admin/peers/([^/]+)/", peers['body'])
peer_id = ''
detail = {'status': 0, 'body': ''}
for candidate in peer_candidates:
    probe = fetch_text(f"/admin/peers/{urllib.parse.quote(candidate, safe='')}/?token={token}&read_mode=local")
    if int(probe.get('status') or 0) == 200:
        peer_id = candidate
        detail = probe
        break

try:
    live_obj = json.loads(live_data['body'] or '{}')
except Exception:
    live_obj = {}

out = {
    'host': 'edg',
    'mode': 'local',
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
        'mode_local': '<code>local</code>' in dashboard['body'],
    },
    'peers_has': {
        'real_ip': 'IP (реальный)' in peers['body'],
        'uplink': 'Uplink' in peers['body'],
        'last_hs': 'Последний HS' in peers['body'],
        'mode_local': '<code>local</code>' in peers['body'],
    },
    'events_has': {
        'title': 'Админ: лента событий' in events['body'],
        'quick_api': 'API события' in events['body'],
        'peer_filter': 'Peer ID:' in events['body'],
        'mode_local': '<code>local</code>' in events['body'],
    },
    'live_has': {
        'title': 'Admin: live-мониторинг' in live['body'],
        'online_label': 'Онлайн сейчас:' in live['body'],
        'events_table': 'Пока нет событий.' in live['body'] or '<th>Событие</th>' in live['body'],
        'mode_local': '<code>local</code>' in live['body'],
    },
    'detail_has': {
        'real_ip': 'IP (реальный)' in detail['body'],
        'uplink': 'Uplink' in detail['body'],
        'last_handshake': 'Last handshake' in detail['body'],
        'related_events': 'Связанные события' in detail['body'],
        'mode_local': '<code>local</code>' in detail['body'],
    },
    'live_data': {
        'ok': bool(live_obj.get('ok')),
        'online_count': int(live_obj.get('online_count') or 0),
        'events_count': len(live_obj.get('events') or []),
        'first_event': (live_obj.get('events') or [{}])[0].get('event'),
    },
}
print(json.dumps(out, ensure_ascii=False, sort_keys=True))
PY

echo "[2/3] fetch EDG shadow admin surfaces"
ssh "${EDG_HOST}" "sudo python3 -" <<'PY' > "${SHADOW_JSON}"
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
    req = urllib.request.Request(base + path, headers={'User-Agent': 'edg-local-shadow-compare/1.0'})
    try:
        with opener.open(req, timeout=15) as resp:
            return {'status': resp.status, 'body': resp.read().decode('utf-8', 'replace')}
    except urllib.error.HTTPError as e:
        return {'status': e.code, 'body': e.read().decode('utf-8', 'replace')}
    except (urllib.error.URLError, TimeoutError, socket.timeout) as e:
        return {'status': 599, 'body': f'fetch_error:{type(e).__name__}:{e}'}

dashboard = fetch_text(f'/admin/?token={token}&read_mode=shadow')
peers = fetch_text(f'/admin/peers/?token={token}&read_mode=shadow')
events = fetch_text(f'/admin/events/?token={token}&read_mode=shadow')
live = fetch_text(f'/admin/live/?token={token}&read_mode=shadow')
live_data = fetch_text(f'/admin/live/data/?token={token}&read_mode=shadow')

peer_candidates = re.findall(r"/admin/peers/([^/]+)/", peers['body'])
peer_id = ''
detail = {'status': 0, 'body': ''}
for candidate in peer_candidates:
    probe = fetch_text(f"/admin/peers/{urllib.parse.quote(candidate, safe='')}/?token={token}&read_mode=shadow")
    if int(probe.get('status') or 0) == 200:
        peer_id = candidate
        detail = probe
        break

try:
    live_obj = json.loads(live_data['body'] or '{}')
except Exception:
    live_obj = {}

out = {
    'host': 'edg',
    'mode': 'shadow',
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
        'mode_shadow': '<code>shadow</code>' in dashboard['body'],
    },
    'peers_has': {
        'real_ip': 'IP (реальный)' in peers['body'],
        'uplink': 'Uplink' in peers['body'],
        'last_hs': 'Последний HS' in peers['body'],
        'mode_shadow': '<code>shadow</code>' in peers['body'],
    },
    'events_has': {
        'title': 'Админ: лента событий' in events['body'],
        'quick_api': 'API события' in events['body'],
        'peer_filter': 'Peer ID:' in events['body'],
        'mode_shadow': '<code>shadow</code>' in events['body'],
    },
    'live_has': {
        'title': 'Admin: live-мониторинг' in live['body'],
        'online_label': 'Онлайн сейчас:' in live['body'],
        'events_table': 'Пока нет событий.' in live['body'] or '<th>Событие</th>' in live['body'],
        'mode_shadow': '<code>shadow</code>' in live['body'],
    },
    'detail_has': {
        'real_ip': 'IP (реальный)' in detail['body'],
        'uplink': 'Uplink' in detail['body'],
        'last_handshake': 'Last handshake' in detail['body'],
        'related_events': 'Связанные события' in detail['body'],
        'mode_shadow': '<code>shadow</code>' in detail['body'],
    },
    'live_data': {
        'ok': bool(live_obj.get('ok')),
        'online_count': int(live_obj.get('online_count') or 0),
        'events_count': len(live_obj.get('events') or []),
        'first_event': (live_obj.get('events') or [{}])[0].get('event'),
    },
}
print(json.dumps(out, ensure_ascii=False, sort_keys=True))
PY

echo "[3/3] compare EDG local vs shadow"
python3 - <<'PY' "${LOCAL_JSON}" "${SHADOW_JSON}"
import json
import pathlib
import sys

local = json.loads(pathlib.Path(sys.argv[1]).read_text())
shadow = json.loads(pathlib.Path(sys.argv[2]).read_text())

def status_ok(data):
    return all(int(data.get(key) or 0) == 200 for key in ['dashboard_status', 'peers_status', 'events_status', 'live_status', 'detail_status'])

summary = {
    'local_status_ok': status_ok(local),
    'shadow_status_ok': status_ok(shadow),
    'local_peer_id': local.get('peer_id'),
    'shadow_peer_id': shadow.get('peer_id'),
    'dashboard_shape_match': {
        'title': local.get('dashboard_has', {}).get('title') == shadow.get('dashboard_has', {}).get('title'),
        'open_peers': local.get('dashboard_has', {}).get('open_peers') == shadow.get('dashboard_has', {}).get('open_peers'),
        'open_events': local.get('dashboard_has', {}).get('open_events') == shadow.get('dashboard_has', {}).get('open_events'),
        'open_live': local.get('dashboard_has', {}).get('open_live') == shadow.get('dashboard_has', {}).get('open_live'),
    },
    'peers_shape_match': {
        'real_ip': local.get('peers_has', {}).get('real_ip') == shadow.get('peers_has', {}).get('real_ip'),
        'uplink': local.get('peers_has', {}).get('uplink') == shadow.get('peers_has', {}).get('uplink'),
        'last_hs': local.get('peers_has', {}).get('last_hs') == shadow.get('peers_has', {}).get('last_hs'),
    },
    'events_shape_match': {
        'title': local.get('events_has', {}).get('title') == shadow.get('events_has', {}).get('title'),
        'quick_api': local.get('events_has', {}).get('quick_api') == shadow.get('events_has', {}).get('quick_api'),
        'peer_filter': local.get('events_has', {}).get('peer_filter') == shadow.get('events_has', {}).get('peer_filter'),
    },
    'live_shape_match': {
        'title': local.get('live_has', {}).get('title') == shadow.get('live_has', {}).get('title'),
        'online_label': local.get('live_has', {}).get('online_label') == shadow.get('live_has', {}).get('online_label'),
        'events_table': local.get('live_has', {}).get('events_table') == shadow.get('live_has', {}).get('events_table'),
    },
    'detail_shape_match': {
        'real_ip': local.get('detail_has', {}).get('real_ip') == shadow.get('detail_has', {}).get('real_ip'),
        'uplink': local.get('detail_has', {}).get('uplink') == shadow.get('detail_has', {}).get('uplink'),
        'last_handshake': local.get('detail_has', {}).get('last_handshake') == shadow.get('detail_has', {}).get('last_handshake'),
        'related_events': local.get('detail_has', {}).get('related_events') == shadow.get('detail_has', {}).get('related_events'),
    },
    'local_live_data': local.get('live_data'),
    'shadow_live_data': shadow.get('live_data'),
}
summary['dashboard_match'] = all(summary['dashboard_shape_match'].values())
summary['peers_match'] = all(summary['peers_shape_match'].values())
summary['events_match'] = all(summary['events_shape_match'].values())
summary['live_match'] = all(summary['live_shape_match'].values())
summary['detail_match'] = all(summary['detail_shape_match'].values())

print(json.dumps({'ok': True, 'summary': summary, 'local': local, 'shadow': shadow}, ensure_ascii=False, sort_keys=True))
PY
