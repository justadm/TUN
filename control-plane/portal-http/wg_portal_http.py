#!/usr/bin/env python3
import datetime as dt
import io
import html
import ipaddress
import json
import csv
import os
import contextvars
import fcntl
import subprocess
import time
from pathlib import Path
import base64
import zipfile
import socket
import sys
from zoneinfo import ZoneInfo
from http.cookies import SimpleCookie
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse, urlencode, quote
from urllib.request import Request, urlopen
from urllib.error import URLError, HTTPError

HOST = os.getenv("WG_PORTAL_HTTP_HOST", "10.200.0.4")
PORT = int(os.getenv("WG_PORTAL_HTTP_PORT", "18090"))
CLI = os.getenv("WG_PORTAL_CLI", "/usr/local/bin/wg_portal.py")
TITLE = "JsTun"
ACTIVE_WINDOW_SEC = int(os.getenv("WG_PORTAL_ACTIVE_WINDOW_SEC", "600"))
PAYMENT_URL = os.getenv("WG_PORTAL_PAYMENT_URL", "").strip()
ADMIN_TOKEN = os.getenv("WG_PORTAL_ADMIN_TOKEN", "").strip()
STATE_DIR = Path(os.getenv("WG_PORTAL_STATE", "/var/lib/wg-portal"))
STATIC_ROOT = Path(os.getenv("WG_PORTAL_STATIC_ROOT", "/usr/local/share/wg-portal"))
USE_CONTROL_API = (os.getenv("WG_PORTAL_USE_CONTROL_API", "0").strip() == "1")
CONTROL_API_BASE = os.getenv("WG_CONTROL_API_BASE", "http://127.0.0.1:18110/v1").strip().rstrip("/")
CONTROL_API_TOKEN = os.getenv("WG_CONTROL_API_TOKEN", "").strip()
CONTROL_API_TIMEOUT_SEC = int(os.getenv("WG_CONTROL_API_TIMEOUT_SEC", "5"))
CONTROL_API_FALLBACK_CLI = (os.getenv("WG_CONTROL_API_FALLBACK_CLI", "1").strip() == "1")
READ_SHADOW_ENABLED = (os.getenv("JSTUN_READ_SHADOW_ENABLED", "0").strip() == "1")
READ_SHADOW_BASE = os.getenv("JSTUN_READ_SHADOW_BASE", "").strip().rstrip("/")
READ_SHADOW_TOKEN = os.getenv("JSTUN_READ_SHADOW_TOKEN", "").strip()
READ_SHADOW_TIMEOUT_SEC = int(os.getenv("JSTUN_READ_SHADOW_TIMEOUT_SEC", "5"))
READ_SHADOW_RESOURCES = {
    x.strip().lower()
    for x in (os.getenv("JSTUN_READ_SHADOW_RESOURCES", "") or "").split(",")
    if x.strip()
}
ADMIN_READ_CANARY_ENABLED = (os.getenv("JSTUN_ADMIN_READ_CANARY_ENABLED", "0").strip() == "1")
ADMIN_READ_MODE_DEFAULT = str(os.getenv("JSTUN_ADMIN_READ_MODE_DEFAULT", "local") or "local").strip().lower()
ADMIN_DATA_SCOPE_DEFAULT = "all" if ADMIN_READ_MODE_DEFAULT == "shadow" else "edg"
WG_BILLING_CLI = os.getenv("WG_BILLING_CLI", "/opt/wg-billing/wg_billing_service_skeleton.py").strip()
WG_BILLING_PYTHON = os.getenv("WG_BILLING_PYTHON", "python3").strip()
WG_BILLING_AMOUNT_RUB = float(os.getenv("WG_BILLING_AMOUNT_RUB", "199"))
WG_BILLING_TTL_SEC = int(os.getenv("WG_BILLING_TTL_SEC", "900"))
WG_BILLING_POLL_LIMIT = int(os.getenv("WG_BILLING_POLL_LIMIT", "50"))
WG_BILLING_LIST_LIMIT = int(os.getenv("WG_BILLING_LIST_LIMIT", "300"))
WG_BILLING_REDIRECT_URL = os.getenv("WG_BILLING_REDIRECT_URL", "").strip()
WG_BILLING_PURPOSE_TEMPLATE = os.getenv("WG_BILLING_PURPOSE_TEMPLATE", "WG access for {label} ({peer_id})")
WG_BILLING_FALLBACK_URL = os.getenv(
    "WG_BILLING_FALLBACK_URL",
    "https://www.tinkoff.ru/rm/r_QbvegWGXPM.bSevcVNkfN/wM7Ce36559",
).strip()
WG_PORTAL_DISPLAY_TZ = os.getenv("WG_PORTAL_DISPLAY_TZ", "Europe/Moscow").strip() or "Europe/Moscow"
WG_PORTAL_UPLINK_ENABLED = (os.getenv("WG_PORTAL_UPLINK_ENABLED", "1").strip() != "0")
try:
    DISPLAY_TZ = ZoneInfo(WG_PORTAL_DISPLAY_TZ)
except Exception:
    DISPLAY_TZ = dt.timezone.utc
    WG_PORTAL_DISPLAY_TZ = "UTC"

# Anti-abuse: ограничение выдачи новых профилей на /new/.
# По умолчанию: не более 1 создания профиля в час с одного IP.
NEW_IP_WINDOW_SEC = int(os.getenv("WG_PORTAL_NEW_IP_WINDOW_SEC", "3600"))
NEW_IP_MAX = int(os.getenv("WG_PORTAL_NEW_IP_MAX", "1"))
NEW_IP_DB = STATE_DIR / "issued_by_ip.json"
NEW_IP_LOCK = STATE_DIR / ".http_new_ip.lock"
LABEL_OVERRIDES_DB = STATE_DIR / "label_overrides.json"
LABEL_OVERRIDES_LOCK = STATE_DIR / ".http_label_overrides.lock"
TYPE_OVERRIDES_DB = STATE_DIR / "type_overrides.json"
TYPE_OVERRIDES_LOCK = STATE_DIR / ".http_type_overrides.lock"
ADMIN_META_DB = STATE_DIR / "admin_meta.json"
ADMIN_META_LOCK = STATE_DIR / ".http_admin_meta.lock"
MIGRATION_STATE_DB = STATE_DIR / "migration_state.json"
MIGRATION_STATE_LOCK = STATE_DIR / ".http_migration_state.lock"
ISSUED_RESULTS_DIR = STATE_DIR / "issued_results"
NEW_IP_EXEMPT_IPS = {
    x.strip()
    for x in (os.getenv("WG_PORTAL_NEW_IP_EXEMPT_IPS", "") or "").split(",")
    if x.strip()
}

# Audit log (JSONL). Важно: не логируем секреты (cfg, tokens, lk_code).
AUDIT_LOG = STATE_DIR / "audit.jsonl"
AUDIT_LOCK = STATE_DIR / ".http_audit.lock"
AUDIT_MAX_LINES = int(os.getenv("WG_PORTAL_ADMIN_EVENTS_MAX_LINES", "10000"))
ADMIN_LIVE_WINDOW_SEC = int(os.getenv("WG_PORTAL_ADMIN_LIVE_WINDOW_SEC", "600"))
ADMIN_LIVE_POLL_SEC = int(os.getenv("WG_PORTAL_ADMIN_LIVE_POLL_SEC", "10"))
TG_ALERTS_ENABLED = (os.getenv("WG_PORTAL_TG_ALERTS_ENABLED", "1").strip() != "0")
TG_BOT_TOKEN = os.getenv("WG_PORTAL_TG_BOT_TOKEN", "").strip()
TG_CHAT_ID = os.getenv("WG_PORTAL_TG_CHAT_ID", "").strip()
TG_TIMEOUT_SEC = float(os.getenv("WG_PORTAL_TG_TIMEOUT_SEC", "4"))
IP_GEO_ENABLED = (os.getenv("WG_PORTAL_IP_GEO_ENABLED", "1").strip() != "0")
IP_GEO_TIMEOUT_SEC = float(os.getenv("WG_PORTAL_IP_GEO_TIMEOUT_SEC", "2"))
IP_GEO_CACHE_TTL_SEC = int(os.getenv("WG_PORTAL_IP_GEO_CACHE_TTL_SEC", "21600"))
IP_GEO_CACHE_FILE = Path(os.getenv("WG_PORTAL_IP_GEO_CACHE_FILE", str(STATE_DIR / "geo_cache.json")))
ADMIN_BAD_COOKIE_ALERT_COOLDOWN_SEC = int(os.getenv("WG_PORTAL_ADMIN_BAD_COOKIE_ALERT_COOLDOWN_SEC", "300"))
ADMIN_MISSING_TOKEN_ALERT_COOLDOWN_SEC = int(os.getenv("WG_PORTAL_ADMIN_MISSING_TOKEN_ALERT_COOLDOWN_SEC", "60"))
ADMIN_QUERY_OK_ALERT_COOLDOWN_SEC = int(os.getenv("WG_PORTAL_ADMIN_QUERY_OK_ALERT_COOLDOWN_SEC", "1800"))

LIVE_PREV = {"ts": 0, "peers": {}}
GEO_CACHE = {}
GEO_CACHE_LOADED = False
ALERT_LAST_SENT = {}
GEO_PROVIDER_ALLOW_CACHE = {}
ADMIN_READ_MODE = contextvars.ContextVar("jstun_admin_read_mode", default=(ADMIN_READ_MODE_DEFAULT if ADMIN_READ_MODE_DEFAULT in ("local", "shadow") else "local"))
ADMIN_DATA_SCOPE = contextvars.ContextVar("jstun_admin_data_scope", default=ADMIN_DATA_SCOPE_DEFAULT)
PORTAL_ENV_PATH = Path(os.getenv("WG_PORTAL_ENV", "/etc/wireguard/wg-portal.env"))
ACCOUNT_LK_DIR = Path(__file__).resolve().parents[1] / "account-lk"
ACCOUNT_LK_DB = Path(os.getenv("WG_ACCOUNT_LK_DB", str(STATE_DIR / "account_lk.sqlite3")))
ACCOUNT_LK_SESSION_COOKIE = "account_lk_session"
if str(ACCOUNT_LK_DIR) not in sys.path:
    sys.path.append(str(ACCOUNT_LK_DIR))
try:
    from service import AccountLKService  # type: ignore
    from sqlite_store import SqliteAccountLKStore  # type: ignore
except Exception:
    AccountLKService = None
    SqliteAccountLKStore = None

ACCOUNT_LK_STORE = None
ACCOUNT_LK_SERVICE = None

INSTALL_LINKS = {
    "windows": "https://download.wireguard.com/windows-client/wireguard-installer.exe",
    "macos": "https://itunes.apple.com/us/app/wireguard/id1451685025?ls=1&mt=12",
    "android": "https://play.google.com/store/apps/details?id=com.wireguard.android",
    "ios": "https://itunes.apple.com/us/app/wireguard/id1441195209?ls=1&mt=8",
    "more": "https://www.wireguard.com/install/",
}

CLIENT_TYPE_LABELS = {
    "admin": "Админ",
    "employee": "Сотрудник",
    "vip": "ВИП",
    "normal": "Обычный",
}

SUPPORTED_EDGES = ("edg", "vrn", "msk_d")
SUPPORTED_SCOPES = ("edg", "vrn", "msk_d", "all")


def _is_supported_edge(edge_id):
    return str(edge_id or "").strip().lower() in SUPPORTED_EDGES


def _edge_options_html(selected_edge="edg", public_labels=False):
    selected = str(selected_edge or "edg").strip().lower()
    rows = []
    for edge in SUPPORTED_EDGES:
        label = _public_gateway_label(edge) if public_labels else edge.upper()
        rows.append(f"<option value='{edge}' {'selected' if selected == edge else ''}>{esc(label)}</option>")
    return "".join(rows)


def _account_lk_service():
    global ACCOUNT_LK_STORE, ACCOUNT_LK_SERVICE
    if ACCOUNT_LK_SERVICE is not None:
        return ACCOUNT_LK_SERVICE
    if AccountLKService is None or SqliteAccountLKStore is None:
        return None
    ACCOUNT_LK_DB.parent.mkdir(parents=True, exist_ok=True)
    ACCOUNT_LK_STORE = SqliteAccountLKStore(ACCOUNT_LK_DB)
    ACCOUNT_LK_SERVICE = AccountLKService(ACCOUNT_LK_STORE)
    return ACCOUNT_LK_SERVICE


def run_cli(args):
    p = subprocess.run([CLI] + args, capture_output=True, text=True)
    if p.returncode != 0:
        try:
            return json.loads(p.stdout.strip() or "{}")
        except Exception:
            return {"ok": False, "error": (p.stderr or p.stdout or "cli failed").strip()}
    try:
        return json.loads(p.stdout.strip() or "{}")
    except Exception:
        return {"ok": False, "error": "invalid json from cli"}


def _is_public_ip(ip_raw):
    try:
        ip_obj = ipaddress.ip_address(str(ip_raw or "").strip())
    except Exception:
        return False
    if ip_obj.is_private or ip_obj.is_loopback or ip_obj.is_link_local:
        return False
    if ip_obj.is_multicast or ip_obj.is_reserved or ip_obj.is_unspecified:
        return False
    return True


def _allow_geo_provider_host(host):
    host = str(host or "").strip()
    if not host:
        return
    now = int(dt.datetime.now(dt.timezone.utc).timestamp())
    prev = int(GEO_PROVIDER_ALLOW_CACHE.get(host) or 0)
    if now - prev < 900:
        return
    GEO_PROVIDER_ALLOW_CACHE[host] = now
    try:
        infos = socket.getaddrinfo(host, 443, type=socket.SOCK_STREAM)
    except Exception:
        return
    v4 = set()
    v6 = set()
    for info in infos:
        fam = info[0]
        sockaddr = info[4] if len(info) > 4 else ()
        ip = sockaddr[0] if sockaddr else ""
        if not ip:
            continue
        if fam == socket.AF_INET:
            v4.add(ip)
        elif fam == socket.AF_INET6:
            v6.add(ip)
    for ip in sorted(v4):
        subprocess.run(
            ["nft", "add", "element", "inet", "msk_geo", "msk_custom4", "{", ip, "}"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )
    for ip in sorted(v6):
        subprocess.run(
            ["nft", "add", "element", "inet", "msk_geo", "msk_custom6", "{", ip, "}"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )


def _load_geo_cache_once():
    global GEO_CACHE_LOADED
    if GEO_CACHE_LOADED:
        return
    GEO_CACHE_LOADED = True
    try:
        if not IP_GEO_CACHE_FILE.exists():
            return
        raw = json.loads(IP_GEO_CACHE_FILE.read_text(encoding="utf-8") or "{}")
        if isinstance(raw, dict):
            now = int(dt.datetime.now(dt.timezone.utc).timestamp())
            for ip, item in raw.items():
                if not isinstance(item, dict):
                    continue
                if int(item.get("expires_at") or 0) <= now:
                    continue
                GEO_CACHE[str(ip).strip()] = {
                    "text": str(item.get("text") or ""),
                    "expires_at": int(item.get("expires_at") or 0),
                }
    except Exception:
        return


def _save_geo_cache():
    try:
        IP_GEO_CACHE_FILE.parent.mkdir(parents=True, exist_ok=True)
        payload = {}
        now = int(dt.datetime.now(dt.timezone.utc).timestamp())
        for ip, item in GEO_CACHE.items():
            if not isinstance(item, dict):
                continue
            if int(item.get("expires_at") or 0) <= now:
                continue
            payload[str(ip).strip()] = {
                "text": str(item.get("text") or ""),
                "expires_at": int(item.get("expires_at") or 0),
            }
        tmp = IP_GEO_CACHE_FILE.with_suffix(IP_GEO_CACHE_FILE.suffix + ".tmp")
        tmp.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
        tmp.replace(IP_GEO_CACHE_FILE)
    except Exception:
        return


def _geo_for_ip(ip_raw):
    ip = str(ip_raw or "").strip()
    if not ip:
        return ""
    if not IP_GEO_ENABLED:
        return ""
    if not _is_public_ip(ip):
        return "локальный/приватный IP"
    _load_geo_cache_once()
    now = int(dt.datetime.now(dt.timezone.utc).timestamp())
    cached = GEO_CACHE.get(ip)
    if isinstance(cached, dict) and int(cached.get("expires_at") or 0) > now:
        return str(cached.get("text") or "")
    providers = [
        ("ipwho.is", f"https://ipwho.is/{quote(ip)}"),
        ("ipapi.co", f"https://ipapi.co/{quote(ip)}/json/"),
        ("ipinfo.io", f"https://ipinfo.io/{quote(ip)}/json"),
    ]
    text = ""
    for host, url in providers:
        _allow_geo_provider_host(host)
        req = Request(url=url, headers={"User-Agent": "wg-portal-http/1.0"})
        try:
            with urlopen(req, timeout=IP_GEO_TIMEOUT_SEC) as resp:
                raw = resp.read().decode("utf-8", errors="replace")
                data = json.loads(raw or "{}")
        except Exception:
            continue
        country = ""
        region = ""
        city = ""
        isp = ""
        if host == "ipwho.is":
            if not data.get("success"):
                continue
            country = str(data.get("country_code") or data.get("country") or "").strip()
            region = str(data.get("region") or "").strip()
            city = str(data.get("city") or "").strip()
            conn = data.get("connection") if isinstance(data.get("connection"), dict) else {}
            isp = str((conn or {}).get("isp") or "").strip()
        elif host == "ipapi.co":
            if str(data.get("error") or "").strip():
                continue
            country = str(data.get("country_code") or data.get("country_name") or "").strip()
            region = str(data.get("region") or "").strip()
            city = str(data.get("city") or "").strip()
            isp = str(data.get("org") or "").strip()
        else:
            if str(data.get("bogon") or "").strip():
                continue
            country = str(data.get("country") or "").strip()
            region = str(data.get("region") or "").strip()
            city = str(data.get("city") or "").strip()
            isp = str(data.get("org") or "").strip()
        parts = [x for x in (country, region, city) if x]
        base = ", ".join(parts)
        text = (base + (f" ({isp})" if isp else "")).strip()
        if text:
            break
    GEO_CACHE[ip] = {"text": text, "expires_at": now + IP_GEO_CACHE_TTL_SEC}
    _save_geo_cache()
    return text


def _latest_peer_real_ip_map(max_lines=AUDIT_MAX_LINES):
    latest = {}
    for e in reversed(read_audit_all(max_lines=max_lines)):
        peer_id_a = str(e.get("peer_id") or "").strip()
        ip_a = str(e.get("ip") or "").strip()
        if not peer_id_a or not ip_a or peer_id_a in latest:
            continue
        if ip_a in ("0.0.0.0", "127.0.0.1", "::1"):
            continue
        latest[peer_id_a] = ip_a
    return latest


def _latest_peer_label_map(max_lines=AUDIT_MAX_LINES):
    latest = {}
    for e in reversed(read_audit_all(max_lines=max_lines)):
        if str(e.get("event") or "") != "admin_action_ok":
            continue
        if str(e.get("action") or "") != "set_label":
            continue
        pid = str(e.get("peer_id") or "").strip()
        lbl = str(e.get("label") or "").strip()
        if not pid or not lbl or pid in latest:
            continue
        latest[pid] = lbl
    return latest


def _endpoint_host(endpoint_raw):
    s = str(endpoint_raw or "").strip()
    if not s or s == "(none)":
        return ""
    if s.startswith("["):
        right = s.find("]")
        if right > 1:
            return s[1:right]
        return ""
    if ":" in s:
        return s.rsplit(":", 1)[0]
    return s


def _wg_endpoint_maps():
    by_pub = {}
    by_allowed_ip = {}
    commands = [
        ["/usr/bin/wg", "show", "all", "dump"],
        ["wg", "show", "all", "dump"],
        ["/usr/bin/sudo", "-n", "/usr/bin/wg", "show", "all", "dump"],
    ]
    dump_out = ""
    for cmd in commands:
        try:
            p = subprocess.run(cmd, capture_output=True, text=True, timeout=4)
        except Exception:
            continue
        out = str(p.stdout or "")
        if out.strip():
            dump_out = out
            break
    if not dump_out.strip():
        return by_pub, by_allowed_ip
    for raw in dump_out.splitlines():
        line = raw.strip()
        if not line:
            continue
        cols = line.split("\t")
        # peer row format (wg show dump):
        # public_key, preshared_key, endpoint, allowed_ips, latest_hs, rx, tx, keepalive
        if len(cols) < 8:
            continue
        endpoint_host = _endpoint_host(cols[2])
        if not endpoint_host:
            continue
        pub = str(cols[0] or "").strip()
        allowed = str(cols[3] or "").strip()
        if pub:
            by_pub[pub] = endpoint_host
        for part in allowed.split(","):
            ip_short = str(part or "").strip().split("/", 1)[0].strip()
            if ip_short:
                by_allowed_ip[ip_short] = endpoint_host
    return by_pub, by_allowed_ip


def _ip_is_public(ip_raw):
    try:
        ip_obj = ipaddress.ip_address(str(ip_raw or "").strip())
    except Exception:
        return False
    return not (ip_obj.is_private or ip_obj.is_loopback or ip_obj.is_link_local or ip_obj.is_reserved or ip_obj.is_multicast)


def _lock_label_overrides():
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    fd = os.open(str(LABEL_OVERRIDES_LOCK), os.O_CREAT | os.O_RDWR, 0o600)
    fcntl.flock(fd, fcntl.LOCK_EX)
    return fd


def _unlock_label_overrides(fd):
    fcntl.flock(fd, fcntl.LOCK_UN)
    os.close(fd)


def _read_label_overrides():
    try:
        txt = LABEL_OVERRIDES_DB.read_text(encoding="utf-8")
        data = json.loads(txt or "{}")
        return data if isinstance(data, dict) else {}
    except Exception:
        return {}


def _write_label_overrides(data):
    try:
        LABEL_OVERRIDES_DB.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
        return True
    except Exception:
        return False


def _get_label_override(peer_id):
    pid = str(peer_id or "").strip()
    if not pid:
        return ""
    data = _read_label_overrides()
    val = str(data.get(pid) or "").strip()
    return val


def _set_label_override(peer_id, label):
    pid = str(peer_id or "").strip()
    lbl = str(label or "").strip()
    if not pid:
        return False
    fd = _lock_label_overrides()
    try:
        data = _read_label_overrides()
        if lbl:
            data[pid] = lbl
        else:
            data.pop(pid, None)
        return _write_label_overrides(data)
    finally:
        _unlock_label_overrides(fd)


def _lock_admin_meta():
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    fd = os.open(str(ADMIN_META_LOCK), os.O_CREAT | os.O_RDWR, 0o600)
    fcntl.flock(fd, fcntl.LOCK_EX)
    return fd


def _unlock_admin_meta(fd):
    fcntl.flock(fd, fcntl.LOCK_UN)
    os.close(fd)


def _read_admin_meta():
    try:
        txt = ADMIN_META_DB.read_text(encoding="utf-8")
        data = json.loads(txt or "{}")
        return data if isinstance(data, dict) else {}
    except Exception:
        return {}


def _write_admin_meta(data):
    try:
        ADMIN_META_DB.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
        return True
    except Exception:
        return False


def _get_admin_meta(peer_id, current_label=""):
    pid = str(peer_id or "").strip()
    if not pid:
        return {"admin_label": "", "admin_comment": ""}
    data = _read_admin_meta()
    raw = data.get(pid)
    if not isinstance(raw, dict):
        raw = {}
    admin_label = str(raw.get("admin_label") or "").strip() or str(current_label or "").strip()
    admin_comment = str(raw.get("admin_comment") or "").strip()
    return {"admin_label": admin_label, "admin_comment": admin_comment}


def _set_admin_meta(peer_id, admin_label="", admin_comment=""):
    pid = str(peer_id or "").strip()
    if not pid:
        return False
    fd = _lock_admin_meta()
    try:
        data = _read_admin_meta()
        row = data.get(pid)
        if not isinstance(row, dict):
            row = {}
        row["admin_label"] = str(admin_label or "").strip()
        row["admin_comment"] = str(admin_comment or "").strip()
        data[pid] = row
        return _write_admin_meta(data)
    finally:
        _unlock_admin_meta(fd)


def _apply_admin_meta_to_item(item, latest_label_map=None):
    if not isinstance(item, dict):
        return item
    pid = str(item.get("id") or "").strip()
    if not pid:
        return item
    label_from_audit = ""
    if isinstance(latest_label_map, dict):
        label_from_audit = str(latest_label_map.get(pid) or "").strip()
    label_override = label_from_audit or _get_label_override(pid)
    if label_override:
        item["label"] = label_override
    meta = _get_admin_meta(pid, current_label=str(item.get("label") or ""))
    item["admin_label"] = meta.get("admin_label") or str(item.get("label") or "")
    item["admin_comment"] = meta.get("admin_comment") or ""
    return item


def _lock_type_overrides():
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    fd = os.open(str(TYPE_OVERRIDES_LOCK), os.O_CREAT | os.O_RDWR, 0o600)
    fcntl.flock(fd, fcntl.LOCK_EX)
    return fd


def _unlock_type_overrides(fd):
    fcntl.flock(fd, fcntl.LOCK_UN)
    os.close(fd)


def _read_type_overrides():
    try:
        txt = TYPE_OVERRIDES_DB.read_text(encoding="utf-8")
        data = json.loads(txt or "{}")
        return data if isinstance(data, dict) else {}
    except Exception:
        return {}


def _write_type_overrides(data):
    try:
        TYPE_OVERRIDES_DB.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
        return True
    except Exception:
        return False


def _ensure_issued_results_dir():
    try:
        ISSUED_RESULTS_DIR.mkdir(parents=True, exist_ok=True)
    except Exception:
        pass


def _issued_result_path(peer_id):
    pid = safe_id(peer_id)
    return ISSUED_RESULTS_DIR / f"{pid}.json"


def _save_issued_result(peer_id, data):
    pid = safe_id(peer_id)
    if not pid or not isinstance(data, dict):
        return False
    _ensure_issued_results_dir()
    try:
        payload = dict(data)
        payload["id"] = pid
        _issued_result_path(pid).write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
        return True
    except Exception:
        return False


def _load_issued_result(peer_id):
    pid = safe_id(peer_id)
    if not pid:
        return {}
    try:
        raw = _issued_result_path(pid).read_text(encoding="utf-8")
        data = json.loads(raw)
        return data if isinstance(data, dict) else {}
    except Exception:
        return {}


def _normalize_client_type(v):
    key = str(v or "").strip().lower()
    return key if key in CLIENT_TYPE_LABELS else "normal"


def _uplink_label_for_edge(edge_id, uplink_name):
    uplink = str(uplink_name or "").strip().lower() or "ams"
    labels = {
        "ams": "AMS",
        "fra": "FRA",
        "nyc": "NYC",
    }
    return labels.get(uplink, uplink.upper() if uplink else "-")


def _public_gateway_label(edge_id):
    edge = str(edge_id or "").strip().lower()
    labels = {
        "edg": "Основной",
        "vrn": "Резервный",
        "msk_d": "Дополнительный",
    }
    return labels.get(edge, "Основной")


def _public_uplink_label(uplink_name):
    uplink = str(uplink_name or "").strip().lower() or "ams"
    labels = {
        "ams": "По умолчанию",
        "fra": "Европа",
        "nyc": "США",
    }
    return labels.get(uplink, "По умолчанию")


def _account_provider_title(provider):
    key = str(provider or "").strip().lower()
    labels = {
        "telegram": "Telegram",
        "vk": "VK",
        "yandex": "Yandex",
        "google": "Google",
    }
    return labels.get(key, "Способ входа")


def _account_profile_status_label(status):
    key = str(status or "").strip().lower()
    labels = {
        "pending": "ожидает активации",
        "active": "активен",
        "blocked": "заблокирован",
        "expired": "истёк",
        "removed": "удалён",
    }
    return labels.get(key, key or "—")


def _get_client_type(peer_id):
    pid = str(peer_id or "").strip()
    if not pid:
        return "normal"
    data = _read_type_overrides()
    return _normalize_client_type(data.get(pid))


def _set_client_type(peer_id, client_type):
    pid = str(peer_id or "").strip()
    if not pid:
        return False
    val = _normalize_client_type(client_type)
    fd = _lock_type_overrides()
    try:
        data = _read_type_overrides()
        data[pid] = val
        return _write_type_overrides(data)
    finally:
        _unlock_type_overrides(fd)


def _lock_migration_state():
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    fd = os.open(str(MIGRATION_STATE_LOCK), os.O_CREAT | os.O_RDWR, 0o600)
    fcntl.flock(fd, fcntl.LOCK_EX)
    return fd


def _unlock_migration_state(fd):
    fcntl.flock(fd, fcntl.LOCK_UN)
    os.close(fd)


def _read_migration_state():
    try:
        txt = MIGRATION_STATE_DB.read_text(encoding="utf-8")
        data = json.loads(txt or "{}")
        return data if isinstance(data, dict) else {}
    except Exception:
        return {}


def _write_migration_state(data):
    try:
        MIGRATION_STATE_DB.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
        return True
    except Exception:
        return False


def _normalize_migration_state(v):
    row = dict(v or {})
    checklist = row.get("checklist") if isinstance(row.get("checklist"), dict) else {}
    return {
        "wave": str(row.get("wave") or "").strip(),
        "note": str(row.get("note") or "").strip(),
        "checklist": {
            "issued": bool(checklist.get("issued")),
            "client_imported": bool(checklist.get("client_imported")),
            "validated": bool(checklist.get("validated")),
            "old_removed": bool(checklist.get("old_removed")),
            "rollback_issued": bool(checklist.get("rollback_issued")),
            "rollback_validated": bool(checklist.get("rollback_validated")),
            "rollback_finalized": bool(checklist.get("rollback_finalized")),
        },
    }


def _get_migration_state(peer_id):
    pid = str(peer_id or "").strip()
    if not pid:
        return _normalize_migration_state({})
    data = _read_migration_state()
    return _normalize_migration_state(data.get(pid) or {})


def _set_migration_state(peer_id, wave=None, note=None, checklist=None):
    pid = str(peer_id or "").strip()
    if not pid:
        return False
    fd = _lock_migration_state()
    try:
        data = _read_migration_state()
        row = _normalize_migration_state(data.get(pid) or {})
        if wave is not None:
            row["wave"] = str(wave or "").strip()[:64]
        if note is not None:
            row["note"] = str(note or "").strip()[:2000]
        if isinstance(checklist, dict):
            merged = dict(row.get("checklist") or {})
            for key in ("issued", "client_imported", "validated", "old_removed", "rollback_issued", "rollback_validated", "rollback_finalized"):
                if key in checklist:
                    merged[key] = bool(checklist.get(key))
            row["checklist"] = merged
        data[pid] = row
        return _write_migration_state(data)
    finally:
        _unlock_migration_state(fd)


def _parse_peer_ids_text(raw):
    text = str(raw or "")
    parts = []
    for chunk in text.replace(",", "\n").replace(";", "\n").splitlines():
        pid = str(chunk or "").strip()
        if pid:
            parts.append(pid)
    seen = set()
    out = []
    for pid in parts:
        if pid in seen:
            continue
        seen.add(pid)
        out.append(pid)
    return out


def _tg_send_text(text):
    if not TG_ALERTS_ENABLED:
        return False
    if not TG_BOT_TOKEN or not TG_CHAT_ID:
        return False
    body = urlencode(
        {
            "chat_id": TG_CHAT_ID,
            "text": str(text or "")[:4000],
            "disable_web_page_preview": "true",
        }
    ).encode("utf-8")
    req = Request(
        url=f"https://api.telegram.org/bot{TG_BOT_TOKEN}/sendMessage",
        data=body,
        method="POST",
        headers={"Content-Type": "application/x-www-form-urlencoded"},
    )
    try:
        with urlopen(req, timeout=TG_TIMEOUT_SEC):
            return True
    except Exception:
        return False


def _api_call_to(base, token, timeout_sec, method, path, payload=None):
    base = str(base or "").strip().rstrip("/")
    token = str(token or "").strip()
    if not base:
        return {"ok": False, "error": "control_api_base_empty"}
    if not token:
        return {"ok": False, "error": "control_api_token_empty"}
    url = f"{base}{path}"
    body = None
    headers = {"X-API-Token": token}
    if payload is not None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        headers["Content-Type"] = "application/json; charset=utf-8"
    req = Request(url=url, data=body, method=method, headers=headers)
    try:
        with urlopen(req, timeout=int(timeout_sec or 5)) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            return json.loads(raw or "{}")
    except HTTPError as e:
        try:
            raw = e.read().decode("utf-8", errors="replace")
            data = json.loads(raw or "{}")
            if "ok" not in data:
                data["ok"] = False
            return data
        except Exception:
            return {"ok": False, "error": f"http_error:{e.code}"}
    except URLError as e:
        return {"ok": False, "error": f"url_error:{e}"}
    except Exception as e:
        return {"ok": False, "error": f"api_exception:{e}"}


def _api_call(method, path, payload=None):
    if not USE_CONTROL_API:
        return {"ok": False, "error": "control_api_disabled"}
    return _api_call_to(CONTROL_API_BASE, CONTROL_API_TOKEN, CONTROL_API_TIMEOUT_SEC, method, path, payload=payload)


def _shadow_read_config():
    enabled = bool(READ_SHADOW_ENABLED)
    base = str(READ_SHADOW_BASE or "").strip().rstrip("/")
    token = str(READ_SHADOW_TOKEN or "").strip()
    timeout_sec = int(READ_SHADOW_TIMEOUT_SEC or 5)
    resources = set(READ_SHADOW_RESOURCES or set())
    if enabled and base and token:
        return {
            "enabled": enabled,
            "base": base,
            "token": token,
            "timeout_sec": timeout_sec,
            "resources": resources,
        }
    try:
        for raw in PORTAL_ENV_PATH.read_text(encoding="utf-8").splitlines():
            line = str(raw or "").strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            if k == "JSTUN_READ_SHADOW_ENABLED":
                enabled = (str(v or "").strip() == "1")
            elif k == "JSTUN_READ_SHADOW_BASE":
                base = str(v or "").strip().rstrip("/")
            elif k == "JSTUN_READ_SHADOW_TOKEN":
                token = str(v or "").strip()
            elif k == "JSTUN_READ_SHADOW_TIMEOUT_SEC":
                try:
                    timeout_sec = int(v or 5)
                except Exception:
                    timeout_sec = 5
            elif k == "JSTUN_READ_SHADOW_RESOURCES":
                resources = {x.strip().lower() for x in str(v or "").split(",") if x.strip()}
    except Exception:
        pass
    return {
        "enabled": enabled,
        "base": base,
        "token": token,
        "timeout_sec": timeout_sec,
        "resources": resources,
    }


def _use_shadow_read(resource):
    resource = str(resource or "").strip().lower()
    cfg = _shadow_read_config()
    if not resource or not cfg.get("enabled"):
        return False
    if not cfg.get("base") or not cfg.get("token"):
        return False
    if str(ADMIN_READ_MODE.get() or "local").strip().lower() != "shadow":
        return False
    return resource in set(cfg.get("resources") or set())


def _read_api_call(resource, method, path):
    if _use_shadow_read(resource):
        cfg = _shadow_read_config()
        out = _api_call_to(cfg.get("base"), cfg.get("token"), cfg.get("timeout_sec"), method, path)
        if out.get("ok"):
            return out
        if USE_CONTROL_API:
            local = _api_call(method, path)
            if local.get("ok"):
                local.setdefault("source", "local")
                local["read_fallback_from"] = "shadow"
                return local
        return out
    return _api_call(method, path)


def backend_list():
    if USE_CONTROL_API:
        out = _read_api_call("peers", "GET", "/peers")
        if out.get("ok"):
            return out
        if not CONTROL_API_FALLBACK_CLI:
            return out
    return run_cli(["list"])


def backend_list_for_lk():
    cfg = _shadow_read_config()
    if cfg.get("enabled") and cfg.get("base") and cfg.get("token"):
        out = _api_call_to(cfg.get("base"), cfg.get("token"), cfg.get("timeout_sec"), "GET", "/peers")
        if out.get("ok"):
            out.setdefault("source", "shadow")
            return out
    return backend_list()


def backend_create(label, ttl_sec=None, expire_sec=None, gateway=None, uplink=None):
    if USE_CONTROL_API:
        payload = {"label": label}
        if ttl_sec is not None:
            payload["ttl_sec"] = int(ttl_sec)
        if expire_sec is not None:
            payload["expire_sec"] = int(expire_sec)
        if gateway:
            payload["gateway"] = str(gateway or "").strip().lower()
        if uplink:
            payload["uplink"] = str(uplink or "").strip().lower()
        out = _api_call("POST", "/peers/create", payload=payload)
        if out.get("ok"):
            return out
        if not CONTROL_API_FALLBACK_CLI:
            return out
    args = ["create", "--label", label]
    if ttl_sec is not None:
        args += ["--ttl-sec", str(int(ttl_sec))]
    if expire_sec is not None:
        args += ["--expire-sec", str(int(expire_sec))]
    return run_cli(args)


def backend_block(peer_id):
    pid = str(peer_id or "").strip()
    if USE_CONTROL_API:
        out = _api_call("POST", f"/peers/{pid}/block", payload={})
        if out.get("ok"):
            return out
        if not CONTROL_API_FALLBACK_CLI:
            return out
    return run_cli(["block", "--id", pid])


def backend_remove(peer_id):
    pid = str(peer_id or "").strip()
    if USE_CONTROL_API:
        out = _api_call("POST", f"/peers/{pid}/remove", payload={})
        if out.get("ok"):
            return out
        if not CONTROL_API_FALLBACK_CLI:
            return out
    return run_cli(["remove", "--id", pid])


def backend_reissue(peer_id, remove_old=False, gateway=None, uplink=None):
    pid = str(peer_id or "").strip()
    if USE_CONTROL_API:
        payload = {"remove_old": bool(remove_old)}
        if gateway:
            payload["gateway"] = str(gateway or "").strip().lower()
        if uplink:
            payload["uplink"] = str(uplink or "").strip().lower()
        out = _api_call("POST", f"/peers/{pid}/reissue", payload=payload)
        if out.get("ok"):
            return out
        if not CONTROL_API_FALLBACK_CLI:
            return out
    # Fallback: emulation through CLI with list+create (+optional remove)
    lst = backend_list()
    if not lst.get("ok"):
        return lst
    item = None
    for x in lst.get("items", []):
        if str(x.get("id") or "").strip() == pid:
            item = x
            break
    if not item:
        return {"ok": False, "error": "peer not found"}
    old_label = str(item.get("label") or "web")
    new_label = (old_label + "-reissue")[:64]
    abs_ttl = int(item.get("absolute_ttl_sec") or 0)
    if abs_ttl > 0:
        created = backend_create(new_label, ttl_sec=abs_ttl, expire_sec=abs_ttl, gateway=gateway, uplink=uplink)
    else:
        created = backend_create(new_label, gateway=gateway, uplink=uplink)
    if not created.get("ok"):
        return created
    removed = None
    if remove_old:
        removed = backend_remove(pid)
    return {"ok": True, "old_id": pid, "new_peer": created, "old_removed": removed}


def _qr_block_html(qr_png_b64, qr_is_placeholder=False):
    qrb64 = str(qr_png_b64 or "").strip()
    placeholder = bool(qr_is_placeholder)
    if not qrb64:
        placeholder = True
    note = ""
    if placeholder:
        note = "<p class='small warn'><b>QR временно недоступен.</b> Используйте кнопку скачивания или копирования конфига ниже.</p>"
    if not qrb64:
        return f"<div class='card'><h3>QR</h3>{note}</div>"
    return f"""<div class='card'>
<h3>QR</h3>
{note}
<img alt='WG QR' style='max-width:360px;width:100%' src='data:image/png;base64,{esc(qrb64)}' />
</div>"""


def _render_new_result_page(data):
    peer_id = str(data.get("id", "") or "")
    label = str(data.get("label", "") or "profile")
    gateway_req = str(data.get("gateway") or "edg").strip().lower() or "edg"
    uplink_req = str(data.get("uplink") or "ams").strip().lower() or "ams"
    cfg = str(data.get("config", "") or "")
    qrb64 = data.get("qr_png_b64", "")
    qr_is_placeholder = bool(data.get("qr_is_placeholder"))
    lk_token = str(data.get("lk_token", "") or "")
    cfg_js = json.dumps(cfg, ensure_ascii=False)
    cfg_b64 = base64.b64encode(cfg.encode("utf-8")).decode("ascii")
    folder = safe_slug(label)
    expires_ui = fmt_dt_ui(data.get("absolute_expires_at") or data.get("expires_at") or "")
    conf_name = f"{folder}.conf"
    qr_block = _qr_block_html(qrb64, qr_is_placeholder)
    body = f"""
<div class=card>
<h2>Профиль подключения создан</h2>
<div style='display:grid;grid-template-columns:repeat(auto-fit,minmax(320px,1fr));gap:16px;align-items:start'>
  {qr_block}
  <div class='card'>
    <h3>Данные подключения</h3>
    <p><b>ID:</b> <code>{esc(peer_id)}</code></p>
    <p><b>IP:</b> <code>{esc(data.get('allowed_ip'))}</code></p>
    <p><b>Точка подключения:</b> <code>{esc(_public_gateway_label(gateway_req))}</code></p>
    <p><b>Выход в интернет:</b> <code>{esc(_public_uplink_label(uplink_req))}</code></p>
    <p><b>Истекает:</b> <code>{esc(expires_ui)}</code></p>
    <p class='warn'><b>Внимание!</b> Если в течение 1 часа профиль не будет активирован, он удалится автоматически.</p>
  </div>
</div>
<div class=card>
<h3>Конфиг</h3>
  <div class='actions' style='display:flex;gap:10px;flex-wrap:nowrap;align-items:center;overflow-x:auto'>
    <button class='btn' type='button' id='copyCfgBtn'>Скопировать</button>
    <button class='btn' type='button' id='copyLinkBtn'>Скопировать ссылку</button>
    <form method='post' action='/dlzip/' style='margin:0;display:inline-flex'>
      <input type='hidden' name='id' value='{esc(peer_id)}'>
      <input type='hidden' name='label' value='{esc(folder)}'>
      <input type='hidden' name='cfg_b64' value='{esc(cfg_b64)}'>
      <button class='btn' type='submit'>Скачать</button>
    </form>
    <span id='copyState' class='small'></span>
  </div>
  <p class='small'>Скачивание отдаст ZIP <code>{esc(folder)}.zip</code> с файлом <code>{esc(conf_name)}</code> внутри.</p>
  <p class='small'>На ПК обычно проще скачать и импортировать файл <code>.conf</code> в WireGuard.</p>
  <pre id='cfgHolder' style='display:none'>{esc(cfg)}</pre>
  <script>
  (() => {{
    const cfg = {cfg_js};
    const btn = document.getElementById('copyCfgBtn');
    const linkBtn = document.getElementById('copyLinkBtn');
    const state = document.getElementById('copyState');
    btn.addEventListener('click', async () => {{
      try {{
        await navigator.clipboard.writeText(cfg);
        state.textContent = 'Конфиг скопирован';
      }} catch (e) {{
        state.textContent = 'Не удалось скопировать автоматически';
        document.getElementById('cfgHolder').style.display = 'block';
      }}
    }});
    if (linkBtn) {{
      linkBtn.addEventListener('click', async () => {{
        try {{
          await navigator.clipboard.writeText(window.location.href);
          state.textContent = 'Ссылка скопирована';
        }} catch (e) {{
          state.textContent = 'Не удалось скопировать ссылку';
        }}
      }});
    }}
  }})();
  </script>
</div>
<p class='actions'><a class='btn' href='/account/'>Открыть аккаунт</a> <span class='small'>После активации профиля он появится в новом личном кабинете.</span></p>
{back_home_link()}
"""
    headers = [
        ("Set-Cookie", f"wg_peer_id={peer_id}; Path=/; Max-Age=31536000; SameSite=Lax; Secure; HttpOnly"),
        ("Set-Cookie", f"wg_lk_token={lk_token}; Path=/; Max-Age=31536000; SameSite=Lax; Secure; HttpOnly"),
        ("Set-Cookie", "wg_lk_ui=1; Path=/; Max-Age=31536000; SameSite=Lax; Secure"),
    ]
    return page("WG Created", body), headers


def backend_get_uplink(peer_id):
    pid = str(peer_id or "").strip()
    if not WG_PORTAL_UPLINK_ENABLED:
        return {"ok": True, "uplink": ""}
    if USE_CONTROL_API:
        out = _read_api_call("peer_uplink", "GET", f"/peers/{pid}/uplink")
        if out.get("ok"):
            return out
        if not CONTROL_API_FALLBACK_CLI:
            return out
    return {"ok": False, "error": "uplink_api_unavailable"}


def backend_get_peer_routing(peer_id):
    pid = str(peer_id or "").strip()
    if not WG_PORTAL_UPLINK_ENABLED:
        return {"ok": True, "peer_id": pid, "ingress_edge": "", "policy_mode": "", "preferred_uplink": "", "active_uplink": "", "effective_uplink": "", "failover_uplink": ""}
    if USE_CONTROL_API:
        out = _read_api_call("peer_routing", "GET", f"/peers/{pid}/routing")
        if out.get("ok"):
            return out
        if not CONTROL_API_FALLBACK_CLI:
            return out
    uplink_out = backend_get_uplink(pid)
    if not uplink_out.get("ok"):
        return uplink_out
    uplink = str(uplink_out.get("uplink") or "").strip().lower()
    failover = "fra" if uplink == "ams" else "ams"
    if uplink == "nyc":
        failover = "ams"
    return {
        "ok": True,
        "source": "legacy",
        "peer_id": pid,
        "allowed_ip": uplink_out.get("allowed_ip") or "",
        "ingress_edge": "edg",
        "effective_edge": "edg",
        "policy_mode": uplink if uplink in ("ams", "fra", "nyc") else "auto",
        "preferred_uplink": uplink or "ams",
        "active_uplink": uplink or "ams",
        "effective_uplink": uplink or "ams",
        "failover_uplink": failover,
        "health_status": "",
        "health_note": "",
    }


def backend_get_peer_routing_for_lk(peer_id):
    pid = str(peer_id or "").strip()
    cfg = _shadow_read_config()
    if pid and cfg.get("enabled") and cfg.get("base") and cfg.get("token"):
        out = _api_call_to(cfg.get("base"), cfg.get("token"), cfg.get("timeout_sec"), "GET", f"/peers/{pid}/routing")
        if out.get("ok"):
            out.setdefault("source", "shadow")
            return out
    return backend_get_peer_routing(pid)


def backend_set_uplink(peer_id, uplink):
    pid = str(peer_id or "").strip()
    route = str(uplink or "").strip().lower()
    if route not in ("ams", "nyc", "fra"):
        return {"ok": False, "error": "invalid_uplink"}
    if USE_CONTROL_API:
        out = _api_call("POST", f"/peers/{pid}/uplink", payload={"uplink": route})
        if out.get("ok"):
            return out
        if not CONTROL_API_FALLBACK_CLI:
            return out
    return {"ok": False, "error": "uplink_api_unavailable"}


def backend_set_routing_policy(peer_id, policy_mode, preferred_uplink=None):
    pid = str(peer_id or "").strip()
    mode = str(policy_mode or "").strip().lower()
    preferred = str(preferred_uplink or "").strip().lower()
    if mode not in ("auto", "manual", "ams", "fra", "nyc"):
        return {"ok": False, "error": "invalid_policy_mode"}
    payload = {"policy_mode": mode}
    if preferred:
        payload["preferred_uplink"] = preferred
    if USE_CONTROL_API:
        out = _api_call("POST", f"/peers/{pid}/routing", payload=payload)
        if out.get("ok"):
            return out
        if not CONTROL_API_FALLBACK_CLI:
            return out
    if mode == "auto":
        return backend_set_uplink(pid, "ams")
    if mode == "manual":
        return backend_set_uplink(pid, preferred or "ams")
    return backend_set_uplink(pid, mode)


def backend_set_preferred_uplink(peer_id, preferred_uplink, current_policy_mode):
    pid = str(peer_id or "").strip()
    preferred = str(preferred_uplink or "").strip().lower()
    mode = str(current_policy_mode or "").strip().lower()
    if preferred not in ("ams", "fra", "nyc"):
        return {"ok": False, "error": "invalid_preferred_uplink"}
    if mode not in ("auto", "manual", "ams", "fra", "nyc"):
        return {"ok": False, "error": "invalid_policy_mode"}
    if USE_CONTROL_API:
        out = _api_call(
            "POST",
            f"/peers/{pid}/preferred-uplink",
            payload={"preferred_uplink": preferred, "current_policy_mode": mode},
        )
        if out.get("ok"):
            return out
        if not CONTROL_API_FALLBACK_CLI:
            return out
    if mode == "auto":
        return {"ok": True, "peer_id": pid, "preferred_uplink": preferred, "policy_mode": "auto", "effective_apply": "ams", "intent_only": True}
    return backend_set_uplink(pid, preferred)


def backend_set_ingress_edge(peer_id, ingress_edge):
    pid = str(peer_id or "").strip()
    edge = str(ingress_edge or "").strip().lower()
    if not _is_supported_edge(edge):
        return {"ok": False, "error": "invalid_ingress_edge"}
    if USE_CONTROL_API:
        out = _api_call(
            "POST",
            f"/peers/{pid}/routing",
            payload={"ingress_edge": edge},
        )
        if out.get("ok"):
            return out
        if not CONTROL_API_FALLBACK_CLI:
            return out
    return {"ok": True, "peer_id": pid, "ingress_edge": edge, "effective_edge": edge, "intent_only": True}


def backend_list_uplinks():
    if not WG_PORTAL_UPLINK_ENABLED:
        return {"ok": True, "nyc_ips": [], "fra_ips": []}
    if USE_CONTROL_API:
        out = _read_api_call("uplinks", "GET", "/uplinks")
        if out.get("ok"):
            if list(out.get("items", []) or []):
                return out
            cfg = _shadow_read_config()
            if cfg.get("enabled") and cfg.get("base") and cfg.get("token"):
                shadow = _api_call_to(cfg.get("base"), cfg.get("token"), cfg.get("timeout_sec"), "GET", "/uplinks")
                if shadow.get("ok") and list(shadow.get("items", []) or []):
                    shadow.setdefault("source", "shadow")
                    shadow["read_fallback_from"] = out.get("source", "local")
                    return shadow
            return out
        if not CONTROL_API_FALLBACK_CLI:
            return out
    return {"ok": False, "error": "uplink_api_unavailable"}


def backend_list_edges():
    def _with_expected_edges(out):
        if not isinstance(out, dict) or not out.get("ok"):
            return out
        items = list(out.get("items", []) or [])
        seen = {str(x.get("edge_id") or "").strip().lower() for x in items if isinstance(x, dict)}
        for edge in SUPPORTED_EDGES:
            if edge in seen:
                continue
            items.append(
                {
                    "edge_id": edge,
                    "name": edge.upper(),
                    "public_ip": "-",
                    "client_iface": "-",
                    "active_clients": 0,
                    "ru_egress": edge,
                    "notru_primary": f"{edge}/ams",
                    "notru_fallback": f"{edge}/fra",
                    "health": "-",
                    "uplinks": [],
                    "_synthetic": True,
                }
            )
        out = dict(out)
        out["items"] = items
        return out

    if USE_CONTROL_API:
        out = _read_api_call("edges", "GET", "/edges")
        if out.get("ok"):
            return _with_expected_edges(out)
        # Edges are currently sourced from the shadow DB model. Until EDG gets
        # a separate local edge inventory source, allow a direct shadow fallback
        # even when admin read mode is "local".
        cfg = _shadow_read_config()
        if cfg.get("enabled") and cfg.get("base") and cfg.get("token"):
            shadow = _api_call_to(cfg.get("base"), cfg.get("token"), cfg.get("timeout_sec"), "GET", "/edges")
            if shadow.get("ok"):
                shadow.setdefault("source", "shadow")
                shadow["read_fallback_from"] = "local"
                return _with_expected_edges(shadow)
        if not CONTROL_API_FALLBACK_CLI:
            return out
    return {"ok": False, "error": "edges_api_unavailable"}


def backend_get_edge(edge_id):
    eid = str(edge_id or "").strip().lower()
    if not eid:
        return {"ok": False, "error": "edge_id_required"}
    out = backend_list_edges()
    if not out.get("ok"):
        return out
    for item in list(out.get("items", []) or []):
        if str(item.get("edge_id") or "").strip().lower() == eid:
            return {"ok": True, "item": item, "source": out.get("source", "db")}
    return {"ok": False, "error": "edge_not_found"}


def _normalize_event_item(item, fallback_id=0):
    row = dict(item or {})
    meta = row.get("metadata")
    if not isinstance(meta, dict):
        meta = {}
    peer_id = str(row.get("peer_id") or "").strip()
    if not peer_id:
        peer_id = str(meta.get("legacy_peer_id_raw") or "").strip()
    try:
        event_id = int(row.get("_event_id") or row.get("event_id") or fallback_id or 0)
    except Exception:
        event_id = 0
    return {
        "_event_id": event_id,
        "ts": row.get("ts") or row.get("occurred_at") or "",
        "event": row.get("event") or row.get("event_type") or "",
        "peer_id": peer_id,
        "label": row.get("label") or meta.get("label") or "",
        "ip": row.get("ip") or meta.get("ip") or "",
        "action": row.get("action") or meta.get("action") or "",
        "reason": row.get("reason") or meta.get("reason") or "",
        "error": row.get("error") or meta.get("error") or "",
        "ok": row.get("ok") if "ok" in row else meta.get("ok"),
        "metadata": meta,
    }


def backend_list_events(limit=100, event_types=None):
    try:
        lim = max(1, min(int(limit), 500))
    except Exception:
        lim = 100
    safe_event_types = [str(x or "").strip() for x in (event_types or []) if str(x or "").strip()]
    wanted = set(safe_event_types)
    if USE_CONTROL_API:
        qs = [f"limit={lim}"]
        if safe_event_types:
            qs.append("event_type=" + quote(",".join(safe_event_types), safe=","))
        out = _read_api_call("events", "GET", "/events?" + "&".join(qs))
        if out.get("ok"):
            items = out.get("items")
            if not isinstance(items, list):
                items = []
            out["items"] = [_normalize_event_item(x) for x in items]
            return out
    legacy_items = [_normalize_event_item(x) for x in reversed(read_audit_all_with_ids(max_lines=max(lim, 1000)))]
    if wanted:
        legacy_items = [x for x in legacy_items if str(x.get("event") or "") in wanted]
    return {
        "ok": True,
        "source": "legacy",
        "limit": lim,
        "items": legacy_items[:lim],
    }


def _uplink_map_from_out(items, out):
    nyc_ips = set(str(x or "").strip() for x in out.get("nyc_ips", []))
    fra_ips = set(str(x or "").strip() for x in out.get("fra_ips", []))
    uplink_map = {}
    for x in items:
        pid = str(x.get("id") or "").strip()
        ip_short = str(x.get("allowed_ip") or "").split("/", 1)[0].strip()
        if ip_short and ip_short in fra_ips:
            uplink_map[pid] = "fra"
        elif ip_short and ip_short in nyc_ips:
            uplink_map[pid] = "nyc"
        else:
            uplink_map[pid] = "ams"
    return uplink_map


def _item_effective_edge(item):
    row = dict(item or {})
    effective_edge = str(row.get("effective_edge") or "").strip().lower()
    if _is_supported_edge(effective_edge):
        return effective_edge
    ingress_edge = str(row.get("ingress_edge") or "").strip().lower()
    if _is_supported_edge(ingress_edge):
        return ingress_edge
    current_edge = str(row.get("current_edge_id") or "").strip().lower()
    if _is_supported_edge(current_edge):
        return current_edge
    ip_short = str(row.get("allowed_ip") or "").split("/", 1)[0].strip()
    if ip_short.startswith("10.18."):
        return "vrn"
    return "edg"


def _filter_items_by_scope(items, scope):
    wanted = str(scope or "all").strip().lower()
    rows = list(items or [])
    if wanted == "all":
        return rows
    if wanted not in SUPPORTED_EDGES:
        return rows
    return [x for x in rows if _item_effective_edge(x) == wanted]


def _peer_routing_view(item, uplink_map=None):
    row = dict(item or {})
    uplink_map = uplink_map or {}
    peer_id = str(row.get("id") or "").strip()
    fallback_uplink = str(uplink_map.get(peer_id) or "").strip().lower() or "ams"
    preferred = str(row.get("preferred_uplink") or "").strip().lower() or fallback_uplink
    active = str(row.get("active_uplink") or "").strip().lower() or str(row.get("effective_uplink") or "").strip().lower() or preferred
    effective = str(row.get("effective_uplink") or "").strip().lower() or active or preferred
    failover = str(row.get("failover_uplink") or "").strip().lower()
    if not failover:
        failover = "fra" if preferred == "ams" else "ams"
        if preferred == "nyc":
            failover = "ams"
    return {
        "ingress_edge": str(row.get("ingress_edge") or row.get("effective_edge") or "edg").strip() or "edg",
        "effective_edge": str(row.get("effective_edge") or row.get("ingress_edge") or "edg").strip() or "edg",
        "policy_mode": str(row.get("policy_mode") or "auto").strip() or "auto",
        "preferred_uplink": preferred,
        "active_uplink": active or preferred,
        "effective_uplink": effective or active or preferred,
        "failover_uplink": failover,
        "health_status": str(row.get("health_status") or "").strip(),
        "health_note": str(row.get("health_note") or "").strip(),
    }


def _routing_drift_view(routing):
    row = dict(routing or {})
    ingress_edge = str(row.get("ingress_edge") or "").strip().lower()
    effective_edge = str(row.get("effective_edge") or "").strip().lower()
    policy_mode = str(row.get("policy_mode") or "").strip().lower()
    preferred_uplink = str(row.get("preferred_uplink") or "").strip().lower()
    effective_uplink = str(row.get("effective_uplink") or row.get("active_uplink") or "").strip().lower()
    issues = []
    if ingress_edge and effective_edge and ingress_edge != effective_edge:
        issues.append("edge")
    if preferred_uplink and effective_uplink and preferred_uplink != effective_uplink:
        issues.append("uplink")
    if policy_mode == "manual" and preferred_uplink and effective_uplink and preferred_uplink != effective_uplink:
        issues.append("policy")
    health_status = str(row.get("health_status") or "").strip().lower()
    health_note = str(row.get("health_note") or "").strip()
    if health_status and health_status not in ("healthy", "ok"):
        issues.append("health")
    if issues:
        return {
            "has_drift": True,
            "code": ",".join(issues),
            "label": "drift",
            "note": health_note or "policy/effective mismatch",
        }
    return {
        "has_drift": False,
        "code": "aligned",
        "label": "aligned",
        "note": health_note or "policy and effective routing match",
    }


def _run_billing_raw(args):
    cli = (WG_BILLING_CLI or "").strip()
    if not cli:
        return {"ok": False, "error": "billing_cli_empty"}
    if not Path(cli).exists():
        return {"ok": False, "error": f"billing_cli_not_found:{cli}"}
    p = subprocess.run([WG_BILLING_PYTHON, cli] + list(args), capture_output=True, text=True)
    out = (p.stdout or "").strip()
    if out:
        try:
            data = json.loads(out)
            if isinstance(data, dict):
                if p.returncode != 0 and "ok" not in data:
                    data["ok"] = False
                return data
        except Exception:
            pass
    return {
        "ok": False,
        "error": one_line((p.stderr or out or f"billing_cli_rc_{p.returncode}"), 300),
    }


def run_billing(args):
    init = _run_billing_raw(["init-db"])
    if not init.get("ok"):
        return init
    return _run_billing_raw(args)


def billing_list_orders(limit=300):
    return run_billing(["list-orders", "--limit", str(int(limit))])


def billing_latest_for_peer(peer_id, limit=300):
    pid = str(peer_id or "").strip()
    out = billing_list_orders(limit=limit)
    if not out.get("ok"):
        return out
    for x in out.get("items", []):
        if str(x.get("peer_id") or "").strip() == pid:
            return {"ok": True, "item": x}
    return {"ok": True, "item": None}


def billing_create_order(peer_id, label):
    pid = str(peer_id or "").strip()
    lbl = str(label or "").strip() or "wg"
    purpose = WG_BILLING_PURPOSE_TEMPLATE.format(peer_id=pid, label=lbl)
    args = [
        "create-order",
        "--peer-id",
        pid,
        "--amount-rub",
        str(WG_BILLING_AMOUNT_RUB),
        "--purpose",
        purpose,
        "--ttl-sec",
        str(int(WG_BILLING_TTL_SEC)),
    ]
    if WG_BILLING_REDIRECT_URL:
        args += ["--redirect-url", WG_BILLING_REDIRECT_URL]
    return run_billing(args)


def billing_poll_once(limit=50):
    return run_billing(["poll-once", "--limit", str(int(limit))])


def billing_status_ru(status):
    st = str(status or "").strip().upper()
    mapping = {
        "NEW": "новый",
        "AWAITING_PAYMENT": "ожидает оплату",
        "PAID": "оплачен",
        "FAILED": "ошибка",
        "EXPIRED": "истек",
    }
    return mapping.get(st, st.lower() if st else "-")


def page(title, body, card_h2_reset=True):
    card_h2_css = ".card > h2{margin-top:0}.card > h3{margin-top:0}" if card_h2_reset else ""
    return f"""<!doctype html>
<html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">
<link rel=\"icon\" href=\"data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 64 64'%3E%3Cdefs%3E%3CradialGradient id='g' cx='30%25' cy='25%25' r='75%25'%3E%3Cstop offset='0%25' stop-color='%239adf3f'/%3E%3Cstop offset='60%25' stop-color='%235da60f'/%3E%3Cstop offset='100%25' stop-color='%2348880a'/%3E%3C/radialGradient%3E%3C/defs%3E%3Ccircle cx='32' cy='32' r='30' fill='url(%23g)'/%3E%3Cpath d='M18 33 L28 43 L46 23' fill='none' stroke='white' stroke-width='9' stroke-linecap='round' stroke-linejoin='round'/%3E%3C/svg%3E\">
<title>{html.escape(title)}</title>
<style>
:root{{--bg:#eef3fb;--card:#ffffff;--text:#0b1220;--muted:#5d6b82;--line:#d9e3f2;--ok:#0f9d58;--warn:#b45309}}
*{{box-sizing:border-box}} body{{font-family:Inter,-apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;background:var(--bg);color:var(--text);max-width:1180px;margin:20px auto;padding:0 16px;line-height:1.5}}
h1,h2,h3,h4,a{{color:#4CAF50}}
.card{{background:var(--card);border:1px solid var(--line);border-radius:16px;padding:18px;margin:14px 0;box-shadow:0 8px 30px rgba(15,23,42,.04)}}
{card_h2_css}
pre{{white-space:pre-wrap;background:#f8fafc;padding:12px;border-radius:8px;overflow:auto}}
code{{background:#f1f5f9;padding:2px 5px;border-radius:5px}}
.btn{{display:inline-block;font-size:14px;padding:7px 20px;border:1px solid #111827;border-radius:8px;text-decoration:none;color:#111827;background:#fff;cursor:pointer}}
.btn:hover{{background:#f8fafc}}
.btn.sm{{padding:8px 10px;font-size:13px}}
.btn.icon{{padding:8px 10px;min-width:42px;display:inline-flex;align-items:center;justify-content:center}}
.btn.os{{display:inline-flex;align-items:center;gap:8px;font-weight:600;padding:12px 20px;min-width:128px}}
.os-ico{{width:20px;height:20px;display:inline-block;vertical-align:middle}}
.btn.copy{{border-style:dashed}}
.small{{color:var(--muted);font-size:12px}}
.ok{{color:var(--ok)}} .warn{{color:var(--warn)}}
table{{border-collapse:collapse;width:100%}} th,td{{border-bottom:1px solid var(--line);padding:8px;text-align:left;font-size:14px;vertical-align:top}}
.install-grid{{display:grid;grid-template-columns:repeat(auto-fit,minmax(164px,1fr));gap:12px}}
.install-card{{border:0;border-radius:10px;padding:0;background:#fff;display:flex;gap:10px;align-items:center;justify-content:center;margin-bottom:10px}}
.install-just p{{font-size:20px;font-weight:700;line-height:1.25;margin:0}}
.step-num{{display:block;font-size:54px;font-weight:900;line-height:1;height:62px;color:#FF9800;margin:0;border:4px solid;border-radius:50%;text-align:center;width:62px}}
.btn-link{{text-decoration:underline;font-weight:800}}
.brand{{display:flex;align-items:center;gap:10px;margin-bottom:14px}}
@media (max-width:720px){{.brand{{flex-wrap:wrap;}}}}
.brand-left{{display:flex;align-items:center;gap:10px;text-decoration:none}}
.brand-title{{margin:0;text-decoration:none;color:#4CAF50}}
.brand-logo{{width:38px;height:38px;display:block}}
.top-menu{{margin-left:auto;display:flex;gap:10px;flex-wrap:wrap;justify-content:flex-end}}
.top-menu a{{display:inline-block;text-decoration:none;font-size:14px;font-weight:700;color:#4CAF50;border:1px solid #4CAF50;border-radius:8px;padding:6px 12px;background:#fff;line-height:1.2}}
.top-menu a:hover{{text-decoration:none;background:#eef8e8}}
.back{{margin:6px 0 0}}
.actions{{display:flex;gap:8px;flex-wrap:wrap;align-items:center}}
.sr{{position:absolute;left:-10000px;top:auto;width:1px;height:1px;overflow:hidden}}
input, select {{
    font-size: 14px;
    padding: 8px 10px;
    border-radius: 4px;
    border: 1px solid;
    height: 36px;
}}
div.d-flex {{
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    justify-content: space-between;
    gap: 20px;
    max-width: 375px;
}}
div.d-flex button.btn, div.d-flex input, div.d-flex select {{
    display: block;
    border-radius: 8px;
    padding: 10px;
    font-size: 14px;
    width: 100%;
    height: 38px;
}}
div.d-flex label {{ width: 100%; }}
.btn[disabled], .btn.disabled {{ opacity: .5; cursor: not-allowed; background: #4caf5161; }}
.field-error {{ border-color: #d93025 !important; box-shadow: 0 0 0 2px rgba(217,48,37,.18); }}
.form-error {{ display: none; width: 100%; color: #b3261e; font-size: 13px; line-height: 1.3; }}
.new-form-loader {{
    display: none;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    color: var(--muted);
    width: 100%;
}}
.new-form-loader::before {{
    content: '';
    width: 14px;
    height: 14px;
    border: 2px solid #d0d0d0;
    border-top-color: #666;
    border-radius: 50%;
    animation: btnSpin .8s linear infinite;
}}
@keyframes btnSpin {{ to {{ transform: rotate(360deg); }} }}
.footer{{margin:20px 0 10px;padding:12px 0;border-top:1px solid var(--line);color:var(--muted);font-size:13px;display:flex;justify-content:space-between;gap:10px;flex-wrap:wrap}}
.peer-head{{display:flex;align-items:flex-start;justify-content:space-between;gap:12px;flex-wrap:wrap}}
.peer-title{{margin:0}}
.kv-grid{{display:grid;grid-template-columns:1fr 1fr;gap:10px;margin-top:8px}}
.kv{{border:1px solid var(--line);border-radius:10px;padding:10px 12px;background:#fff}}
.kv-link{{display:block;text-decoration:none;color:inherit}}
.kv-link:hover{{background:#f8fafc;text-decoration:none}}
.kv .k{{display:block;font-size:12px;color:var(--muted);margin-bottom:4px}}
.kv .v{{font-size:15px}}
.peer-edit{{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin:8px 0 14px}}
.peer-edit input{{min-width:260px}}
@media (max-width:720px){{.kv-grid{{grid-template-columns:1fr}} .peer-edit input{{min-width:0;width:100%}}}}
.hero{{position:relative;overflow:hidden;background:linear-gradient(135deg,#4e99a5 0%,#011341 40%,#011341 100%);color:#eaf3ff;border:none;border-radius:18px;padding:0;margin-bottom:20px}}
@keyframes heroPulse{{0%,100%{{transform:translate3d(0,0,0)}}50%{{transform:translate3d(0,-8px,0)}}}}
.hero *{{position:relative;z-index:1}}
.hero h1,.hero h2{{color:#f3f7ff;font-size:46px;line-height:1.05;letter-spacing:-.02em;margin:0 0 14px;max-width:860px}}
.hero p{{font-size:20px;color:#cfe0ff;max-width:760px;margin:0 0 20px}}
.hero-cta{{display:flex;gap:12px;flex-wrap:wrap;align-items:center}}
.btn.primary{{background:#FF9800;color:#fff;border-color:#FF9800;font-weight:800;padding:10px 18px;box-shadow:0 8px 18px rgba(255,152,0,.35)}}
.btn.primary:hover{{background:#ffab2e;border-color:#ffab2e}}
.btn.ghost{{background:rgba(255,255,255,.04);color:#e5efff;border-color:#9cb7f0;font-weight:700;padding:10px 18px}}
.btn.ghost:hover{{background:rgba(255,255,255,.12)}}
.section h2{{margin:0 0 10px;font-size:38px;line-height:1.1;letter-spacing:-.01em}}
.lead{{font-size:18px;color:#334155;margin:0 0 12px;max-width:900px}}
.features{{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:10px;margin-top:10px}}
.feature{{border:1px solid var(--line);border-radius:12px;padding:14px;background:#fff;min-height:148px}}
.feature h3{{margin:0 0 6px;font-size:18px;line-height:1.2;color:#FF9800}}
.feature p{{margin:0;color:#475569;font-size:16px;line-height:1.4}}
.visual-line{{display:flex;align-items:center;justify-content:center;gap:10px;color:#475569;font-weight:700;padding:10px;border:1px dashed #cbd5e1;border-radius:10px;margin-top:8px;flex-wrap:wrap}}
.dot{{width:8px;height:8px;border-radius:999px;background:#4CAF50;display:inline-block}}
.steps{{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:10px}}
.step{{border:1px solid var(--line);border-radius:12px;padding:14px;background:#fff;min-height:172px}}
.step b{{display:block;font-size:40px;color:#FF9800;line-height:1;margin-bottom:8px}}
.pricing{{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:10px}}
.plan{{border:1px solid var(--line);border-radius:12px;padding:14px;background:#fff;min-height:220px}}
.plan h3{{margin:0 0 4px}}
.plan .price{{font-size:22px;font-weight:800;color:#0f172a}}
.testimonials{{display:grid;grid-template-columns:repeat(auto-fit,minmax(240px,1fr));gap:10px}}
.quote{{border:1px solid var(--line);border-radius:12px;padding:14px;background:#fff;min-height:180px}}
.rating{{color:#f59e0b;font-weight:700;margin-top:6px}}
.trust{{display:grid;grid-template-columns:repeat(auto-fit,minmax(160px,1fr));gap:8px}}
.badge{{border:1px solid var(--line);border-radius:999px;padding:10px 12px;text-align:center;background:#fff;color:#334155;font-weight:600}}
.newsletter{{display:flex;gap:8px;flex-wrap:wrap;align-items:center}}
.newsletter input{{min-width:220px;flex:1}}
.hero-wrap{{display:grid;grid-template-columns:1.25fr .95fr;gap:20px;align-items:center}}
.hero-copy{{padding:18px 0 18px 18px}}
.hero-copy .kicker{{margin:0 0 10px;font-size:12px;font-weight:800;letter-spacing:.14em;color:#93c5fd;text-transform:uppercase}}
.hero-lead{{font-size:19px;color:#d9e7ff;max-width:760px;margin:0 0 20px}}
.hero-visual{{min-height:100%;background:url('/assets/img/main.webp') center no-repeat;background-size:cover}}
.section-kicker{{margin:0 0 8px;font-size:12px;font-weight:800;letter-spacing:.14em;text-transform:uppercase;color:#FF9800}}
.split{{display:grid;grid-template-columns:1fr 1.3fr;gap:18px;align-items:stretch}}
.security-illustration{{min-height:360px;background:url('/assets/img/privacy.webp') center no-repeat;background-size:contain}}
.checklist{{margin:10px 0 0;padding-left:20px;color:#334155}}
.checklist li{{margin:6px 0}}
.split.reverse{{grid-template-columns:1fr 1fr}}
.media-card{{position:relative;border-radius:14px;overflow:hidden;border:1px solid #bdd2ee;min-height:360px}}
.media-image{{position:absolute;inset:0;background:url('/assets/img/security.webp') center no-repeat;background-size:cover}}
.media-overlay{{position:absolute;bottom:0;right:0;z-index:1;padding:20px;color:#fff;max-width:360px}}
.media-overlay h3{{color:#fff;margin:0 0 10px}}
.media-overlay p{{color:#dbeafe;margin:0 0 12px}}
.meter{{margin:10px 0}}
.meter-row{{display:flex;justify-content:space-between;gap:12px;font-weight:700;color:#334155}}
.meter-bar{{height:8px;border-radius:999px;background:#dbe8fb;overflow:hidden;margin-top:4px}}
.meter-bar i{{display:block;height:100%;background:linear-gradient(90deg,#22c55e,#3b82f6)}}
.feedback .quote{{min-height:220px}}
.profile-image{{width:72px;height:72px;border-radius:999px;overflow:hidden;margin:0 0 8px;border:2px solid #c7d6ea}}
.profile-image img{{width:100%;height:100%;object-fit:cover;display:block}}
.steps-quick .step{{display:flex;align-items:center;gap:14px;min-height:auto}}
.steps-quick .step p{{margin:0;font-size:clamp(18px,2.4vw,22px);font-weight:700;line-height:1.25}}
@media (max-width:900px){{.hero-wrap{{grid-template-columns:1fr}} .split{{grid-template-columns:1fr}} .hero-visual{{min-height:240px}} .split.reverse{{grid-template-columns:1fr}}}}
@media (max-width:720px){{.brand{{flex-wrap:wrap}} .hero h1,.hero h2{{font-size:36px;line-height:1.12}} .hero p,.hero-lead{{font-size:17px}} .hero-copy{{padding:18px}} .section h2{{font-size:30px}} .steps-quick .step{{align-items:flex-start}}}}

</style></head><body>
<div class='brand'>
  <a class='brand-left' href='/' aria-label='На главную'>
    <svg class='brand-logo' viewBox='0 0 64 64' aria-hidden='true'>
      <defs>
        <radialGradient id='logoG' cx='30%' cy='25%' r='75%'>
          <stop offset='0%' stop-color='#9adf3f'/>
          <stop offset='60%' stop-color='#5da60f'/>
          <stop offset='100%' stop-color='#4CAF50'/>
        </radialGradient>
      </defs>
      <circle cx='32' cy='32' r='30' fill='url(#logoG)'/>
      <path d='M18 33 L28 43 L46 23' fill='none' stroke='white' stroke-width='9' stroke-linecap='round' stroke-linejoin='round'/>
    </svg>
    <h1 class='brand-title'>{html.escape(TITLE)}</h1>
  </a>
  <nav class='top-menu' aria-label='Меню'>
    <a href='/account/profiles/new'>Новый профиль</a>
    <a href='/help/'>Помощь</a>
    <a id='menu-lk' href='/account/'>ЛК</a>
  </nav>
</div>
{body}
<footer class='footer'>
  <span>2024–2026 © JsTun</span>
  <span><a href='mailto:admin@jstun.com'>admin@jstun.com</a></span>
  <span>Защищенное подключение</span>
</footer>
<script>
(() => {{
  const lk = document.getElementById('menu-lk');
  if (!lk) return;
  lk.style.display = 'inline';
}})();
</script>
</body></html>"""


def back_home_link():
    return ""

def lk_login_form(prefill_id=""):
    pid = (str(prefill_id or "")).strip()
    return """
<div class='card'>
<h3>Recovery доступ</h3>
<form method='post' action='/recover/'>
 <label>ID: <input name='id' maxlength='64' placeholder='ID из /new/' value='{pid}'></label><br><br>
 <label>Код: <input name='k' maxlength='128' placeholder='секретный код ЛК'></label><br><br>
 <button class='btn' type='submit'>Открыть recovery</button>
 </form>
 <p class='small'>Используйте эту форму только как аварийный сценарий, если активное WG-подключение временно не определяется автоматически.</p>
 </div>
""".format(pid=html.escape(pid))


def _lk_mode_badge(via):
    mode = str(via or "").strip().lower()
    if mode == "wg":
        return "<span class='small'><code>WG auto</code></span>"
    if mode == "cookie":
        return "<span class='small'><code>legacy cookie</code></span>"
    return "<span class='small'><code>fallback</code></span>"


def esc(s):
    return html.escape(str(s if s is not None else ""))

def fmt_dt_ui(iso_z):
    # Input examples: 2026-02-15T20:02:25Z
    # Output: dd.mm.yy H:i:s (portal display timezone)
    s = (str(iso_z or "")).strip()
    if not s:
        return ""
    try:
        if s.endswith("Z"):
            d = dt.datetime.fromisoformat(s.replace("Z", "+00:00"))
        else:
            d = dt.datetime.fromisoformat(s)
        if d.tzinfo is None:
            d = d.replace(tzinfo=dt.timezone.utc)
        d = d.astimezone(DISPLAY_TZ)
        return d.strftime("%d.%m.%y %H:%M:%S")
    except Exception:
        return s

def safe_slug(s, default="wireguard-export", max_len=32):
    s = (str(s or "")).strip()
    out = []
    for ch in s:
        if ("a" <= ch <= "z") or ("A" <= ch <= "Z") or ("0" <= ch <= "9") or ch in ("-", "_", "."):
            out.append(ch)
        else:
            out.append("_")
    res = "".join(out).strip("._-")
    if not res:
        res = default
    return res[:max_len]

def safe_id(s, default="peer", max_len=64):
    s = (str(s or "")).strip()
    out = []
    for ch in s:
        if ("a" <= ch <= "z") or ("A" <= ch <= "Z") or ("0" <= ch <= "9") or ch in ("-", "_"):
            out.append(ch)
        else:
            out.append("_")
    res = "".join(out)
    if not res:
        res = default
    return res[:max_len]

def read_audit_tail(limit=50):
    try:
        if not AUDIT_LOG.exists():
            return []
        lines = AUDIT_LOG.read_text(encoding="utf-8", errors="replace").splitlines()
        out = []
        for ln in lines[-max(1, int(limit)) :]:
            ln = (ln or "").strip()
            if not ln:
                continue
            try:
                out.append(json.loads(ln))
            except Exception:
                out.append({"event": "audit_parse_error", "raw": ln[:300]})
        return out
    except Exception:
        return []


def read_audit_all(max_lines=None):
    try:
        if not AUDIT_LOG.exists():
            return []
        lim = AUDIT_MAX_LINES if max_lines is None else int(max_lines)
        lim = max(1, lim)
        lines = AUDIT_LOG.read_text(encoding="utf-8", errors="replace").splitlines()
        out = []
        for ln in lines[-lim:]:
            ln = (ln or "").strip()
            if not ln:
                continue
            try:
                out.append(json.loads(ln))
            except Exception:
                out.append({"event": "audit_parse_error", "raw": ln[:300]})
        return out
    except Exception:
        return []


def read_audit_all_with_ids(max_lines=None):
    items = read_audit_all(max_lines=max_lines)
    out = []
    for idx, item in enumerate(items, start=1):
        row = dict(item or {})
        row["_event_id"] = idx
        out.append(row)
    return out

def one_line(s, max_len=140):
    s = str(s if s is not None else "")
    s = " ".join(s.split())
    if len(s) > max_len:
        return s[: max_len - 1] + "…"
    return s

def svg_trash():
    return (
        "<svg width='18' height='18' viewBox='0 0 24 24' fill='none' xmlns='http://www.w3.org/2000/svg'>"
        "<path d='M9 3h6l1 2h4v2H4V5h4l1-2Z' fill='currentColor'/>"
        "<path d='M6 9h12l-1 12H7L6 9Z' fill='currentColor'/>"
        "</svg>"
    )

def svg_block():
    return (
        "<svg width='18' height='18' viewBox='0 0 24 24' fill='none' xmlns='http://www.w3.org/2000/svg'>"
        "<path d='M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20Zm6 10a5.98 5.98 0 0 1-1.42 3.88L8.12 7.42A5.98 5.98 0 0 1 12 6c3.31 0 6 2.69 6 6ZM6 12c0-1.5.55-2.87 1.46-3.92l8.46 8.46A5.98 5.98 0 0 1 12 18c-3.31 0-6-2.69-6-6Z' fill='currentColor'/>"
        "</svg>"
    )

EVENT_LABELS_RU = {
    "admin_login_attempt": "Вход в админку: попытка",
    "admin_action_ok": "Действие админа: успешно",
    "admin_action_error": "Действие админа: ошибка",
    "lk_login_ok": "Вход в ЛК: успешно",
    "lk_login_denied": "Вход в ЛК: отказ",
    "api_create": "API: создание peer",
    "api_block": "API: блокировка peer",
    "api_remove": "API: удаление peer",
    "api_reissue": "API: перевыпуск peer",
    "api_list": "API: список peers",
    "api_unauthorized": "API: не авторизован",
    "new_create_ok": "Новый профиль: успешно",
    "new_create_attempt": "Новый профиль: попытка",
    "new_denied": "Новый профиль: отказ",
    "reissue_ok": "Перевыпуск: успешно",
    "reissue_denied": "Перевыпуск: отказ",
    "reissue_finalize_ok": "Перевыпуск: завершен",
    "cleanup_remove": "Очистка: удаление peer",
    "billing_create_attempt": "Billing: попытка создания",
    "billing_create_ok": "Billing: заказ создан",
    "billing_create_error": "Billing: ошибка создания",
    "billing_create_skip_existing": "Billing: пропуск, уже есть заказ",
    "billing_refresh_ok": "Billing: статус обновлен",
    "billing_refresh_attempt": "Billing: попытка обновления",
    "billing_denied": "Billing: отказ",
    "dlzip_ok": "Скачивание ZIP: успешно",
    "logout": "Выход из ЛК",
}


def event_label_ru(event_name):
    key = str(event_name or "").strip()
    if not key:
        return "Все события"
    return EVENT_LABELS_RU.get(key, key)


def _event_matches_quick_filter(event_name, quick_filter):
    name = str(event_name or "").strip()
    qf = str(quick_filter or "").strip()
    if not qf:
        return True
    if qf == "login_denied":
        return name == "lk_login_denied"
    if qf == "admin_errors":
        return name == "admin_action_error"
    if qf == "admin_ok":
        return name == "admin_action_ok"
    if qf == "api":
        return name.startswith("api_")
    return True


PEER_STATUS_LABELS_RU = {
    "pending": "ожидает",
    "active": "активен",
    "blocked": "заблокирован",
    "expired": "истек",
    "removed": "удален",
}


def peer_status_ru(status):
    key = str(status or "").strip().lower()
    if not key:
        return "все"
    return PEER_STATUS_LABELS_RU.get(key, key)


class H(BaseHTTPRequestHandler):
    def _lock_audit(self):
        STATE_DIR.mkdir(parents=True, exist_ok=True)
        fd = os.open(str(AUDIT_LOCK), os.O_CREAT | os.O_RDWR, 0o600)
        fcntl.flock(fd, fcntl.LOCK_EX)
        return fd

    def _unlock_audit(self, fd):
        os.close(fd)

    def _audit(self, event, **fields):
        rec = {
            "ts": dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
            "event": str(event),
            "ip": self._client_ip(),
            "ua": (self.headers.get("User-Agent", "") or "")[:300],
        }
        for k, v in fields.items():
            if v is None:
                continue
            rec[str(k)] = v
        fd = self._lock_audit()
        try:
            with open(AUDIT_LOG, "a", encoding="utf-8") as f:
                f.write(json.dumps(rec, ensure_ascii=False) + "\n")
        finally:
            self._unlock_audit(fd)

    def _lock_new_ip_db(self):
        STATE_DIR.mkdir(parents=True, exist_ok=True)
        fd = os.open(str(NEW_IP_LOCK), os.O_CREAT | os.O_RDWR, 0o600)
        fcntl.flock(fd, fcntl.LOCK_EX)
        return fd

    def _unlock_new_ip_db(self, fd):
        os.close(fd)

    def _load_new_ip_db(self):
        try:
            if not NEW_IP_DB.exists():
                return {}
            return json.loads(NEW_IP_DB.read_text(encoding="utf-8") or "{}")
        except Exception:
            return {}

    def _save_new_ip_db(self, db):
        NEW_IP_DB.write_text(json.dumps(db, ensure_ascii=False, indent=2), encoding="utf-8")

    def _client_ip(self):
        # Для LK важен не hop прокси, а исходный клиентский адрес.
        # Пробуем вытащить его из полной XFF-цепочки, предпочитая первый публичный IP.
        cands = []
        hdr = self.headers.get("X-Forwarded-For", "") or ""
        if hdr:
            for part in hdr.split(","):
                ip_s = str(part or "").strip()
                if not ip_s:
                    continue
                try:
                    ipaddress.ip_address(ip_s)
                except Exception:
                    continue
                if ip_s not in cands:
                    cands.append(ip_s)
        for raw in (
            self.headers.get("X-Real-IP", "") or "",
            self.client_address[0] if self.client_address else "",
        ):
            ip_s = str(raw or "").strip()
            if not ip_s:
                continue
            try:
                ipaddress.ip_address(ip_s)
            except Exception:
                continue
            if ip_s not in cands:
                cands.append(ip_s)
        for ip_s in cands:
            if _ip_is_public(ip_s):
                return ip_s
        return cands[0] if cands else "0.0.0.0"

    def _client_ip_candidates(self):
        seen = []
        hdr = self.headers.get("X-Forwarded-For", "") or ""
        for raw in (hdr.split(",") if hdr else []):
            ip_s = str(raw or "").strip()
            if not ip_s:
                continue
            try:
                ipaddress.ip_address(ip_s)
            except Exception:
                continue
            if ip_s not in seen:
                seen.append(ip_s)
        for raw in (
            self.headers.get("X-Real-IP", "") or "",
            self.client_address[0] if self.client_address else "",
        ):
            ip_s = str(raw or "").strip()
            if not ip_s:
                continue
            try:
                ipaddress.ip_address(ip_s)
            except Exception:
                continue
            if ip_s not in seen:
                seen.append(ip_s)
        ordered = [x for x in seen if _ip_is_public(x)] + [x for x in seen if not _ip_is_public(x)]
        return ordered or ["0.0.0.0"]

    def _tg_alert(self, lines, dedupe_key="", cooldown_sec=0):
        if not TG_ALERTS_ENABLED:
            return
        if dedupe_key and int(cooldown_sec or 0) > 0:
            now = int(dt.datetime.now(dt.timezone.utc).timestamp())
            prev = int(ALERT_LAST_SENT.get(dedupe_key) or 0)
            if now - prev < int(cooldown_sec):
                return
            ALERT_LAST_SENT[dedupe_key] = now
        text = "\n".join([str(x) for x in (lines or []) if str(x).strip()])
        if text:
            _tg_send_text(text)

    def _notify_admin_login(self, ok, via, reason=""):
        ip = self._client_ip()
        geo = _geo_for_ip(ip)
        host = (self.headers.get("Host", "") or "").strip()
        status = "успех" if ok else "отказ"
        lines = [
            "JsTun: попытка входа в админку",
            f"Статус: {status}",
            f"Способ: {via or '-'}",
            f"IP: {ip}",
            f"Гео: {geo or 'не определено'}",
            f"Host: {host or '-'}",
            f"Причина: {reason or '-'}",
        ]
        dedupe_key = ""
        cooldown = 0
        if (via or "") == "cookie" and not ok:
            dedupe_key = f"admin_bad_cookie:{ip}"
            cooldown = ADMIN_BAD_COOKIE_ALERT_COOLDOWN_SEC
        elif (via or "") == "none" and not ok:
            if not _is_public_ip(ip):
                return
            dedupe_key = f"admin_missing_token:{ip}"
            cooldown = ADMIN_MISSING_TOKEN_ALERT_COOLDOWN_SEC
        elif (via or "") == "query" and ok:
            if _is_public_ip(ip):
                dedupe_key = f"admin_query_ok:{ip}:{host or '-'}"
                cooldown = ADMIN_QUERY_OK_ALERT_COOLDOWN_SEC
            else:
                return
        self._tg_alert(lines, dedupe_key=dedupe_key, cooldown_sec=cooldown)

    def _notify_new_account(self, stage, label="", kind="", peer_id="", allowed_ip=""):
        ip = self._client_ip()
        geo = _geo_for_ip(ip)
        host = (self.headers.get("Host", "") or "").strip()
        lines = [
            "JSTUN: создание нового профиля подключения",
            f"Этап: {stage}",
            f"IP: {ip}",
            f"Гео: {geo or 'не определено'}",
            f"Label: {label or '-'}",
            f"Тип: {kind or '-'}",
            f"Peer ID: {peer_id or '-'}",
            f"WG IP: {allowed_ip or '-'}",
            f"Host: {host or '-'}",
        ]
        self._tg_alert(lines)

    def _send_html(self, s, code=200, headers=None):
        b = s.encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(b)))
        if headers:
            for k, v in headers:
                self.send_header(k, v)
        self.end_headers()
        self.wfile.write(b)

    def _send_json(self, obj, code=200, headers=None):
        b = json.dumps(obj, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(b)))
        if headers:
            for k, v in headers:
                self.send_header(k, v)
        self.end_headers()
        self.wfile.write(b)

    def _send_bytes(self, b, content_type, code=200, headers=None):
        self.send_response(code)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(b)))
        if headers:
            for k, v in headers:
                self.send_header(k, v)
        self.end_headers()
        self.wfile.write(b)

    def _send_csv(self, filename, rows, headers_row):
        buf = io.StringIO()
        w = csv.writer(buf)
        w.writerow(headers_row)
        for r in rows:
            w.writerow(r)
        b = buf.getvalue().encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "text/csv; charset=utf-8")
        self.send_header("Content-Disposition", f"attachment; filename=\"{filename}\"")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def _redirect(self, location, headers=None, code=303):
        self.send_response(code)
        self.send_header("Location", location)
        if headers:
            for k, v in headers:
                self.send_header(k, v)
        self.end_headers()

    def _serve_static(self, req_path):
        rel = (str(req_path or "").lstrip("/")).strip()
        root = STATIC_ROOT.resolve()
        target = (root / rel).resolve()
        if not str(target).startswith(str(root) + os.sep):
            return self._send_html(page("403", "<div class='card'>Forbidden</div>"), 403)
        if not target.exists() or not target.is_file():
            return self._send_html(page("404", "<div class='card'>Not found</div>"), 404)
        ext = target.suffix.lower()
        ctype = {
            ".webp": "image/webp",
            ".png": "image/png",
            ".jpg": "image/jpeg",
            ".jpeg": "image/jpeg",
            ".svg": "image/svg+xml",
            ".gif": "image/gif",
            ".ico": "image/x-icon",
            ".css": "text/css; charset=utf-8",
            ".js": "application/javascript; charset=utf-8",
        }.get(ext, "application/octet-stream")
        try:
            data = target.read_bytes()
        except Exception:
            return self._send_html(page("500", "<div class='card'>Read error</div>"), 500)
        return self._send_bytes(data, ctype)

    def _read_form(self):
        l = int(self.headers.get("Content-Length", "0") or "0")
        raw = self.rfile.read(l).decode("utf-8", errors="ignore")
        return parse_qs(raw)

    def _cookies(self):
        c = SimpleCookie()
        c.load(self.headers.get("Cookie", ""))
        return {k: m.value for k, m in c.items()}

    def _account_session_token(self):
        return str(self._cookies().get(ACCOUNT_LK_SESSION_COOKIE, "") or "").strip()

    def _account_lk_current_account(self):
        svc = _account_lk_service()
        if svc is None:
            return None
        token = self._account_session_token()
        if not token:
            return None
        try:
            return svc.resolve_account_by_session(token)
        except Exception:
            return None

    def _account_lk_nav(self):
        items = [
            ("/account/", "Аккаунт"),
            ("/account/profiles/new", "Новый профиль"),
            ("/account/claims/new", "Подключить профиль"),
        ]
        current = urlparse(self.path).path
        links = []
        for href, title in items:
            if current == href or (href == "/account/" and current.startswith("/account/profiles/") and current.endswith("/")):
                links.append(f"<a class='btn disabled' aria-disabled='true'>{esc(title)}</a>")
            else:
                links.append(f"<a class='btn' href='{href}'>{esc(title)}</a>")
        return "<p class='actions'>" + "".join(links) + "</p>"

    def _account_lk_runtime_index(self):
        out = backend_list_for_lk()
        items = list(out.get("items", []) or []) if out.get("ok") else []
        return {
            "ok": bool(out.get("ok")),
            "source": str(out.get("source") or ""),
            "by_id": {str(x.get("id") or "").strip(): x for x in items if str(x.get("id") or "").strip()},
            "items": items,
        }

    def _account_lk_current_claim_candidate(self):
        runtime = self._account_lk_runtime_index()
        if not runtime.get("ok"):
            return None
        auth = self._resolve_lk_access({"items": runtime.get("items", [])}, require_active=False)
        if not auth.get("ok"):
            return None
        item = auth.get("item") or {}
        return {
            "peer_id": str(item.get("id") or "").strip(),
            "label": str(item.get("admin_label") or item.get("label") or item.get("id") or "").strip(),
            "allowed_ip": str(item.get("allowed_ip") or "").strip(),
            "status": str(item.get("status") or "").strip(),
        }

    def _render_account_login(self):
        parsed = urlparse(self.path)
        qs = parse_qs(parsed.query)
        selected_provider = (qs.get("provider", ["telegram"])[0] or "telegram").strip().lower()
        provider_meta = {
            "telegram": ("Telegram", "@username или ID"),
            "vk": ("VK", "ID профиля или username"),
            "yandex": ("Yandex", "Логин Yandex"),
            "google": ("Google", "Email Google"),
        }
        if selected_provider not in provider_meta:
            selected_provider = "telegram"
        provider_links = []
        for key, (title, _placeholder) in provider_meta.items():
            if key == selected_provider:
                provider_links.append(f"<a class='btn disabled' aria-disabled='true'>{esc(title)}</a>")
            else:
                provider_links.append(f"<a class='btn' href='/account/login?provider={quote(key, safe='')}'>{esc(title)}</a>")
        provider_title, provider_placeholder = provider_meta[selected_provider]
        body = f"""
{self._account_lk_nav()}
<div class='card'>
<h2>Вход в аккаунт</h2>
<p class='small'>Вход в новый личный кабинет. Пока это переходный этап: кнопки уже пользовательские, а авторизация ещё работает в bridge-режиме.</p>
<p class='actions'>{"".join(provider_links)}</p>
<form method='post' action='/account/login'>
  <input type='hidden' name='provider' value='{esc(selected_provider)}'>
  <label>Способ входа:<br><input value='{esc(provider_title)}' disabled></label><br><br>
  <label>Логин или ID в {esc(provider_title)}:<br><input name='subject' placeholder='{esc(provider_placeholder)}'></label><br><br>
  <label>Как вас показывать в кабинете:<br><input name='display_name' placeholder='Например, Just'></label><br><br>
  <label>Email, если хотите привязать его к аккаунту:<br><input name='email' placeholder='name@example.com'></label><br><br>
  <button class='btn' type='submit'>Войти</button>
</form>
</div>
"""
        return self._send_html(page("Аккаунт", body))

    def _render_account_home(self):
        svc = _account_lk_service()
        if svc is None:
            return self._send_html(page("Аккаунт", f"{self._account_lk_nav()}<div class='card warn'><b>Новый личный кабинет временно недоступен.</b></div>"), 503)
        account = self._account_lk_current_account()
        if account is None:
            return self._redirect("/account/login", code=302)
        payload = svc.account_home_payload(account.account_id)
        identities_html = "".join(
            f"<li><b>{esc(_account_provider_title(x.get('provider')))}</b>"
            f" <span class='small'>{esc(x.get('subject') or '')}</span></li>"
            for x in (payload.get("identities") or [])
        ) or "<li class='small'>Пока нет привязанных способов входа.</li>"
        runtime = self._account_lk_runtime_index()
        runtime_by_id = runtime.get("by_id", {})
        profiles_html_parts = []
        for x in (payload.get("connection_profiles") or []):
            legacy_peer_id = str(x.get("legacy_peer_id") or "").strip()
            runtime_item = runtime_by_id.get(legacy_peer_id) if legacy_peer_id else None
            connection_profile_id = str(x.get("connection_profile_id") or "").strip()
            label = str(x.get("admin_label") or x.get("display_label") or legacy_peer_id or x.get("connection_profile_id") or "").strip()
            status = str((runtime_item or {}).get("status") or x.get("status") or "").strip()
            allowed_ip = str((runtime_item or {}).get("allowed_ip") or x.get("allowed_ip") or "").strip()
            gateway = str((runtime_item or {}).get("ingress_edge") or x.get("current_edge_id") or "").strip().lower()
            uplink = str((runtime_item or {}).get("effective_uplink") or x.get("effective_uplink_id") or x.get("preferred_uplink_id") or "").strip().lower()
            meta = []
            if allowed_ip:
                meta.append(f"IP: <code>{esc(allowed_ip)}</code>")
            if gateway:
                meta.append(f"Точка подключения: <code>{esc(_public_gateway_label(gateway))}</code>")
            if uplink:
                meta.append(f"Выход в интернет: <code>{esc(_public_uplink_label(uplink))}</code>")
            if legacy_peer_id:
                meta.append(f"<a href='/admin/peers/{quote(legacy_peer_id, safe='')}/?read_mode=shadow'>Открыть в админке</a>")
            detail_href = f"/account/profiles/{quote(connection_profile_id, safe='')}/"
            profiles_html_parts.append(
                "<li>"
                + f"<div><a href='{detail_href}'><b>{esc(label)}</b></a> <code>{esc(_account_profile_status_label(status or 'pending'))}</code></div>"
                + (f"<div class='small'>{' • '.join(meta)}</div>" if meta else "")
                + f"<div class='actions'><a class='btn sm' href='{detail_href}'>Открыть</a></div>"
                + "</li>"
            )
        has_profiles = bool(profiles_html_parts)
        profiles_html = "".join(profiles_html_parts) or "<li class='small'>Пока у вас ещё нет профилей подключения.</li>"
        bal = payload.get("balance") or {}
        acc = payload.get("account") or {}
        current_claim = self._account_lk_current_claim_candidate()
        current_claim_html = ""
        if current_claim and not any(str(x.get("legacy_peer_id") or "").strip() == current_claim["peer_id"] for x in (payload.get("connection_profiles") or [])):
            current_claim_html = f"""
<div class='card'>
  <h3>Текущий активный профиль</h3>
  <p><b>{esc(current_claim.get('label') or current_claim.get('peer_id') or 'Профиль')}</b></p>
  <p class='small'>IP: <code>{esc(current_claim.get('allowed_ip') or '')}</code></p>
  <form method='post' action='/account/claims/new' class='actions'>
    <input type='hidden' name='legacy_peer_id' value='{esc(current_claim.get('peer_id') or '')}'>
    <input type='hidden' name='display_label' value='{esc(current_claim.get('label') or '')}'>
    <input type='hidden' name='claim_method' value='wg_session_attach'>
    <button class='btn' type='submit'>Подключить этот профиль к аккаунту</button>
  </form>
</div>
"""
        body = f"""
{self._account_lk_nav()}
<div class='card'>
  <h2>С чего начать</h2>
  <ol>
    <li>Создайте новый профиль подключения.</li>
    <li>Импортируйте его в WireGuard по QR-коду или через файл конфигурации.</li>
    <li>Включите WireGuard и проверьте статус профиля в этом кабинете.</li>
  </ol>
  <p class='actions'>
    <a class='btn' href='/account/profiles/new'>Создать профиль</a>
    <a class='btn' href='/help/'>Открыть инструкцию</a>
  </p>
</div>
<div class='card'>
  <h2>Аккаунт</h2>
  <form method='post' action='/account/profile' class='actions' style='margin-bottom:12px'>
    <label>Имя аккаунта:<br><input name='display_name' value='{esc(acc.get('display_name') or '')}' placeholder='Как показывать вас в кабинете'></label>
    <button class='btn' type='submit'>Сохранить</button>
  </form>
  <p><b>Имя:</b> <code>{esc(acc.get('display_name') or 'Без имени')}</code></p>
  <p><b>Email:</b> <code>{esc(acc.get('email') or 'не указан')}</code></p>
  <p><b>Статус аккаунта:</b> <code>{esc('активен' if str(acc.get('status') or '') == 'active' else acc.get('status') or '')}</code></p>
  <p><b>Внутренний счёт:</b> <code>{esc(bal.get('available_minor'))}</code> {esc(bal.get('currency') or 'RUB')}</p>
  <details class='small'>
    <summary>Технические данные</summary>
    <p><b>ID аккаунта:</b> <code>{esc(acc.get('account_id'))}</code></p>
  </details>
</div>
{current_claim_html}
<div class='card'><h3>Способы входа</h3><ul>{identities_html}</ul></div>
<div class='card'>
  <h3>Профили подключения</h3>
  <p class='small'>Здесь отображаются все ваши профили и их текущее состояние.</p>
  <ul>{profiles_html}</ul>
  <p class='actions'>
    <a class='btn' href='/account/profiles/new'>Новый профиль</a>
    {"" if has_profiles else "<a class='btn' href='/help/'>Как подключиться</a>"}
  </p>
</div>
"""
        return self._send_html(page("Аккаунт", body))

    def _render_account_profile_new(self):
        if self._account_lk_current_account() is None:
            return self._redirect("/account/login", code=302)
        selected_gateway = "edg"
        body = f"""
{self._account_lk_nav()}
<div class='card'>
<h2>Новый профиль подключения</h2>
<form method='post' action='/account/profiles/new'>
  <label>Название профиля:<br><input name='display_label' placeholder='Например, MacBook или iPhone'></label><br><br>
  <label>Точка подключения:
    <select name='gateway' id='account-profile-gateway'>
      {_edge_options_html('edg', public_labels=True)}
    </select>
  </label><br><br>
  <label>Выход в интернет:
    <select name='uplink' id='account-profile-uplink'>
      <option value='ams' data-edg='{esc(_public_uplink_label('ams'))}' data-vrn='{esc(_public_uplink_label('ams'))}' data-msk_d='{esc(_public_uplink_label('ams'))}'>{esc(_public_uplink_label('ams'))}</option>
      <option value='nyc' data-edg='{esc(_public_uplink_label('nyc'))}' data-vrn='{esc(_public_uplink_label('nyc'))}' data-msk_d='{esc(_public_uplink_label('nyc'))}'>{esc(_public_uplink_label('nyc'))}</option>
      <option value='fra' data-edg='{esc(_public_uplink_label('fra'))}' data-vrn='{esc(_public_uplink_label('fra'))}' data-msk_d='{esc(_public_uplink_label('fra'))}'>{esc(_public_uplink_label('fra'))}</option>
    </select>
  </label><br><br>
  <p class='small' id='account-profile-help'>Новый профиль будет выпущен сразу с QR-кодом и конфигом.</p>
  <button class='btn' type='submit'>Создать профиль</button>
</form>
<script>
(() => {{
  const gatewaySelect = document.getElementById('account-profile-gateway');
  const uplinkSelect = document.getElementById('account-profile-uplink');
  if (!gatewaySelect || !uplinkSelect) return;
  const sync = () => {{
    const gateway = gatewaySelect.value || 'edg';
    for (const option of Array.from(uplinkSelect.options)) {{
      option.textContent = option.dataset[gateway] || option.textContent;
    }}
  }};
  gatewaySelect.addEventListener('change', sync);
  sync();
}})();
</script>
</div>
"""
        return self._send_html(page("Новый профиль", body))

    def _render_account_profile_detail(self, connection_profile_id):
        svc = _account_lk_service()
        account = self._account_lk_current_account()
        if svc is None or account is None:
            return self._redirect("/account/login", code=302)
        profile = svc.store.get_connection_profile(str(connection_profile_id or "").strip())
        if profile is None or str(profile.account_id or "") != str(account.account_id or ""):
            body = f"{self._account_lk_nav()}<div class='card warn'><b>Профиль подключения не найден.</b></div>"
            return self._send_html(page("Профиль подключения", body), 404)
        runtime = self._account_lk_runtime_index()
        runtime_item = runtime.get("by_id", {}).get(str(profile.legacy_peer_id or "").strip()) if str(profile.legacy_peer_id or "").strip() else None
        label = str(profile.admin_label or profile.display_label or profile.legacy_peer_id or profile.connection_profile_id or "").strip()
        allowed_ip = str((runtime_item or {}).get("allowed_ip") or profile.allowed_ip or "").strip()
        gateway = str((runtime_item or {}).get("ingress_edge") or profile.current_edge_id or "").strip().lower()
        uplink = str((runtime_item or {}).get("effective_uplink") or profile.effective_uplink_id or profile.preferred_uplink_id or "").strip().lower()
        last_hs = fmt_dt_ui((runtime_item or {}).get("last_handshake_at") or profile.last_handshake_at or "")
        issued = _load_issued_result(profile.legacy_peer_id) if profile.legacy_peer_id else None
        issued_block = ""
        if issued:
            cfg = str(issued.get("config", "") or "")
            qrb64 = issued.get("qr_png_b64", "")
            qr_is_placeholder = bool(issued.get("qr_is_placeholder"))
            cfg_js = json.dumps(cfg, ensure_ascii=False)
            cfg_b64 = base64.b64encode(cfg.encode("utf-8")).decode("ascii")
            folder = safe_slug(str(issued.get("label") or label or "profile"))
            conf_name = f"{folder}.conf"
            qr_block = _qr_block_html(qrb64, qr_is_placeholder)
            issued_block = f"""
<div class='card'>
  <h3>QR и конфиг</h3>
  <div style='display:grid;grid-template-columns:repeat(auto-fit,minmax(280px,1fr));gap:16px;align-items:start'>
    {qr_block}
    <div>
      <div class='actions' style='display:flex;gap:10px;flex-wrap:nowrap;align-items:center;overflow-x:auto'>
        <button class='btn' type='button' id='copyCfgBtn'>Скопировать</button>
        <form method='post' action='/dlzip/' style='margin:0;display:inline-flex'>
          <input type='hidden' name='id' value='{esc(profile.legacy_peer_id)}'>
          <input type='hidden' name='label' value='{esc(folder)}'>
          <input type='hidden' name='cfg_b64' value='{esc(cfg_b64)}'>
          <button class='btn' type='submit'>Скачать</button>
        </form>
        <span id='copyState' class='small'></span>
      </div>
      <p class='small'>Скачивание отдаст ZIP <code>{esc(folder)}.zip</code> с файлом <code>{esc(conf_name)}</code> внутри.</p>
      <p class='small'>На ПК обычно проще скачать и импортировать файл <code>.conf</code> в WireGuard.</p>
      <pre id='cfgHolder' style='display:none'>{esc(cfg)}</pre>
      <script>
      (() => {{
        const cfg = {cfg_js};
        const btn = document.getElementById('copyCfgBtn');
        const state = document.getElementById('copyState');
        if (!btn) return;
        btn.addEventListener('click', async () => {{
          try {{
            await navigator.clipboard.writeText(cfg);
            state.textContent = 'Конфиг скопирован';
          }} catch (e) {{
            state.textContent = 'Не удалось скопировать автоматически';
            document.getElementById('cfgHolder').style.display = 'block';
          }}
        }});
      }})();
      </script>
    </div>
  </div>
</div>
"""
        body = f"""
{self._account_lk_nav()}
<div class='card'>
  <h2>Профиль подключения</h2>
  <p><b>Название:</b> <code>{esc(label)}</code></p>
  <p><b>Статус:</b> <code>{esc(_account_profile_status_label((runtime_item or {}).get('status') or profile.status or 'pending'))}</code></p>
  <p><b>IP:</b> <code>{esc(allowed_ip or '—')}</code></p>
  <p><b>Точка подключения:</b> <code>{esc(_public_gateway_label(gateway) if gateway else '—')}</code></p>
  <p><b>Выход в интернет:</b> <code>{esc(_public_uplink_label(uplink) if uplink else '—')}</code></p>
  <p><b>Последняя активность:</b> <code>{esc(last_hs or '—')}</code></p>
  <p class='actions'>
    <a class='btn' href='/account/'>Назад в аккаунт</a>
    {f"<a class='btn' href='/new/{quote(profile.legacy_peer_id, safe='')}/'>Открыть страницу выдачи</a>" if profile.legacy_peer_id else ""}
  </p>
  {f"<form method='post' action='/account/profiles/{quote(profile.connection_profile_id, safe='')}/remove' class='actions'><button class='btn' type='submit'>Удалить профиль</button></form>" if profile.legacy_peer_id else ""}
  <details class='small'>
    <summary>Технические данные</summary>
    <p><b>ID профиля:</b> <code>{esc(profile.connection_profile_id)}</code></p>
    <p><b>ID peer:</b> <code>{esc(profile.legacy_peer_id or '—')}</code></p>
  </details>
</div>
{issued_block}
"""
        return self._send_html(page("Профиль подключения", body))

    def _render_account_claim_new(self):
        if self._account_lk_current_account() is None:
            return self._redirect("/account/login", code=302)
        current_claim = self._account_lk_current_claim_candidate()
        quick_attach_html = ""
        if current_claim:
            quick_attach_html = f"""
<div class='card'>
  <h3>Быстрое подключение текущего профиля</h3>
  <p><b>{esc(current_claim.get('label') or current_claim.get('peer_id') or '')}</b></p>
  <p class='small'>IP: <code>{esc(current_claim.get('allowed_ip') or '')}</code></p>
  <form method='post' action='/account/claims/new' class='actions'>
    <input type='hidden' name='legacy_peer_id' value='{esc(current_claim.get('peer_id') or '')}'>
    <input type='hidden' name='display_label' value='{esc(current_claim.get('label') or '')}'>
    <input type='hidden' name='claim_method' value='wg_session_attach'>
    <button class='btn' type='submit'>Подключить текущий профиль</button>
  </form>
</div>
"""
        body = f"""
{self._account_lk_nav()}
{quick_attach_html}
<div class='card'>
<h2>Подключить существующий профиль</h2>
<p class='small'>Если профиль был создан раньше, его можно привязать к аккаунту и дальше управлять им уже отсюда.</p>
<form method='post' action='/account/claims/new'>
  <label>ID профиля подключения:<br><input name='legacy_peer_id' placeholder='p123...'></label><br><br>
  <label>Понятное название в аккаунте:<br><input name='display_label' placeholder='Старый iPhone профиль'></label><br><br>
  <button class='btn' type='submit'>Привязать профиль</button>
</form>
</div>
"""
        return self._send_html(page("Подключить профиль", body))

    def _is_peer_active_now(self, item, now_unix):
        status = str(item.get("status") or "")
        if status in ("removed", "expired", "blocked"):
            return False
        hs_unix = int(item.get("last_handshake_unix") or 0)
        return hs_unix > 0 and (now_unix - hs_unix) <= ACTIVE_WINDOW_SEC

    def _build_live_data(self, items, scope="all"):
        global LIVE_PREV

        now_unix = int(dt.datetime.now(dt.timezone.utc).timestamp())
        endpoint_by_pub, endpoint_by_allowed_ip = _wg_endpoint_maps()
        latest_peer_ip = _latest_peer_real_ip_map()
        latest_label_map = _latest_peer_label_map()
        uplink_map = {}
        if WG_PORTAL_UPLINK_ENABLED:
            out = backend_list_uplinks()
            if out.get("ok"):
                uplink_map = _uplink_map_from_out(items, out)
        prev_ts = int((LIVE_PREV or {}).get("ts") or 0)
        prev_peers = (LIVE_PREV or {}).get("peers") or {}
        if not isinstance(prev_peers, dict):
            prev_peers = {}
        dt_sec = (now_unix - prev_ts) if prev_ts > 0 else 0

        scope = str(scope or "all").strip().lower()
        scoped_items = _filter_items_by_scope(items, scope)
        for x in scoped_items:
            _apply_admin_meta_to_item(x, latest_label_map=latest_label_map)
        scoped_peer_ids = {
            str(x.get("id") or "").strip()
            for x in scoped_items
            if str(x.get("id") or "").strip()
        }

        online = []
        curr_peers = {}
        for x in scoped_items:
            peer_id = str(x.get("id") or "").strip()
            peer_pub = str(x.get("public_key") or "").strip()
            rx = int(x.get("rx_bytes") or 0)
            tx = int(x.get("tx_bytes") or 0)
            curr_peers[peer_id] = {"rx": rx, "tx": tx}

            ip_short = str(x.get("allowed_ip") or "").split("/", 1)[0]
            endpoint_ip = str(x.get("real_ip") or "").strip()
            if peer_pub:
                endpoint_ip = endpoint_ip or endpoint_by_pub.get(peer_pub, "")
            if (not endpoint_ip) and ip_short:
                endpoint_ip = endpoint_by_allowed_ip.get(ip_short, "")
            if (not endpoint_ip):
                endpoint_ip = str(latest_peer_ip.get(peer_id) or "")
            endpoint_geo = _geo_for_ip(endpoint_ip) if endpoint_ip not in ("", "-") else ""
            label_eff = str(x.get("admin_label") or x.get("label") or "")

            hs_unix = int(x.get("last_handshake_unix") or 0)
            hs_age = (now_unix - hs_unix) if hs_unix > 0 else None
            status = str(x.get("status") or "")
            if status in ("removed", "expired", "blocked"):
                continue
            if hs_unix <= 0 or hs_age is None or hs_age > ADMIN_LIVE_WINDOW_SEC:
                continue

            rx_rate = None
            tx_rate = None
            prev = prev_peers.get(peer_id)
            if dt_sec > 0 and isinstance(prev, dict):
                prev_rx = int(prev.get("rx") or 0)
                prev_tx = int(prev.get("tx") or 0)
                rx_rate = max(0, rx - prev_rx) / float(dt_sec)
                tx_rate = max(0, tx - prev_tx) / float(dt_sec)

            online.append(
                {
                    "id": peer_id,
                    "label": label_eff,
                    "ip": ip_short,
                    "endpoint_ip": endpoint_ip,
                    "endpoint_geo": endpoint_geo,
                    "uplink": uplink_map.get(peer_id, "ams") if WG_PORTAL_UPLINK_ENABLED else "",
                    "status": status,
                    "last_handshake_at": str(x.get("last_handshake_at") or ""),
                    "handshake_age_sec": hs_age,
                    "rx_bytes": rx,
                    "tx_bytes": tx,
                    "rx_rate_bps": rx_rate,
                    "tx_rate_bps": tx_rate,
                }
            )

        online.sort(key=lambda z: int(z.get("handshake_age_sec") or 10**9))
        LIVE_PREV = {"ts": now_unix, "peers": curr_peers}

        events = []
        interesting = {
            "new_create_ok",
            "lk_login_ok",
            "admin_action_ok",
            "admin_action_error",
            "reissue_ok",
            "reissue_finalize_ok",
            "cleanup_remove",
            "api_create",
            "api_block",
            "api_remove",
            "api_reissue",
        }
        events_data = backend_list_events(limit=20, event_types=sorted(interesting))
        source_events = list(events_data.get("items", [])) if events_data.get("ok") else []
        for e in source_events:
            ev = str(e.get("event") or "")
            if ev not in interesting:
                continue
            peer_id = str(e.get("peer_id") or "").strip()
            if scope != "all" and peer_id and peer_id not in scoped_peer_ids:
                continue
            meta = _get_admin_meta(peer_id, latest_label_map.get(peer_id) or str(e.get("label") or ""))
            events.append(
                {
                    "ts": fmt_dt_ui(e.get("ts") or ""),
                    "event": ev,
                    "peer_id": peer_id,
                    "label": str(e.get("label") or ""),
                    "admin_label": str(meta.get("admin_label") or str(e.get("label") or "")),
                    "ip": str(e.get("ip") or ""),
                    "action": str(e.get("action") or ""),
                    "ok": e.get("ok"),
                }
            )
            if len(events) >= 20:
                break

        return {
            "ok": True,
            "ts": dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
            "poll_sec": ADMIN_LIVE_POLL_SEC,
            "active_window_sec": ACTIVE_WINDOW_SEC,
            "online_window_sec": ADMIN_LIVE_WINDOW_SEC,
            "online_count": len(online),
            "online": online,
            "events": events,
        }

    def _admin_auth(self, parsed):
        qs = parse_qs(parsed.query)
        token_q = (qs.get("token") or [""])[0].strip()
        token_c = self._cookies().get("wg_admin_token", "").strip()
        if not ADMIN_TOKEN:
            self._audit("admin_login_attempt", via="none", ok=False, reason="admin_token_not_configured")
            self._notify_admin_login(ok=False, via="none", reason="admin_token_not_configured")
            return False, None, "ADMIN token не настроен"
        if token_q and token_q == ADMIN_TOKEN:
            self._audit("admin_login_attempt", via="query", ok=True)
            self._notify_admin_login(ok=True, via="query", reason="token_ok")
            return True, [(
                "Set-Cookie",
                f"wg_admin_token={ADMIN_TOKEN}; Path=/; Max-Age=2592000; SameSite=Strict; Secure; HttpOnly",
            )], ""
        if token_q:
            self._audit("admin_login_attempt", via="query", ok=False, reason="bad_token")
            self._notify_admin_login(ok=False, via="query", reason="bad_token")
            return False, None, "нужен token"
        if token_c and token_c == ADMIN_TOKEN:
            return True, None, ""
        if token_c:
            self._audit("admin_login_attempt", via="cookie", ok=False, reason="bad_cookie")
            self._notify_admin_login(ok=False, via="cookie", reason="bad_cookie")
            return False, None, "нужен token"
        self._audit("admin_login_attempt", via="none", ok=False, reason="missing_token")
        self._notify_admin_login(ok=False, via="none", reason="missing_token")
        return False, None, "нужен token"

    def _admin_read_mode(self, parsed):
        mode = "local"
        headers = []
        if not ADMIN_READ_CANARY_ENABLED:
            return mode, headers
        qs = parse_qs(parsed.query)
        mode_q = str((qs.get("read_mode") or [""])[0] or "").strip().lower()
        mode_c = self._cookies().get("wg_admin_read_mode", "").strip().lower()
        if mode_q in ("local", "shadow"):
            mode = mode_q
            headers.append((
                "Set-Cookie",
                f"wg_admin_read_mode={mode}; Path=/; Max-Age=2592000; SameSite=Strict; Secure; HttpOnly",
            ))
        elif mode_c in ("local", "shadow"):
            mode = mode_c
        return mode, headers

    def _admin_data_scope(self, parsed):
        scope = ADMIN_DATA_SCOPE_DEFAULT
        headers = []
        if not ADMIN_READ_CANARY_ENABLED:
            return scope, headers
        qs = parse_qs(parsed.query)
        scope_q = str((qs.get("scope") or [""])[0] or "").strip().lower()
        scope_c = self._cookies().get("wg_admin_data_scope", "").strip().lower()
        if scope_q in SUPPORTED_SCOPES:
            scope = scope_q
            headers.append((
                "Set-Cookie",
                f"wg_admin_data_scope={scope}; Path=/; Max-Age=2592000; SameSite=Strict; Secure; HttpOnly",
            ))
        elif scope_c in SUPPORTED_SCOPES:
            scope = scope_c
        else:
            legacy_mode_q = str((qs.get("read_mode") or [""])[0] or "").strip().lower()
            legacy_mode_c = self._cookies().get("wg_admin_read_mode", "").strip().lower()
            legacy_mode = legacy_mode_q if legacy_mode_q in ("local", "shadow") else legacy_mode_c
            if legacy_mode == "local":
                scope = "edg"
            elif legacy_mode == "shadow":
                scope = "all"
        return scope, headers

    def _scope_to_read_mode(self, scope):
        return "local" if str(scope or "all").strip().lower() == "edg" else "shadow"

    def _admin_scope_label(self, scope):
        key = str(scope or "").strip().lower()
        if key == "vrn":
            return "Only VRN"
        if key == "edg":
            return "Only EDG"
        if key == "msk_d":
            return "Only MSK_D"
        return "All edges"

    def _find_peer_by_id(self, items, peer_id):
        pid = str(peer_id or "").strip()
        if not pid:
            return None
        for item in list(items or []):
            if str(item.get("id") or "").strip() == pid:
                return item
        return None

    def _find_peer_by_allowed_ip(self, items, ip_raw, now_unix, require_active=True):
        ip = str(ip_raw or "").strip()
        if not ip:
            return None
        matches = []
        for item in list(items or []):
            allowed_ip = str(item.get("allowed_ip") or "").split("/", 1)[0].strip()
            if allowed_ip != ip:
                continue
            if require_active and not self._is_peer_active_now(item, now_unix):
                continue
            matches.append(item)
        if len(matches) == 1:
            return matches[0]
        return None

    def _find_peer_by_real_ip(self, items, ip_raw, now_unix, require_active=True):
        ip = str(ip_raw or "").strip()
        if not ip or ip in ("0.0.0.0", "127.0.0.1", "::1"):
            return None
        latest_peer_ip = _latest_peer_real_ip_map()
        matches = []
        for item in list(items or []):
            if require_active and not self._is_peer_active_now(item, now_unix):
                continue
            peer_id = str(item.get("id") or "").strip()
            real_ip = str(item.get("real_ip") or "").strip()
            if not real_ip and peer_id:
                real_ip = str(latest_peer_ip.get(peer_id) or "").strip()
            if real_ip != ip:
                continue
            matches.append(item)
        if len(matches) == 1:
            return matches[0]
        return None

    def _resolve_lk_access(self, data, peer_id_from_query="", lk_token_from_query="", require_active=True):
        items = list((data or {}).get("items", []) or [])
        cookies = self._cookies()
        now_unix = int(dt.datetime.now(dt.timezone.utc).timestamp())
        headers = []
        client_ip_candidates = self._client_ip_candidates()
        client_ip = client_ip_candidates[0]

        wg_item = None
        for cand_ip in client_ip_candidates:
            wg_item = self._find_peer_by_allowed_ip(items, cand_ip, now_unix, require_active=require_active)
            if wg_item:
                break
        if not wg_item:
            for cand_ip in client_ip_candidates:
                wg_item = self._find_peer_by_real_ip(items, cand_ip, now_unix, require_active=require_active)
                if wg_item:
                    client_ip = cand_ip
                    break
        if wg_item:
            peer_id = str(wg_item.get("id") or "").strip()
            lk_expected = str(wg_item.get("lk_token") or "").strip()
            headers.extend([
                ("Set-Cookie", f"wg_peer_id={peer_id}; Path=/; Max-Age=31536000; SameSite=Lax; Secure; HttpOnly"),
                ("Set-Cookie", "wg_lk_ui=1; Path=/; Max-Age=31536000; SameSite=Lax; Secure"),
            ])
            if lk_expected:
                headers.append(("Set-Cookie", f"wg_lk_token={lk_expected}; Path=/; Max-Age=31536000; SameSite=Lax; Secure; HttpOnly"))
            return {
                "ok": True,
                "item": wg_item,
                "peer_id": peer_id,
                "headers": headers,
                "via": "wg",
                "client_ip": client_ip,
            }

        peer_id = (peer_id_from_query or cookies.get("wg_peer_id") or "").strip()
        lk_token = (lk_token_from_query or cookies.get("wg_lk_token") or "").strip()
        if not peer_id:
            return {
                "ok": False,
                "reason": "wg_peer_not_detected",
                "headers": headers,
                "via": "none",
                "client_ip": client_ip,
            }

        item = self._find_peer_by_id(items, peer_id)
        if peer_id_from_query and item:
            headers.append(("Set-Cookie", f"wg_peer_id={peer_id}; Path=/; Max-Age=31536000; SameSite=Lax; Secure; HttpOnly"))
        if not item:
            return {
                "ok": False,
                "reason": "peer_not_found",
                "headers": headers,
                "peer_id": peer_id,
                "via": "cookie",
                "client_ip": client_ip,
            }

        expected = str(item.get("lk_token") or "").strip()
        if expected and lk_token_from_query and lk_token_from_query == expected:
            headers.append(("Set-Cookie", f"wg_lk_token={expected}; Path=/; Max-Age=31536000; SameSite=Lax; Secure; HttpOnly"))
        if expected and lk_token != expected:
            return {
                "ok": False,
                "reason": "bad_lk_token",
                "headers": headers,
                "peer_id": peer_id,
                "item": item,
                "via": "cookie",
                "client_ip": client_ip,
            }
        if require_active and not self._is_peer_active_now(item, now_unix):
            return {
                "ok": False,
                "reason": "wg_not_active",
                "headers": headers,
                "peer_id": peer_id,
                "item": item,
                "via": "cookie",
                "client_ip": client_ip,
            }
        headers.append(("Set-Cookie", "wg_lk_ui=1; Path=/; Max-Age=31536000; SameSite=Lax; Secure"))
        return {
            "ok": True,
            "item": item,
            "peer_id": peer_id,
            "headers": headers,
            "via": "cookie",
            "client_ip": client_ip,
        }
    def _render_home(self):
        body = f"""
<section id='hero' class='card hero'>
  <div class='hero-wrap'>
    <div class='hero-copy'>
      <p class='kicker'>SECURE TUNNEL PLATFORM</p>
      <h2>Защищенное соединение<br/>для безопасной работы</h2>
      <p class='hero-lead'>Криптографическое шифрование и туннельное подключение защищают трафик в публичных и мобильных сетях.</p>
      <div class='hero-cta'>
        <a class='btn primary' href='/account/profiles/new'>Создать профиль</a>
        <a class='btn ghost' href='#security'>Узнать подробнее</a>
      </div>
    </div>
    <div class='hero-visual' aria-hidden='true'></div>
  </div>
</section>

<section id='features' class='card section'>
  <p class='section-kicker'>WORLD'S BEST TUNNEL</p>
  <h2>Почему выбирают нас?</h2>
  <div class='features'>
    <article class='feature'>
      <h3>🛡️ Безопасность на уровне крепости</h3>
      <p>Шифрование и туннелирование защищают данные от перехвата в публичных и мобильных сетях.</p>
    </article>
    <article class='feature'>
      <h3>⚡ Высокая скорость передачи</h3>
      <p>Оптимизированная маршрутизация сохраняет комфортную скорость для работы и звонков.</p>
    </article>
    <article class='feature'>
      <h3>🌍 Стабильность соединения</h3>
      <p>Сервис остается доступным даже при нестабильном доступе в интернет.</p>
    </article>
    <article class='feature'>
      <h3>🔒 Конфиденциальность трафика</h3>
      <p>Трафик проходит через защищенный туннель с прозрачными правилами приватности.</p>
    </article>
  </div>
</section>

<section id='security' class='card section split'>
  <div class='security-illustration' aria-hidden='true'></div>
  <div>
    <p class='section-kicker'>ABOUT JsTun</p>
    <h2>Ваш щит в цифровом мире</h2>
    <p class='lead'>Портал помогает быстро получить рабочий WireGuard-профиль с акцентом на безопасность, стабильность и простое подключение.</p>
    <ul class='checklist'>
      <li>Шифрование и туннелирование трафика.</li>
      <li>Оптимизированные маршруты и низкая задержка.</li>
      <li>Работа на Windows, macOS, Linux, iOS и Android.</li>
    </ul>
  </div>
</section>

<section class='card section split reverse'>
  <div class='media-card'>
    <div class='media-image' aria-hidden='true'></div>
    <div class='media-overlay'>
      <h3>Будущее сетевой безопасности уже сейчас</h3>
      <p>Устойчивый защищенный канал и предсказуемая производительность даже на слабых сетях.</p>
    </div>
  </div>
  <div>
    <p class='section-kicker'>PERFORMANCE</p>
    <h2>Контроль скорости и стабильности</h2>
    <p class='lead'>Мониторинг канала и интеллектуальные маршруты поддерживают доступность сервиса даже в нестабильных сетях.</p>
    <div class='meter'>
      <div class='meter-row'><span>Скоростной туннель</span><b>95%</b></div>
      <div class='meter-bar'><i style='width:95%'></i></div>
    </div>
    <div class='meter'>
      <div class='meter-row'><span>Стабильность маршрута</span><b>88%</b></div>
      <div class='meter-bar'><i style='width:88%'></i></div>
    </div>
    <div class='meter'>
      <div class='meter-row'><span>Защита конфиденциальности</span><b>99%</b></div>
      <div class='meter-bar'><i style='width:99%'></i></div>
    </div>
  </div>
</section>

<section class='card section'>
  <h2>Подключить просто!</h2>
  <div class='steps steps-quick'>
    <div class='step'>
      <span class='step-num'>1</span>
      <p>Авторизуйся или зарегистрируйся на портале</p>
    </div>
    <div class='step'>
      <span class='step-num'>2</span>
      <p>Создай <a href='/account/profiles/new'>профиль подключения</a><br/>и получи конфиг</p>
    </div>
    <div class='step'>
      <span class='step-num'>3</span>
      <p>Пропиши его в WireGuard<br/>и пользуйся защитой!</p>
    </div>
  </div>
</section>

<section class='card section'>
  <h2>Скачать WireGuard</h2>
  <div class='install-grid'>
    <div class='install-card'><p><a class='btn os' href='{INSTALL_LINKS['windows']}' target='_blank' rel='noopener'><svg aria-hidden='true' class='os-ico' viewBox='0 0 256 256'><path d='M112,144v51.63672a7.9983,7.9983,0,0,1-9.43115,7.87061l-64-11.63623A8.00019,8.00019,0,0,1,32,184V144a8.00008,8.00008,0,0,1,8-8h64A8.00008,8.00008,0,0,1,112,144ZM109.126,54.2217a7.995,7.995,0,0,0-6.55713-1.729l-64,11.63623A8.00017,8.00017,0,0,0,32,72v40a8.00008,8.00008,0,0,0,8,8h64a8.00008,8.00008,0,0,0,8-8V60.3633A7.99853,7.99853,0,0,0,109.126,54.2217Zm112-20.36377a7.99714,7.99714,0,0,0-6.55713-1.729l-80,14.5459A7.99965,7.99965,0,0,0,128,54.54543V112a8.00008,8.00008,0,0,0,8,8h80a8.00008,8.00008,0,0,0,8-8V40A8.00028,8.00028,0,0,0,221.126,33.85793ZM216,136H136a8.00008,8.00008,0,0,0-8,8v57.45459a7.99967,7.99967,0,0,0,6.56885,7.87061l80,14.5459A8.0001,8.0001,0,0,0,224,216V144A8.00008,8.00008,0,0,0,216,136Z'/></svg>Windows</a></p></div>
    <div class='install-card'><p><a class='btn os' href='{INSTALL_LINKS['macos']}' target='_blank' rel='noopener'><svg aria-hidden='true' class='os-ico' viewBox='0 0 384 512' xmlns='http://www.w3.org/2000/svg'><path d='M318.7 268.7c-.2-36.7 16.4-64.4 50-84.8-18.8-26.9-47.2-41.7-84.7-44.6-35.5-2.8-74.3 20.7-88.5 20.7-15 0-49.4-19.7-76.4-19.7C63.3 141.2 4 184.8 4 273.5q0 39.3 14.4 81.2c12.8 36.7 59 126.7 107.2 125.2 25.2-.6 43-17.9 75.8-17.9 31.8 0 48.3 17.9 76.4 17.9 48.6-.7 90.4-82.5 102.6-119.3-65.2-30.7-61.7-90-61.7-91.9zm-56.6-164.2c27.3-32.4 24.8-61.9 24-72.5-24.1 1.4-52 16.4-67.9 34.9-17.5 19.8-27.8 44.3-25.6 71.9 26.1 2 49.9-11.4 69.5-34.3z'></path></svg>macOS</a></p></div>
    <div class='install-card'><p><a class='btn os' href='{INSTALL_LINKS['android']}' target='_blank' rel='noopener'><svg aria-hidden='true' class='os-ico' viewBox='0 0 512 512' xmlns='http://www.w3.org/2000/svg'><path d='M325.3 234.3L104.6 13l280.8 161.2-60.1 60.1zM47 0C34 6.8 25.3 19.2 25.3 35.3v441.3c0 16.1 8.7 28.5 21.7 35.3l256.6-256L47 0zm425.2 225.6l-58.9-34.1-65.7 64.5 65.7 64.5 60.1-34.1c18-14.3 18-46.5-1.2-60.8zM104.6 499l280.8-161.2-60.1-60.1L104.6 499z'></path></svg>Android</a></p></div>
    <div class='install-card'><p><a class='btn os' href='{INSTALL_LINKS['ios']}' target='_blank' rel='noopener'><svg aria-hidden='true' class='os-ico' viewBox='0 0 384 512' xmlns='http://www.w3.org/2000/svg'><path d='M318.7 268.7c-.2-36.7 16.4-64.4 50-84.8-18.8-26.9-47.2-41.7-84.7-44.6-35.5-2.8-74.3 20.7-88.5 20.7-15 0-49.4-19.7-76.4-19.7C63.3 141.2 4 184.8 4 273.5q0 39.3 14.4 81.2c12.8 36.7 59 126.7 107.2 125.2 25.2-.6 43-17.9 75.8-17.9 31.8 0 48.3 17.9 76.4 17.9 48.6-.7 90.4-82.5 102.6-119.3-65.2-30.7-61.7-90-61.7-91.9zm-56.6-164.2c27.3-32.4 24.8-61.9 24-72.5-24.1 1.4-52 16.4-67.9 34.9-17.5 19.8-27.8 44.3-25.6 71.9 26.1 2 49.9-11.4 69.5-34.3z'></path></svg>iOS</a></p></div>
    <div class='install-card'><p><a class='btn os' href='https://launchpad.net/ubuntu/+source/wireguard-linux-compat' target='_blank' rel='noopener'><svg aria-hidden='true' class='os-ico' viewBox='0 0 16 16' xmlns='http://www.w3.org/2000/svg'><path d='M3.26 8A1.37 1.37 0 1 1 .52 8a1.37 1.37 0 0 1 2.74 0zm7.79 6.66a1.37 1.37 0 1 0 2.374-1.37 1.37 1.37 0 0 0-2.374 1.37zm2.37-11.95a1.37 1.37 0 1 0-2.37-1.373 1.37 1.37 0 0 0 2.37 1.373zM8.79 4.1a3.9 3.9 0 0 1 3.89 3.55h2a5.93 5.93 0 0 0-1.73-3.8 1.91 1.91 0 0 1-1.66-.12 2.001 2.001 0 0 1-.94-1.38 6 6 0 0 0-1.54-.2 5.83 5.83 0 0 0-2.61.61l1 1.73a3.94 3.94 0 0 1 1.59-.39zM4.88 8a3.93 3.93 0 0 1 1.66-3.2l-1-1.7A5.93 5.93 0 0 0 3.1 6.5a1.92 1.92 0 0 1 0 3 5.93 5.93 0 0 0 2.42 3.4l1-1.7A3.93 3.93 0 0 1 4.88 8zm3.91 3.91a4 4 0 0 1-1.65-.37l-1 1.73c.81.403 1.704.612 2.61.61.52 0 1.038-.067 1.54-.2a2 2 0 0 1 .94-1.38 1.911 1.911 0 0 1 1.66-.12 5.93 5.93 0 0 0 1.73-3.8h-2a3.91 3.91 0 0 1-3.83 3.53z'></path></svg>Ubuntu</a></p></div>
  </div>
  <p class='small'>Ссылки взяты из официальной страницы установки <a href='https://www.wireguard.com/install/' target='_blank' rel='noopener'>WireGuard</a></p>
</section>

<section class='card section feedback'>
  <h2>Отзывы клиентов</h2>
  <div class='testimonials'>
    <article class='quote'>
      <div class='profile-image'><img loading='lazy' src='/assets/img/vendor/testimonial_002.jpg' alt='Анна'></div>
      <p>«Подключение стабильное, даже в роуминге. Главное, минимум ручной настройки»</p>
      <p><strong>Анна, дизайнер</strong></p>
    </article>
    <article class='quote'>
      <div class='profile-image'><img loading='lazy' src='/assets/img/vendor/happy-man-posing.webp' alt='Максим'></div>
      <p>«Использую для рабочих сервисов и командировок. Скорость держится отлично»</p>
      <p><strong>Максим, фрилансер</strong></p>
    </article>
    <article class='quote'>
      <div class='profile-image'><img loading='lazy' src='/assets/img/vendor/happy-with-phone.webp' alt='Олег'></div>
      <p>«Удобный старт для новых устройств, конфиг и QR в один шаг»</p>
      <p><strong>Олег, IT-специалист</strong></p>
    </article>
    <article class='quote'>
      <div class='profile-image'><img loading='lazy' src='/assets/img/vendor/testimonial_004.jpg' alt='Мария'></div>
      <p>«Система идеально вписалась в нашу инфраструктуру и соответствует требованиям»</p>
      <p><strong>Мария, IT-директор</strong></p>
    </article>
  </div>
</section>
"""
        return self._send_html(page("JsTun", body, card_h2_reset=False))


    def _render_help(self):
        body = f"""
<div class='card'>
<h2>Помощь: как подключиться</h2>
<p>Этот портал выдает конфиг WireGuard и позволяет проверить, что подключение реально работает.</p>
</div>

<div class='card'>
<h3>Шаги</h3>
<ol>
  <li>Авторизуйтесь или зарегистрируйтесь на портале через <a href='/account/login'>личный кабинет</a>.</li>
  <li>Создайте профиль подключения на странице <a href='/account/profiles/new'>нового личного кабинета</a>.</li>
  <li>Установите приложение WireGuard, если оно ещё не установлено (официальные ссылки: <a href='{INSTALL_LINKS['more']}' target='_blank' rel='noopener'>wireguard.com/install</a>).</li>
  <li>Добавьте профиль в WireGuard:
    <ul>
      <li>Телефон: обычно удобнее отсканировать QR.</li>
      <li>ПК: используйте “Скачать конфиг” (ZIP) или “Скопировать конфиг”.</li>
    </ul>
  </li>
  <li>Включите туннель в WireGuard.</li>
  <li>Откройте <a href='/check/'>/check/</a> и убедитесь, что подключение активно.</li>
  <li>После этого профиль и его статус будут доступны в <a href='/account/'>личном кабинете</a>.</li>
 </ol>
</div>

<div class='card'>
<h3>Чеклист “подключилось ли”</h3>
<ul>
  <li>На <a href='/check/'>/check/</a> должен автоматически определиться ваш профиль и быть виден свежий <code>Last handshake</code>.</li>
  <li>Если handshake не обновляется: проверьте, что включен правильный профиль WireGuard и он “активен”.</li>
  <li>Если ЛК не открывается: сначала проверьте, что WireGuard действительно активен. <a href='/recover/'>/recover/</a> используйте только как аварийный fallback.</li>
</ul>
</div>

{back_home_link()}
"""
        return self._send_html(page("WG Help", body))

    def _render_new_get(self):
        body = f"""
<div class=card>
<h2>Новый профиль подключения</h2>
<form method='post' action='/new/' id='new-profile-form'>
<div class='d-flex'>
<label>Название профиля: <input id='new-profile-label' name='label' placeholder='Например, iPhone или MacBook' maxlength='64'></label>
<span class='form-error' id='new-profile-error' role='alert'>Заполните название профиля.</span>
<label>Точка подключения:
  <select name='gateway' id='new-profile-gateway'>
    {_edge_options_html('edg', public_labels=True)}
  </select>
</label>
<label>Выход в интернет:
  <select name='uplink' id='new-profile-uplink'>
    <option value='ams' data-edg='По умолчанию' data-vrn='По умолчанию' data-msk_d='По умолчанию'>По умолчанию</option>
    <option value='nyc' data-edg='США' data-vrn='США' data-msk_d='США'>США</option>
    <option value='fra' data-edg='Европа' data-vrn='Европа' data-msk_d='Европа'>Европа</option>
  </select>
</label>
<span class='small' id='new-profile-routing-note'>Доступные варианты выхода: по умолчанию, Европа и США.</span>
<button class='btn' id='new-profile-submit' type='submit'>Создать</button>
<span class='new-form-loader' id='new-profile-loader'>Создаю профиль, подождите...</span>
</div>
</form>
<script>
(() => {{
  const form = document.getElementById('new-profile-form');
  const btn = document.getElementById('new-profile-submit');
  const loader = document.getElementById('new-profile-loader');
  const loginInput = document.getElementById('new-profile-label');
  const gatewaySelect = document.getElementById('new-profile-gateway');
  const uplinkSelect = document.getElementById('new-profile-uplink');
  const routingNote = document.getElementById('new-profile-routing-note');
  const error = document.getElementById('new-profile-error');
  if (!form || !btn || !loginInput) return;

  const clearError = () => {{
    loginInput.classList.remove('field-error');
    if (error) error.style.display = 'none';
  }};

  loginInput.addEventListener('input', clearError);

  const syncGatewayOptions = () => {{
    if (!gatewaySelect || !uplinkSelect) return;
    const gateway = gatewaySelect.value || 'edg';
    for (const option of Array.from(uplinkSelect.options)) {{
      const label = option.dataset[gateway] || option.textContent;
      option.textContent = label;
    }}
    if (routingNote) {{
      routingNote.textContent = 'Доступные варианты выхода: по умолчанию, Европа и США.';
    }}
  }};

  if (gatewaySelect) {{
    gatewaySelect.addEventListener('change', syncGatewayOptions);
    syncGatewayOptions();
  }}

  form.addEventListener('submit', (e) => {{
    const login = (loginInput.value || '').trim();
    if (!login) {{
      e.preventDefault();
      loginInput.classList.add('field-error');
      if (error) error.style.display = 'block';
      loginInput.focus();
      return;
    }}
    if (btn.disabled) {{
      e.preventDefault();
      return;
    }}
    clearError();
    btn.disabled = true;
    btn.textContent = 'Создаю...';
    if (loader) loader.style.display = 'inline-flex';
  }});
}})();
</script>
<p class='small'>После создания покажем QR, а конфиг можно скопировать или скачать.</p>
</div>
{back_home_link()}
"""
        return self._send_html(page("Новый профиль", body))

    def _admin_nav(self, active):
        tabs = [
            ("/admin/", "Сводка", "dashboard"),
            ("/admin/peers/", "Клиенты", "peers"),
            ("/admin/edges/", "Edges", "edges"),
            ("/admin/waves/", "Waves", "waves"),
            ("/admin/uplinks/", "Аплинки", "uplinks"),
            ("/admin/events/", "События", "events"),
            ("/admin/live/", "Live-мониторинг", "live"),
        ]
        links = []
        for href, label, key in tabs:
            if key == active:
                links.append(f"<a class='btn sm disabled' aria-disabled='true'><b>{esc(label)}</b></a>")
            else:
                links.append(f"<a class='btn sm' href='{href}'>{esc(label)}</a>")
        canary_html = ""
        if ADMIN_READ_CANARY_ENABLED:
            scope = str(ADMIN_DATA_SCOPE.get() or "all").strip().lower()
            active_href = next((href for href, _label, key in tabs if key == active), "/admin/")
            scope_links = []
            for scope_key, scope_title in (("vrn", "VRN"), ("edg", "Only EDG"), ("msk_d", "Only MSK_D"), ("all", "All edges")):
                if scope == scope_key:
                    scope_links.append(f"<a class='btn sm disabled' aria-disabled='true'><b>{esc(scope_title)}</b></a>")
                else:
                    scope_links.append(f"<a class='btn sm' href='{active_href}?scope={scope_key}'>{esc(scope_title)}</a>")
            canary_html = (
                "<div class='small'><div class='actions'><span>Data scope:</span>"
                + "".join(scope_links) +
                "</div>"
                "<div class='small'>`VRN` показывает только VRN peers и VRN runtime. `Only EDG` показывает только локальный EDG runtime. `Only MSK_D` показывает только peers/runtime дополнительного шлюза. `All edges` показывает объединённую multi-edge модель.</div>"
                "</div>"
            )
        return "<div class='card'><h3>Разделы админки</h3><p class='actions'>" + "".join(links) + "</p>" + canary_html + "</div>"

    def _render_admin_edges(self, headers=None):
        out = backend_list_edges()
        if not out.get("ok"):
            return self._send_html(page("WG Admin Edges", f"<div class='card'><b>Ошибка edges:</b> {esc(out.get('error'))}</div>"), 500, headers=headers)

        items = list(out.get("items", []))
        items.sort(key=lambda x: str(x.get("edge_id") or ""))

        rows = []
        for row in items:
            uplinks = list(row.get("uplinks", []) or [])
            notru_primary = "-"
            notru_fallback = "-"
            if uplinks:
                primary = next((u for u in uplinks if str(u.get("name") or "").strip().lower() == "ams"), None)
                fallback = next((u for u in uplinks if str(u.get("name") or "").strip().lower() in ("fra", "nyc")), None)
                if primary:
                    notru_primary = str(primary.get("uplink_id") or primary.get("name") or "-")
                if fallback:
                    notru_fallback = str(fallback.get("uplink_id") or fallback.get("name") or "-")
            health = str(row.get("health_status") or "").strip() or "-"
            health_note = str(row.get("health_note") or "").strip()
            rows.append(
                "<tr>"
                f"<td><a href='/admin/edges/{quote(str(row.get('edge_id') or '').strip())}/'><code>{esc(row.get('edge_id') or '-')}</code></a></td>"
                f"<td><code>{esc(row.get('public_host') or '-')}</code></td>"
                f"<td><code>{esc(row.get('client_interface') or '-')}</code></td>"
                f"<td><code>{esc(row.get('clients_active') or 0)}</code></td>"
                f"<td><code>{esc(row.get('ru_egress') or '-')}</code></td>"
                f"<td><code>{esc(notru_primary)}</code></td>"
                f"<td><code>{esc(notru_fallback)}</code></td>"
                f"<td><code>{esc(health)}</code>" + (f"<div class='small'>{esc(health_note)}</div>" if health_note else "") + "</td>"
                f"<td><a class='btn sm' href='/admin/edges/{quote(str(row.get('edge_id') or '').strip())}/'>Open</a></td>"
                "</tr>"
            )
        table = "".join(rows) or "<tr><td colspan='9'>Пусто</td></tr>"

        body = f"""
{self._admin_nav("edges")}
<div class='card'>
<h2>Админ: edges</h2>
<p class='small'>Первая версия страницы `edges` показывает ingress-edge inventory и runtime state. Actions для `drain` и `enable new peers` пока сознательно не включены.</p>
<div style='overflow:auto'>
<table>
<tr><th>Edge</th><th>Public IP</th><th>Client iface</th><th>Clients active</th><th>RU egress</th><th>NotRU primary</th><th>NotRU fallback</th><th>Health</th><th>Actions</th></tr>
{table}
</table>
</div>
</div>
{back_home_link()}
"""
        return self._send_html(page("WG Admin Edges", body), headers=headers)

    def _render_admin_waves(self, headers=None):
        state = _read_migration_state()
        parsed = urlparse(self.path)
        q = parse_qs(parsed.query or "", keep_blank_values=True)
        selected_wave = str((q.get("wave") or [""])[0] or "").strip()
        selected_queue = str((q.get("queue") or ["all"])[0] or "all").strip().lower()
        if selected_queue not in ("all", "rollback_candidates", "rollback_ready", "rollback_finalized"):
            selected_queue = "all"
        peers_out = backend_list()
        peer_map = {}
        if peers_out.get("ok"):
            for item in list(peers_out.get("items", []) or []):
                pid = str(item.get("id") or "").strip()
                if pid:
                    _apply_admin_meta_to_item(item, latest_label_map=_latest_peer_label_map())
                    peer_map[pid] = item

        def _queue_name(item_row):
            checklist = dict(item_row.get("checklist") or {})
            if checklist.get("rollback_finalized"):
                return "rollback_finalized"
            if checklist.get("rollback_issued") and checklist.get("rollback_validated"):
                return "rollback_ready"
            if (checklist.get("issued") or checklist.get("validated") or checklist.get("old_removed")) and not checklist.get("rollback_issued"):
                return "rollback_candidates"
            return "all"

        def _matches_selected_queue(item_row):
            if selected_queue == "all":
                return True
            return _queue_name(item_row) == selected_queue

        def _render_wave_peer_rows(items):
            rows = []
            for item in items:
                peer_title = str(item.get("admin_label") or item.get("label") or "-")
                peer_comment = str(item.get("admin_comment") or "").strip()
                rows.append(
                    "<tr>"
                    f"<td><a href='/admin/peers/{quote(str(item.get('peer_id') or ''), safe='')}/'><b>{esc(peer_title)}</b></a><div class='small'><code>{esc(item.get('peer_id') or '')}</code></div>" + (f"<div class='small'>{esc(peer_comment)}</div>" if peer_comment else "") + "</td>"
                    f"<td><code>{esc(peer_status_ru(item.get('status') or '-'))}</code></td>"
                    f"<td><code>{esc(item.get('ingress_edge') or '-')}</code></td>"
                    f"<td><code>{esc(item.get('effective_edge') or '-')}</code></td>"
                    f"<td><code>{esc(item.get('effective_uplink') or '-')}</code></td>"
                    f"<td><code>{esc(item.get('queue_name') or '-')}</code></td>"
                    f"<td><code>{esc(item.get('note') or '-')}</code></td>"
                    "</tr>"
                )
            return "".join(rows) or "<tr><td colspan='7'>Пусто</td></tr>"

        def _queue_ids(items, queue_name):
            ids = [str(item.get("peer_id") or "") for item in items if str(item.get("queue_name") or "") == queue_name and str(item.get("peer_id") or "")]
            return "\n".join(ids)

        waves = {}
        for pid, raw in state.items():
            row = _normalize_migration_state(raw)
            wave = str(row.get("wave") or "").strip()
            if not wave:
                continue
            if selected_wave and wave != selected_wave:
                continue
            peer = dict(peer_map.get(pid) or {})
            routing = _peer_routing_view(peer) if peer else {}
            bucket = waves.setdefault(wave, {"count": 0, "issued": 0, "client_imported": 0, "validated": 0, "old_removed": 0, "rollback_issued": 0, "rollback_validated": 0, "rollback_finalized": 0, "rollback_candidates": 0, "rollback_ready": 0, "items": []})
            bucket["count"] += 1
            checklist = dict(row.get("checklist") or {})
            for key in ("issued", "client_imported", "validated", "old_removed", "rollback_issued", "rollback_validated", "rollback_finalized"):
                if checklist.get(key):
                    bucket[key] += 1
            item_row = {
                "peer_id": pid,
                "label": str(peer.get("label") or pid),
                "admin_label": str(peer.get("admin_label") or peer.get("label") or pid),
                "admin_comment": str(peer.get("admin_comment") or ""),
                "status": str(peer.get("status") or "-"),
                "ingress_edge": str(routing.get("ingress_edge") or "-"),
                "effective_edge": str(routing.get("effective_edge") or "-"),
                "effective_uplink": str(routing.get("effective_uplink") or routing.get("preferred_uplink") or "-"),
                "note": str(row.get("note") or ""),
                "checklist": checklist,
            }
            queue_name = _queue_name(item_row)
            item_row["queue_name"] = queue_name
            if queue_name == "rollback_candidates":
                bucket["rollback_candidates"] += 1
            elif queue_name == "rollback_ready":
                bucket["rollback_ready"] += 1
            bucket["items"].append(item_row)
        rows = []
        detail_sections = []
        for wave in sorted(waves):
            bucket = waves[wave]
            rows.append(
                "<tr>"
                f"<td><a href='/admin/waves/?wave={quote(wave, safe='')}'><code>{esc(wave)}</code></a></td>"
                f"<td><code>{esc(bucket.get('count') or 0)}</code></td>"
                f"<td><code>{esc(bucket.get('issued') or 0)}</code></td>"
                f"<td><code>{esc(bucket.get('client_imported') or 0)}</code></td>"
                f"<td><code>{esc(bucket.get('validated') or 0)}</code></td>"
                f"<td><code>{esc(bucket.get('old_removed') or 0)}</code></td>"
                f"<td><code>{esc(bucket.get('rollback_candidates') or 0)}</code></td>"
                f"<td><code>{esc(bucket.get('rollback_ready') or 0)}</code></td>"
                f"<td><code>{esc(bucket.get('rollback_issued') or 0)}</code></td>"
                f"<td><code>{esc(bucket.get('rollback_validated') or 0)}</code></td>"
                f"<td><code>{esc(bucket.get('rollback_finalized') or 0)}</code></td>"
                "</tr>"
            )
            all_items = sorted(bucket.get("items") or [], key=lambda x: (x.get("queue_name") or "", x.get("status") or "", x.get("admin_label") or x.get("label") or "", x.get("peer_id") or ""))
            filtered_items = [item for item in all_items if _matches_selected_queue(item)]
            wave_peer_ids = [str(item.get("peer_id") or "") for item in filtered_items if str(item.get("peer_id") or "")]
            wave_peer_ids_text = "\n".join(wave_peer_ids)
            rollback_candidate_ids = _queue_ids(all_items, "rollback_candidates")
            rollback_ready_ids = _queue_ids(all_items, "rollback_ready")
            rollback_finalized_ids = _queue_ids(all_items, "rollback_finalized")
            detail_sections.append(f"""
<div class='card'>
<h3>Wave: {esc(wave)}</h3>
<p class='actions'>
  <a class='btn sm' href='/admin/waves/?wave={quote(wave, safe="")}'>Open only this wave</a>
  <a class='btn sm' href='/admin/waves/?wave={quote(wave, safe="")}&queue=rollback_candidates'>Rollback candidates</a>
  <a class='btn sm' href='/admin/waves/?wave={quote(wave, safe="")}&queue=rollback_ready'>Rollback ready</a>
  <a class='btn sm' href='/admin/waves/?wave={quote(wave, safe="")}&queue=rollback_finalized'>Rollback finalized</a>
  <a class='btn sm' href='/admin/waves.csv/?wave={quote(wave, safe="")}'>Export this wave CSV</a>
</p>
<div class='card'>
<h4>Recovery policy</h4>
<div class='grid cols-4'>
  <div><b>Default rollback edge:</b> <code>edg</code></div>
  <div><b>Default rollback uplink:</b> <code>ams</code></div>
  <div><b>Current queue filter:</b> <code>{esc(selected_queue)}</code></div>
  <div><b>Filtered peers:</b> <code>{esc(len(filtered_items))}</code></div>
</div>
<p class='small'>Rollback policy is conservative: issue recovery profiles first, validate client import and path health, then mark rollback finalized. Cleanup of temporary rollback peers remains explicit and auditable.</p>
</div>
<div class='grid cols-3'>
  <div><b>Rollback candidates:</b> <code>{esc(bucket.get("rollback_candidates") or 0)}</code></div>
  <div><b>Rollback ready:</b> <code>{esc(bucket.get("rollback_ready") or 0)}</code></div>
  <div><b>Rollback finalized:</b> <code>{esc(bucket.get("rollback_finalized") or 0)}</code></div>
</div>
<div class='grid cols-3'>
  <div><b>Rollback issued:</b> <code>{esc(bucket.get("rollback_issued") or 0)}</code></div>
  <div><b>Rollback validated:</b> <code>{esc(bucket.get("rollback_validated") or 0)}</code></div>
  <div><b>Forward old removed:</b> <code>{esc(bucket.get("old_removed") or 0)}</code></div>
</div>
<form method='post' action='/admin/action/' class='peer-edit' style='margin-top:10px'>
<input type='hidden' name='action' value='batch_wave_rollback_issue'>
<input type='hidden' name='next' value='/admin/waves/?wave={quote(wave, safe="")}'>
<input type='hidden' name='migration_wave' value='{esc(wave)}'>
<label>Gateway:
<select name='gateway'>{_edge_options_html('edg')}</select>
</label>
<label>Uplink:
<select name='uplink'><option value='ams' selected>AMS / DIRECT</option><option value='fra'>FRA</option><option value='nyc'>NYC</option></select>
</label>
<label>Note:
<input name='migration_note' maxlength='2000' value='rollback:{esc(wave)}'>
</label>
<textarea name='peer_ids' rows='5' style='width:100%;font-family:ui-monospace, SFMono-Regular, Menlo, monospace'>{esc(rollback_candidate_ids or wave_peer_ids_text)}</textarea>
<button class='btn sm' type='submit'>Issue rollback profiles</button>
</form>
<form method='post' action='/admin/action/' class='actions' style='margin-top:10px'>
<input type='hidden' name='action' value='batch_wave_set_rollback_checklist'>
<input type='hidden' name='next' value='/admin/waves/?wave={quote(wave, safe="")}'>
<textarea name='peer_ids' rows='5' style='width:100%;font-family:ui-monospace, SFMono-Regular, Menlo, monospace'>{esc(rollback_ready_ids or wave_peer_ids_text)}</textarea>
<label><input type='checkbox' name='migration_rollback_issued' value='1'> rollback issued</label>
<label><input type='checkbox' name='migration_rollback_validated' value='1'> rollback validated</label>
<label><input type='checkbox' name='migration_rollback_finalized' value='1'> rollback finalized</label>
<button class='btn sm' type='submit'>Set rollback status</button>
</form>
<form method='post' action='/admin/action/' class='actions' style='margin-top:10px'>
<input type='hidden' name='action' value='batch_wave_finalize_rollback'>
<input type='hidden' name='next' value='/admin/waves/?wave={quote(wave, safe="")}'>
<textarea name='peer_ids' rows='5' style='width:100%;font-family:ui-monospace, SFMono-Regular, Menlo, monospace'>{esc(rollback_ready_ids or rollback_finalized_ids or wave_peer_ids_text)}</textarea>
<button class='btn sm' type='submit'>Finalize rollback</button>
</form>
<div style='overflow:auto'>
<table>
<tr><th>Peer</th><th>Status</th><th>Ingress</th><th>Effective</th><th>Uplink</th><th>Queue</th><th>Note</th></tr>
{_render_wave_peer_rows(filtered_items)}
</table>
</div>
</div>
""")
        summary_table = "".join(rows) or "<tr><td colspan='6'>Пока нет wave metadata.</td></tr>"
        body = f"""
{self._admin_nav("waves")}
<div class='card'>
<h2>Админ: waves</h2>
<p class='small'>Сводка операторских migration waves. Здесь видно сколько peer-ов уже выдано, импортировано, validated и финализировано по каждой волне.</p>
<p class='actions'>
  <a class='btn sm' href='/admin/waves/'>All waves</a>
  <a class='btn sm' href='/admin/waves/?{"wave=" + quote(selected_wave, safe="") + "&" if selected_wave else ""}queue=rollback_candidates'>Rollback candidates</a>
  <a class='btn sm' href='/admin/waves/?{"wave=" + quote(selected_wave, safe="") + "&" if selected_wave else ""}queue=rollback_ready'>Rollback ready</a>
  <a class='btn sm' href='/admin/waves/?{"wave=" + quote(selected_wave, safe="") + "&" if selected_wave else ""}queue=rollback_finalized'>Rollback finalized</a>
  <a class='btn sm' href='/admin/waves.csv/{"?wave=" + quote(selected_wave, safe="") if selected_wave else ""}'>Export CSV</a>
</p>
<p class='small'>Current wave filter: <code>{esc(selected_wave or "all")}</code></p>
<p class='small'>Current queue filter: <code>{esc(selected_queue)}</code></p>
<div style='overflow:auto'>
<table>
<tr><th>Wave</th><th>Peers</th><th>Issued</th><th>Imported</th><th>Validated</th><th>Old removed</th><th>Rb candidates</th><th>Rb ready</th><th>Rb issued</th><th>Rb validated</th><th>Rb finalized</th></tr>
{summary_table}
</table>
</div>
</div>
{''.join(detail_sections)}
{back_home_link()}
"""
        return self._send_html(page("WG Admin Waves", body), headers=headers)

    def _render_admin_waves_csv(self, parsed=None):
        parsed = parsed or urlparse(self.path)
        q = parse_qs(parsed.query or "", keep_blank_values=True)
        selected_wave = str((q.get("wave") or [""])[0] or "").strip()
        state = _read_migration_state()
        peers_out = backend_list()
        peer_map = {}
        if peers_out.get("ok"):
            for item in list(peers_out.get("items", []) or []):
                pid = str(item.get("id") or "").strip()
                if pid:
                    peer_map[pid] = item
        buf = io.StringIO()
        writer = csv.writer(buf)
        writer.writerow(["wave", "peer_id", "admin_label", "admin_comment", "label", "status", "ingress_edge", "effective_edge", "effective_uplink", "issued", "client_imported", "validated", "old_removed", "rollback_issued", "rollback_validated", "rollback_finalized", "note"])
        for pid, raw in sorted(state.items(), key=lambda kv: str(kv[0])):
            row = _normalize_migration_state(raw)
            wave = str(row.get("wave") or "").strip()
            if not wave:
                continue
            if selected_wave and wave != selected_wave:
                continue
            peer = dict(peer_map.get(pid) or {})
            routing = _peer_routing_view(peer) if peer else {}
            checklist = dict(row.get("checklist") or {})
            writer.writerow([
                wave,
                pid,
                str(peer.get("admin_label") or peer.get("label") or pid),
                str(peer.get("admin_comment") or ""),
                str(peer.get("label") or pid),
                str(peer.get("status") or ""),
                str(routing.get("ingress_edge") or ""),
                str(routing.get("effective_edge") or ""),
                str(routing.get("effective_uplink") or routing.get("preferred_uplink") or ""),
                "1" if checklist.get("issued") else "0",
                "1" if checklist.get("client_imported") else "0",
                "1" if checklist.get("validated") else "0",
                "1" if checklist.get("old_removed") else "0",
                "1" if checklist.get("rollback_issued") else "0",
                "1" if checklist.get("rollback_validated") else "0",
                "1" if checklist.get("rollback_finalized") else "0",
                str(row.get("note") or ""),
            ])
        data = buf.getvalue().encode("utf-8")
        fname = "waves.csv" if not selected_wave else f"waves-{safe_slug(selected_wave)}.csv"
        self.send_response(200)
        self.send_header("Content-Type", "text/csv; charset=utf-8")
        self.send_header("Content-Disposition", f'attachment; filename="{fname}"')
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)
        return

    def _render_admin_edge_detail(self, edge_id, parsed=None, headers=None):
        out = backend_get_edge(edge_id)
        if not out.get("ok"):
            code = 404 if out.get("error") == "edge_not_found" else 500
            return self._send_html(page("WG Admin Edge", f"<div class='card'><b>Ошибка edge:</b> {esc(out.get('error'))}</div>"), code, headers=headers)

        row = dict(out.get("item") or {})
        eid = str(row.get("edge_id") or edge_id or "").strip().lower()
        parsed = parsed or urlparse(self.path)
        q = parse_qs(parsed.query or "", keep_blank_values=True)
        placement_filter = str((q.get("placement") or ["all"])[0] or "all").strip().lower()
        if placement_filter not in ("all", "intent", "effective", "drift", "incoming", "outgoing", "cutover"):
            placement_filter = "all"
        uplinks = list(row.get("uplinks", []) or [])
        uplinks.sort(key=lambda x: str(x.get("uplink_id") or x.get("name") or ""))
        uplink_rows = []
        for uplink in uplinks:
            uplink_rows.append(
                "<tr>"
                f"<td><code>{esc(uplink.get('uplink_id') or '-')}</code></td>"
                f"<td><code>{esc(uplink.get('name') or '-')}</code></td>"
                f"<td><code>{esc(uplink.get('kind') or '-')}</code></td>"
                f"<td><code>{'yes' if uplink.get('enabled') else 'no'}</code></td>"
                f"<td><code>{esc(uplink.get('health_status') or '-')}</code></td>"
                f"<td><code>{esc(uplink.get('active_peer_count') or 0)}</code></td>"
                f"<td><span class='small'>{esc(uplink.get('health_note') or '-')}</span></td>"
                "</tr>"
            )
        uplinks_table = "".join(uplink_rows) or "<tr><td colspan='7'>Пусто</td></tr>"

        peers_out = backend_list()
        placement_rows = []
        drift_rows = []
        placement_summary = {
            "intent": 0,
            "effective": 0,
            "drift": 0,
            "aligned": 0,
            "incoming": 0,
            "outgoing": 0,
            "cutover": 0,
        }
        bulk_peer_ids = []
        filtered_status_counts = {
            "active": 0,
            "pending": 0,
            "blocked": 0,
            "expired": 0,
            "removed": 0,
            "other": 0,
        }
        batch_default_wave = f"{eid}-wave"
        if peers_out.get("ok"):
            items = list(peers_out.get("items", []) or [])
            for item in items:
                _apply_admin_meta_to_item(item, latest_label_map=_latest_peer_label_map())
            items.sort(key=lambda x: (str(x.get("status") or ""), str(x.get("admin_label") or x.get("label") or ""), str(x.get("id") or "")))
            for item in items:
                routing = _peer_routing_view(item)
                ingress_edge = str(routing.get("ingress_edge") or "").strip().lower()
                effective_edge = str(routing.get("effective_edge") or "").strip().lower()
                if ingress_edge != eid and effective_edge != eid:
                    continue
                drift = _routing_drift_view(routing)
                status = str(item.get("status") or "").strip()
                hs_unix = int(item.get("last_handshake_unix") or 0)
                is_cutover_ready = bool(
                    effective_edge == eid
                    and not drift.get("has_drift")
                    and status == "active"
                    and hs_unix > 0
                )
                is_incoming = bool(ingress_edge == eid and effective_edge != eid)
                is_outgoing = bool(effective_edge == eid and ingress_edge != eid)
                migration_state = _get_migration_state(item.get("id"))
                migration_wave = str(migration_state.get("wave") or "").strip()
                migration_note = str(migration_state.get("note") or "").strip()
                migration_checklist = dict(migration_state.get("checklist") or {})
                checklist_summary = ("issued " if migration_checklist.get("issued") else "") + ("imported " if migration_checklist.get("client_imported") else "") + ("validated " if migration_checklist.get("validated") else "") + ("old-removed" if migration_checklist.get("old_removed") else "")
                include_row = (
                    placement_filter == "all"
                    or (placement_filter == "intent" and ingress_edge == eid)
                    or (placement_filter == "effective" and effective_edge == eid)
                    or (placement_filter == "drift" and drift.get("has_drift"))
                    or (placement_filter == "incoming" and is_incoming)
                    or (placement_filter == "outgoing" and is_outgoing)
                    or (placement_filter == "cutover" and is_cutover_ready)
                )
                placement_summary["intent"] += 1 if ingress_edge == eid else 0
                placement_summary["effective"] += 1 if effective_edge == eid else 0
                placement_summary["drift"] += 1 if drift.get("has_drift") else 0
                placement_summary["aligned"] += 1 if not drift.get("has_drift") else 0
                placement_summary["incoming"] += 1 if is_incoming else 0
                placement_summary["outgoing"] += 1 if is_outgoing else 0
                placement_summary["cutover"] += 1 if is_cutover_ready else 0
                peer_id = str(item.get("id") or "").strip()
                allowed_ip = str(item.get("allowed_ip") or "").strip()
                label = str(item.get("admin_label") or item.get("label") or peer_id).strip()
                admin_comment = str(item.get("admin_comment") or "").strip()
                last_hs_s = fmt_dt_ui(item.get("last_handshake_at"))
                drift_cell = f"<code>{esc(drift.get('label') or '-')}</code>" + (f"<div class='small'>{esc(drift.get('code') or '')}</div>" if drift.get("has_drift") else "")
                if include_row:
                    bulk_peer_ids.append(peer_id)
                    if status in filtered_status_counts:
                        filtered_status_counts[status] += 1
                    else:
                        filtered_status_counts["other"] += 1
                    placement_rows.append(
                        "<tr>"
                        f"<td><a href='/admin/peers/{quote(peer_id, safe='')}/'><b>{esc(label)}</b></a><div class='small'><code>{esc(peer_id)}</code></div>" + (f"<div class='small'>{esc(admin_comment)}</div>" if admin_comment else "") + "</td>"
                        f"<td><code>{esc(peer_status_ru(status))}</code></td>"
                        f"<td><code>{esc(allowed_ip or '-')}</code></td>"
                        f"<td><code>{esc(ingress_edge or '-')}</code></td>"
                        f"<td><code>{esc(effective_edge or '-')}</code></td>"
                        f"<td><code>{esc(routing.get('policy_mode') or '-')}</code></td>"
                        f"<td><code>{esc(routing.get('preferred_uplink') or '-')}</code></td>"
                        f"<td><code>{esc(routing.get('effective_uplink') or '-')}</code></td>"
                        f"<td><code>{esc(migration_wave or '-')}</code></td>"
                        f"<td><code>{esc(checklist_summary.strip() or '-')}</code>" + (f"<div class='small'>{esc(migration_note)}</div>" if migration_note else "") + "</td>"
                        f"<td>{drift_cell}</td>"
                        f"<td>{esc(last_hs_s)}</td>"
                        "</tr>"
                    )
                if drift.get("has_drift"):
                    drift_rows.append(
                        "<tr>"
                        f"<td><a href='/admin/peers/{quote(peer_id, safe='')}/'><b>{esc(label)}</b></a><div class='small'><code>{esc(peer_id)}</code></div>" + (f"<div class='small'>{esc(admin_comment)}</div>" if admin_comment else "") + "</td>"
                        f"<td><code>{esc(peer_status_ru(status))}</code></td>"
                        f"<td><code>{esc(ingress_edge or '-')}</code></td>"
                        f"<td><code>{esc(effective_edge or '-')}</code></td>"
                        f"<td><code>{esc(routing.get('preferred_uplink') or '-')}</code></td>"
                        f"<td><code>{esc(routing.get('effective_uplink') or '-')}</code></td>"
                        f"<td><code>{esc(migration_wave or '-')}</code></td>"
                        f"<td>{drift_cell}</td>"
                        "</tr>"
                    )
        placement_table = "".join(placement_rows) or "<tr><td colspan='12'>Для этого edge пока нет peer-ов по intent/effective state.</td></tr>"
        drift_table = "".join(drift_rows) or "<tr><td colspan='8'>Сейчас drift по этому edge не наблюдается.</td></tr>"
        filter_links = []
        for value, label in (
            ("all", "All"),
            ("intent", "Intent"),
            ("effective", "Effective"),
            ("drift", "Drift"),
            ("incoming", "Incoming"),
            ("outgoing", "Outgoing"),
            ("cutover", "Cutover Ready"),
        ):
            href = f"/admin/edges/{quote(eid, safe='')}/?placement={quote(value, safe='')}"
            cls = "btn sm" + (" active" if placement_filter == value else "")
            filter_links.append(f"<a class='{cls}' href='{href}'>{esc(label)}</a>")
        filters_html = "".join(filter_links)
        status_chips = []
        for key, label in (
            ("active", "Active"),
            ("pending", "Pending"),
            ("blocked", "Blocked"),
            ("expired", "Expired"),
            ("removed", "Removed"),
        ):
            status_chips.append(f"<span><b>{esc(label)}:</b> <code>{esc(filtered_status_counts.get(key) or 0)}</code></span>")
        if filtered_status_counts.get("other"):
            status_chips.append(f"<span><b>Other:</b> <code>{esc(filtered_status_counts.get('other') or 0)}</code></span>")
        status_counts_html = "".join(status_chips)
        bulk_ids_text = "\n".join(bulk_peer_ids)

        observed = fmt_dt_ui(row.get("observed_at") or row.get("last_seen_at") or "") or "-"
        body = f"""
{self._admin_nav("edges")}
<div class='card'>
<h2>Админ: edge `{esc(row.get("edge_id") or "-")}`</h2>
<p class='small'>Detail view для ingress edge. Пока только read-only.</p>
<table>
<tr><th>Field</th><th>Value</th></tr>
<tr><td>Edge</td><td><code>{esc(row.get("edge_id") or "-")}</code></td></tr>
<tr><td>Name</td><td><code>{esc(row.get("name") or "-")}</code></td></tr>
<tr><td>Role</td><td><code>{esc(row.get("role") or "-")}</code></td></tr>
<tr><td>State</td><td><code>{esc(row.get("state") or "-")}</code></td></tr>
<tr><td>Clients active</td><td><code>{esc(row.get("clients_active") or 0)}</code></td></tr>
<tr><td>Public host</td><td><code>{esc(row.get("public_host") or "-")}</code></td></tr>
<tr><td>Client interface</td><td><code>{esc(row.get("client_interface") or "-")}</code></td></tr>
<tr><td>Client subnet</td><td><code>{esc(row.get("client_subnet") or "-")}</code></td></tr>
<tr><td>RU egress</td><td><code>{esc(row.get("ru_egress") or "-")}</code></td></tr>
<tr><td>Health</td><td><code>{esc(row.get("health_status") or "-")}</code>{("<div class='small'>" + esc(row.get("health_note") or "") + "</div>") if row.get("health_note") else ""}</td></tr>
<tr><td>Observed at</td><td><code>{esc(observed)}</code></td></tr>
</table>
</div>
<div class='card'>
<h2>Аплинки edge</h2>
<div style='overflow:auto'>
<table>
<tr><th>Uplink ID</th><th>Name</th><th>Kind</th><th>Enabled</th><th>Health</th><th>Active peers</th><th>Note</th></tr>
{uplinks_table}
</table>
</div>
</div>
<div class='card'>
<h2>Peer placement</h2>
<p class='small'>Здесь видно, какие peer-ы целятся в этот edge по policy intent и какие фактически наблюдаются на нём сейчас.</p>
<div class='grid cols-4'>
  <div><b>Intent peers:</b> <code>{esc(placement_summary.get("intent") or 0)}</code></div>
  <div><b>Effective peers:</b> <code>{esc(placement_summary.get("effective") or 0)}</code></div>
  <div><b>Aligned:</b> <code>{esc(placement_summary.get("aligned") or 0)}</code></div>
  <div><b>Drift:</b> <code>{esc(placement_summary.get("drift") or 0)}</code></div>
</div>
<div class='grid cols-3' style='margin-top:10px'>
  <div><b>Incoming queue:</b> <code>{esc(placement_summary.get("incoming") or 0)}</code></div>
  <div><b>Outgoing queue:</b> <code>{esc(placement_summary.get("outgoing") or 0)}</code></div>
  <div><b>Cutover ready:</b> <code>{esc(placement_summary.get("cutover") or 0)}</code></div>
</div>
<p class='actions' style='margin-top:10px'>{filters_html}</p>
<p class='small'>Current filter: <code>{esc(placement_filter)}</code></p>
<p class='actions'>{status_counts_html}</p>
</div>
<div class='card'>
<h3>Operator queues</h3>
<p class='small'>Incoming = target edge уже выбран, но effective state ещё не на этом edge. Outgoing = peer всё ещё наблюдается на этом edge, хотя intent уже смотрит в другой edge. Cutover ready = active peer без drift, фактически уже на этом edge.</p>
<div class='grid cols-3'>
  <div><a class='btn sm' href='/admin/edges/{quote(eid, safe='')}/?placement=incoming'>Open incoming queue</a></div>
  <div><a class='btn sm' href='/admin/edges/{quote(eid, safe='')}/?placement=outgoing'>Open outgoing queue</a></div>
  <div><a class='btn sm' href='/admin/edges/{quote(eid, safe='')}/?placement=cutover'>Open cutover-ready</a></div>
</div>
</div>
<div class='card'>
<h3>Batch actions</h3>
<p class='small'>Работают по списку peer ID из текущего фильтра. Это staged workflow: сначала issue migration profiles, затем wave/notes, потом checklist по мере cutover.</p>
<form method='post' action='/admin/action/' class='peer-edit'>
<input type='hidden' name='action' value='batch_migrate_gateway'>
<input type='hidden' name='next' value='/admin/edges/{quote(eid, safe="")}/?placement={quote(placement_filter, safe="")}'>
<label>Gateway:
<select name='gateway'>{_edge_options_html('vrn')}</select>
</label>
<label>Uplink:
<select name='uplink'><option value='ams'>AMS / DIRECT</option><option value='fra'>FRA</option><option value='nyc'>NYC</option></select>
</label>
<label>Wave:
<input name='migration_wave' maxlength='64' value='{esc(batch_default_wave)}'>
</label>
<label>Note:
<input name='migration_note' maxlength='2000' value=''>
</label>
<textarea name='peer_ids' rows='6' style='width:100%;font-family:ui-monospace, SFMono-Regular, Menlo, monospace'>{esc(bulk_ids_text)}</textarea>
<button class='btn sm' type='submit'>Issue migration profiles</button>
</form>
<form method='post' action='/admin/action/' class='peer-edit' style='margin-top:10px'>
<input type='hidden' name='action' value='batch_set_migration_meta'>
<input type='hidden' name='next' value='/admin/edges/{quote(eid, safe="")}/?placement={quote(placement_filter, safe="")}'>
<label>Wave:
<input name='migration_wave' maxlength='64' value='{esc(batch_default_wave)}'>
</label>
<label>Note:
<input name='migration_note' maxlength='2000' value=''>
</label>
<textarea name='peer_ids' rows='6' style='width:100%;font-family:ui-monospace, SFMono-Regular, Menlo, monospace'>{esc(bulk_ids_text)}</textarea>
<button class='btn sm' type='submit'>Set wave / notes</button>
</form>
<form method='post' action='/admin/action/' class='actions' style='margin-top:10px'>
<input type='hidden' name='action' value='batch_set_migration_checklist'>
<input type='hidden' name='next' value='/admin/edges/{quote(eid, safe="")}/?placement={quote(placement_filter, safe="")}'>
<textarea name='peer_ids' rows='6' style='width:100%;font-family:ui-monospace, SFMono-Regular, Menlo, monospace'>{esc(bulk_ids_text)}</textarea>
<label><input type='checkbox' name='migration_issued' value='1'> issued</label>
<label><input type='checkbox' name='migration_client_imported' value='1'> client imported</label>
<label><input type='checkbox' name='migration_validated' value='1'> validated</label>
<label><input type='checkbox' name='migration_old_removed' value='1'> old removed</label>
<button class='btn sm' type='submit'>Set checklist state</button>
</form>
<form method='post' action='/admin/action/' class='peer-edit' style='margin-top:10px'>
<input type='hidden' name='action' value='batch_finalize_old_peers'>
<input type='hidden' name='next' value='/admin/edges/{quote(eid, safe="")}/?placement={quote(placement_filter, safe="")}'>
<textarea name='peer_ids' rows='6' style='width:100%;font-family:ui-monospace, SFMono-Regular, Menlo, monospace'>{esc(bulk_ids_text)}</textarea>
<button class='btn sm' type='submit' onclick="return confirm('Удалить указанные старые peer-ы? Это финальный cutover шаг.');">Finalize old peers</button>
</form>
</div>
<div class='card'>
<h3>Bulk peer IDs</h3>
<p class='small'>Компактный список peer ID для текущего фильтра. Удобно для staged cutover, заметок и операторских batch-runbook шагов.</p>
<textarea rows='8' style='width:100%;font-family:ui-monospace, SFMono-Regular, Menlo, monospace'>{esc(bulk_ids_text)}</textarea>
</div>
<div class='card'>
<h3>Drift only</h3>
<p class='small'>Короткий список peer-ов, у которых intent и effective state на этом edge сейчас расходятся.</p>
<div style='overflow:auto'>
<table>
<tr><th>Peer</th><th>Status</th><th>Ingress edge</th><th>Effective edge</th><th>Preferred</th><th>Effective</th><th>Wave</th><th>Routing</th></tr>
{drift_table}
</table>
</div>
</div>
<div class='card'>
<h3>Filtered placement</h3>
<div style='overflow:auto;margin-top:10px'>
<table>
<tr><th>Peer</th><th>Status</th><th>Allowed IP</th><th>Ingress edge</th><th>Effective edge</th><th>Mode</th><th>Preferred</th><th>Effective</th><th>Wave</th><th>Checklist / Note</th><th>Routing</th><th>Last handshake</th></tr>
{placement_table}
</table>
</div>
</div>
<p class='actions'><a class='btn sm' href='/admin/edges/'>Назад к edges</a></p>
{back_home_link()}
"""
        return self._send_html(page("WG Admin Edge", body), headers=headers)

    def _render_admin_uplinks(self, headers=None):
        out = backend_list_uplinks()
        if not out.get("ok"):
            return self._send_html(page("WG Admin Uplinks", f"<div class='card'><b>Ошибка uplinks:</b> {esc(out.get('error'))}</div>"), 500, headers=headers)

        items = list(out.get("items", []))
        items.sort(key=lambda x: (str(x.get("edge_id") or ""), str(x.get("name") or "")))

        summary = {
            "ams": {"count": 0, "ids": []},
            "fra": {"count": 0, "ids": []},
            "nyc": {"count": 0, "ids": []},
        }
        for ip in out.get("fra_ips", []) or []:
            summary["fra"]["count"] += 1
            summary["fra"]["ids"].append(str(ip))
        for ip in out.get("nyc_ips", []) or []:
            summary["nyc"]["count"] += 1
            summary["nyc"]["ids"].append(str(ip))
        ams_count = 0
        for row in items:
            if str(row.get("name") or "").strip().lower() == "ams":
                try:
                    ams_count += int(row.get("active_peer_count") or 0)
                except Exception:
                    pass
        summary["ams"]["count"] = ams_count

        def _fmt_num(value):
            try:
                if value is None or value == "":
                    return "-"
                num = float(value)
                if abs(num - round(num)) < 0.0001:
                    return str(int(round(num)))
                return f"{num:.1f}"
            except Exception:
                return esc(value)

        cards = []
        for key, title in (("ams", "AMS"), ("fra", "FRA"), ("nyc", "NYC")):
            ips = summary[key]["ids"]
            ips_html = ", ".join(f"<code>{esc(x)}</code>" for x in ips[:8]) if ips else "<span class='small'>-</span>"
            cards.append(
                "<div class='card'>"
                f"<h3>{esc(title)}</h3>"
                f"<p><b>Active peers:</b> <code>{esc(summary[key]['count'])}</code></p>"
                f"<p class='small'><b>Effective IPs:</b> {ips_html}</p>"
                "</div>"
            )

        rows = []
        for row in items:
            uplink_id = str(row.get("uplink_id") or "")
            edge_id = str(row.get("edge_id") or "")
            name = str(row.get("name") or "")
            kind = str(row.get("kind") or "")
            enabled = bool(row.get("enabled"))
            health = str(row.get("health_status") or "") or "-"
            health_note = str(row.get("health_note") or "")
            rtt = _fmt_num(row.get("rtt_ms"))
            loss = _fmt_num(row.get("loss_pct"))
            hs_age = _fmt_num(row.get("handshake_age_sec"))
            active_peer_count = _fmt_num(row.get("active_peer_count"))
            observed_at = fmt_dt_ui(row.get("observed_at") or "")
            rows.append(
                "<tr>"
                f"<td><code>{esc(uplink_id)}</code></td>"
                f"<td><code>{esc(edge_id or '-')}</code></td>"
                f"<td><code>{esc(name or '-')}</code></td>"
                f"<td><code>{esc(kind or '-')}</code></td>"
                f"<td><code>{'yes' if enabled else 'no'}</code></td>"
                f"<td><code>{esc(health)}</code>" + (f"<div class='small'>{esc(health_note)}</div>" if health_note else "") + "</td>"
                f"<td><code>{esc(rtt)}</code></td>"
                f"<td><code>{esc(loss)}</code></td>"
                f"<td><code>{esc(hs_age)}</code></td>"
                f"<td><code>{esc(active_peer_count)}</code></td>"
                f"<td><code>{esc(observed_at or '-')}</code></td>"
                "</tr>"
            )
        table = "".join(rows) or "<tr><td colspan='11'>Пусто</td></tr>"

        body = f"""
{self._admin_nav("uplinks")}
<div class='cards-grid'>
{''.join(cards)}
</div>
<div class='card'>
<h2>Админ: uplinks и edge runtime</h2>
<p class='small'>Inventory и runtime читаются из <code>/v1/uplinks</code>. Здесь видно состав uplink-ов, их здоровье и текущую effective-нагрузку по active peers.</p>
<div style='overflow:auto'>
<table>
<tr><th>Uplink ID</th><th>Edge</th><th>Name</th><th>Kind</th><th>Enabled</th><th>Health</th><th>RTT ms</th><th>Loss %</th><th>HS age sec</th><th>Active peers</th><th>Observed</th></tr>
{table}
</table>
</div>
</div>
{back_home_link()}
"""
        return self._send_html(page("WG Admin Uplinks", body), headers=headers)

    def _parse_pagination(self, parsed, default_per_page=25, max_per_page=200):
        qs = parse_qs(parsed.query)
        try:
            page = int((qs.get("page") or ["1"])[0])
        except Exception:
            page = 1
        try:
            per_page = int((qs.get("per_page") or [str(default_per_page)])[0])
        except Exception:
            per_page = default_per_page
        page = max(1, page)
        per_page = min(max(1, per_page), max_per_page)
        return page, per_page

    def _qs_first(self, parsed, key, default=""):
        qs = parse_qs(parsed.query)
        return str((qs.get(key) or [default])[0] or default).strip()

    def _paginate(self, items, page, per_page):
        total = len(items)
        total_pages = max(1, (total + per_page - 1) // per_page)
        page = min(page, total_pages)
        start = (page - 1) * per_page
        end = start + per_page
        return items[start:end], total, total_pages, page

    def _pager_html(self, base_path, page, per_page, total_pages, total_items, extra_params=None):
        extra_params = dict(extra_params or {})
        extra_params["per_page"] = per_page
        if total_pages <= 1:
            return f"<p class='small'>Всего: {total_items}</p>"
        prev_qs = dict(extra_params)
        prev_qs["page"] = page - 1
        next_qs = dict(extra_params)
        next_qs["page"] = page + 1
        prev_link = f"{base_path}?{urlencode(prev_qs)}"
        next_link = f"{base_path}?{urlencode(next_qs)}"
        prev_btn = f"<a class='btn sm' href='{prev_link}'>← Назад</a>" if page > 1 else "<span class='small'>← Назад</span>"
        next_btn = f"<a class='btn sm' href='{next_link}'>Вперед →</a>" if page < total_pages else "<span class='small'>Вперед →</span>"
        return f"<p class='actions'>{prev_btn}<span class='small'>Стр. {page} / {total_pages} | всего: {total_items}</span>{next_btn}</p>"

    def _render_admin_dashboard(self, headers=None):
        data = backend_list()
        if not data.get("ok"):
            return self._send_html(page("WG Admin", f"<div class='card'><b>Ошибка:</b> {esc(data.get('error'))}</div>"), 500, headers=headers)
        scope = str(ADMIN_DATA_SCOPE.get() or "all").strip().lower()
        items = _filter_items_by_scope(data.get("items", []), scope)
        now_unix = int(dt.datetime.now(dt.timezone.utc).timestamp())
        active_count = 0
        pending_count = 0
        blocked_count = 0
        removed_count = 0
        expired_count = 0
        routing_drift_count = 0
        for x in items:
            st = str(x.get("status") or "")
            if st == "pending":
                pending_count += 1
            elif st == "blocked":
                blocked_count += 1
            elif st == "removed":
                removed_count += 1
            elif st == "expired":
                expired_count += 1
            if self._is_peer_active_now(x, now_unix):
                active_count += 1
            routing = _peer_routing_view(x)
            if _routing_drift_view(routing).get("has_drift"):
                routing_drift_count += 1

        events_data = backend_list_events(limit=AUDIT_MAX_LINES)
        if not events_data.get("ok"):
            return self._send_html(page("WG Admin", f"<div class='card'><b>Ошибка events:</b> {esc(events_data.get('error'))}</div>"), 500, headers=headers)
        events_count = len(events_data.get("items", []))
        events_source = str(events_data.get("source") or "legacy")
        type_counts = {"admin": 0, "employee": 0, "vip": 0, "normal": 0}
        for x in items:
            pid = str(x.get("id") or "").strip()
            ctype = _get_client_type(pid)
            type_counts[ctype] = int(type_counts.get(ctype, 0)) + 1
        scope_q = f"?scope={quote(scope, safe='')}" if scope else ""

        def kv_link(title, value_html, href):
            return (
                f"<a class='kv kv-link' href='{href}'>"
                f"<span class='k'>{title}</span>"
                f"<div class='v'>{value_html}</div>"
                "</a>"
            )

        body = f"""
{self._admin_nav("dashboard")}
<div class='card'>
<h2>Admin Dashboard</h2>
<div class='kv-grid'>
{kv_link('Всего peer', f"<code>{esc(len(items))}</code>", f"/admin/peers/{scope_q}")}
{kv_link(f'Активны (HS ≤ {ACTIVE_WINDOW_SEC} сек)', f"<code>{esc(active_count)}</code>", f"/admin/live/{scope_q}")}
{kv_link('Pending', f"<code>{esc(pending_count)}</code>", f"/admin/peers/?status=pending&scope={esc(scope)}")}
{kv_link('Blocked', f"<code>{esc(blocked_count)}</code>", f"/admin/peers/?status=blocked&scope={esc(scope)}")}
{kv_link('Removed', f"<code>{esc(removed_count)}</code>", f"/admin/peers/?status=removed&scope={esc(scope)}")}
{kv_link('Expired', f"<code>{esc(expired_count)}</code>", f"/admin/peers/?status=expired&scope={esc(scope)}")}
{kv_link('Routing drift', f"<code>{esc(routing_drift_count)}</code>", f"/admin/peers/?view=full&scope={esc(scope)}")}
{kv_link(f'Событий (source={esc(events_source)}, до {AUDIT_MAX_LINES})', f"<code>{esc(events_count)}</code>", "/admin/events/")}
{kv_link('Типы клиентов', f"<code>Админ {esc(type_counts.get('admin',0))}</code> · <code>Сотрудник {esc(type_counts.get('employee',0))}</code> · <code>ВИП {esc(type_counts.get('vip',0))}</code> · <code>Обычный {esc(type_counts.get('normal',0))}</code>", f"/admin/peers/{scope_q}")}
</div>
<p class='small'>Доступ к админ-разделам только по admin token.</p>
</div>
{back_home_link()}
"""
        return self._send_html(page("WG Admin", body), headers=headers)

    def _render_admin_peers(self, parsed, headers=None):
        data = backend_list()
        if not data.get("ok"):
            return self._send_html(page("WG Admin Peers", f"<div class='card'><b>Ошибка:</b> {esc(data.get('error'))}</div>"), 500, headers=headers)
        items = list(data.get("items", []))
        scope = str(ADMIN_DATA_SCOPE.get() or "all").strip().lower()
        items = _filter_items_by_scope(items, scope)
        latest_label_map = _latest_peer_label_map()
        for x in items:
            _apply_admin_meta_to_item(x, latest_label_map=latest_label_map)
        uplink_map = {}
        if WG_PORTAL_UPLINK_ENABLED:
            out = backend_list_uplinks()
            if not out.get("ok"):
                return self._send_html(page("WG Admin Peers", f"<div class='card'><b>Ошибка uplink:</b> {esc(out.get('error'))}</div>"), 500, headers=headers)
            uplink_map = _uplink_map_from_out(items, out)
        latest_peer_ip = _latest_peer_real_ip_map()
        status_filter = self._qs_first(parsed, "status", "")
        q_filter = self._qs_first(parsed, "q", "").lower()
        view_mode = self._qs_first(parsed, "view", "compact").lower()
        if view_mode not in ("compact", "full"):
            view_mode = "compact"
        default_statuses = ("active", "pending")

        if status_filter:
            items = [x for x in items if str(x.get("status") or "").strip() == status_filter]
        else:
            items = [x for x in items if str(x.get("status") or "").strip() in default_statuses]
        if q_filter:
            def _match_peer(x):
                s = " ".join(
                    [
                        str(x.get("id") or ""),
                        str(x.get("label") or ""),
                        str(x.get("allowed_ip") or ""),
                        str(x.get("status") or ""),
                    ]
                ).lower()
                return q_filter in s
            items = [x for x in items if _match_peer(x)]

        items.sort(key=lambda x: str(x.get("created_at") or ""), reverse=True)
        page_num, per_page = self._parse_pagination(parsed, default_per_page=25, max_per_page=200)
        page_items, total, total_pages, page_num = self._paginate(items, page_num, per_page)
        pager_top = self._pager_html(
            "/admin/peers/",
            page_num,
            per_page,
            total_pages,
            total,
            extra_params={"status": status_filter, "q": q_filter, "view": view_mode, "scope": scope},
        )

        rows = []
        for x in page_items:
            peer_id_raw = str(x.get("id") or "")
            peer_id = esc(peer_id_raw)
            status = str(x.get("status") or "")
            ip_raw = str(x.get("allowed_ip") or "")
            ip_short = ip_raw.split("/", 1)[0] if ip_raw else ""
            routing = _peer_routing_view(x, uplink_map)
            routing_drift = _routing_drift_view(routing)
            uplink = routing.get("preferred_uplink") or uplink_map.get(peer_id_raw, "")
            client_type = _get_client_type(peer_id_raw)
            created_s = fmt_dt_ui(x.get("created_at"))
            last_hs_s = fmt_dt_ui(x.get("last_handshake_at"))
            real_ip = str(x.get("real_ip") or "").strip() or latest_peer_ip.get(peer_id_raw, "-")
            real_geo = _geo_for_ip(real_ip) if real_ip not in ("", "-") else ""

            block_btn = (
                f"<button class='btn icon' type='submit' name='action' value='block' title='Блокировать' onclick=\"return confirm('Блокировать {peer_id}?');\">{svg_block()}<span class='sr'>Блокировать</span></button>"
                if status not in ("blocked", "removed", "expired")
                else "<span class='small'>-</span>"
            )
            del_btn = (
                f"<button class='btn icon' type='submit' name='action' value='delete' title='Удалить' onclick=\"return confirm('Удалить {peer_id}?');\">{svg_trash()}<span class='sr'>Удалить</span></button>"
                if status not in ("removed",)
                else "<span class='small'>-</span>"
            )
            uplink_buttons = (
                f"<button class='btn sm' type='submit' name='action' value='uplink_ams' {'disabled' if uplink == 'ams' else ''}>AMS</button>"
                f"<button class='btn sm' type='submit' name='action' value='uplink_nyc' {'disabled' if uplink == 'nyc' else ''}>NYC</button>"
                f"<button class='btn sm' type='submit' name='action' value='uplink_fra' {'disabled' if uplink == 'fra' else ''}>FRA</button>"
                if WG_PORTAL_UPLINK_ENABLED and ip_short
                else ""
            )
            actions = (
                "<form method='post' action='/admin/action/' class='actions'>"
                f"<input type='hidden' name='id' value='{peer_id}'>"
                f"<input type='hidden' name='allowed_ip' value='{esc(ip_short)}'>"
                f"{uplink_buttons}{block_btn}{del_btn}"
                "</form>"
            )
            label_cell = (
                f"<div><a href='/admin/peers/{quote(peer_id_raw, safe='')}/'><b>{esc(x.get('admin_label') or x.get('label'))}</b></a></div>"
                + (f"<div class='small'>{esc(ip_short)}</div>" if ip_short else "<div class='small'>-</div>")
                + (f"<div class='small'>{esc(x.get('admin_comment') or '')}</div>" if str(x.get('admin_comment') or '').strip() else "")
                + f"<div class='small'><code>{esc(routing_drift.get('label') or '-')}</code>" + (f" <span>{esc(routing_drift.get('code') or '')}</span>" if routing_drift.get('has_drift') else "") + "</div>"
            )
            edge_drift = "aligned"
            if str(routing.get("ingress_edge") or "").strip() != str(routing.get("effective_edge") or "").strip():
                edge_drift = f"{str(routing.get('ingress_edge') or '-').strip()} -> {str(routing.get('effective_edge') or '-').strip()}"
            route_cell = (
                f"<div><code>{esc(routing.get('ingress_edge') or '-')}</code> → <code>{esc(routing.get('effective_edge') or '-')}</code></div>"
                f"<div class='small'>mode=<code>{esc(routing.get('policy_mode') or '-')}</code> pref=<code>{esc(routing.get('preferred_uplink') or '-')}</code> eff=<code>{esc(routing.get('effective_uplink') or '-')}</code></div>"
                f"<div class='small'>drift=<code>{esc(edge_drift)}</code></div>"
            )
            hs_cell = f"<div>{esc(last_hs_s)}</div><div class='small'>создан {esc(created_s)}</div>"
            ip_cell = f"<div>{esc(real_ip)}</div>" + (f"<div class='small'>{esc(real_geo)}</div>" if real_geo else "")
            if view_mode == "full":
                rows.append(
                    "<tr>"
                    f"<td>{label_cell}</td>"
                    f"<td><code>{esc(peer_status_ru(status))}</code></td>"
                    f"<td><code>{esc(CLIENT_TYPE_LABELS.get(client_type, CLIENT_TYPE_LABELS['normal']))}</code></td>"
                    f"<td><code>{esc(routing.get('ingress_edge') or '-')}</code></td>"
                    f"<td><code>{esc(routing.get('effective_edge') or '-')}</code></td>"
                    f"<td><code>{esc(routing.get('policy_mode') or '-')}</code></td>"
                    f"<td><code>{esc(routing.get('preferred_uplink') or '-')}</code></td>"
                    f"<td><code>{esc(routing.get('effective_uplink') or '-')}</code></td>"
                    f"<td><code>{esc(routing.get('failover_uplink') or '-')}</code></td>"
                    f"<td><code>{esc(edge_drift)}</code></td>"
                    f"<td>{esc(created_s)}</td>"
                    f"<td>{esc(last_hs_s)}</td>"
                    f"<td>{ip_cell}</td>"
                    f"<td>{actions}</td>"
                    "</tr>"
                )
            else:
                rows.append(
                    "<tr>"
                    f"<td>{label_cell}</td>"
                    f"<td><code>{esc(peer_status_ru(status))}</code><div class='small'>{esc(CLIENT_TYPE_LABELS.get(client_type, CLIENT_TYPE_LABELS['normal']))}</div></td>"
                    f"<td>{route_cell}</td>"
                    f"<td>{hs_cell}</td>"
                    f"<td>{ip_cell}</td>"
                    f"<td>{actions}</td>"
                    "</tr>"
                )
        table = "".join(rows) or ("<tr><td colspan='14'>Пусто</td></tr>" if view_mode == "full" else "<tr><td colspan='6'>Пусто</td></tr>")
        status_opts = ["", "pending", "active", "blocked", "expired", "removed"]
        status_html = "".join(
            [
                f"<option value='{esc(s)}' {'selected' if s == status_filter else ''}>{esc('Активные + ожидают' if s == '' else peer_status_ru(s))}</option>"
                for s in status_opts
            ]
        )
        view_switch = (
            f"<a class='btn sm' href='/admin/peers/?{esc(urlencode({'status': status_filter, 'q': q_filter, 'per_page': per_page, 'view': 'compact', 'scope': scope}))}'>Compact</a>"
            f"<a class='btn sm' href='/admin/peers/?{esc(urlencode({'status': status_filter, 'q': q_filter, 'per_page': per_page, 'view': 'full', 'scope': scope}))}'>Full</a>"
        )
        body = f"""
{self._admin_nav("peers")}
<div class='card'>
<h2>Админ: клиенты и handshake</h2>
<form method='get' action='/admin/peers/' class='actions'>
<label>Статус:
<select name='status'>{status_html}</select>
</label>
<label>Поиск: <input name='q' value='{esc(q_filter)}' placeholder='id, label, ip'></label>
<label>На странице: <input name='per_page' type='number' min='1' max='200' value='{esc(per_page)}' style='width:90px'></label>
<input type='hidden' name='view' value='{esc(view_mode)}'>
<input type='hidden' name='scope' value='{esc(scope)}'>
<button class='btn sm' type='submit'>Фильтровать</button>
<a class='btn sm' href='/admin/peers/'>Сброс</a>
<a class='btn sm' href='/admin/peers.csv/?{esc(urlencode({"status": status_filter, "q": q_filter, "per_page": per_page, "scope": scope}))}'>Экспорт CSV</a>
</form>
<p class='actions'>
  <a class='btn sm' href='/admin/peers/?status=active&per_page=25&view={esc(view_mode)}&scope={esc(scope)}'>Активные</a>
  <a class='btn sm' href='/admin/peers/?status=pending&per_page=25&view={esc(view_mode)}&scope={esc(scope)}'>Ожидают</a>
  <a class='btn sm' href='/admin/peers/?status=blocked&per_page=25&view={esc(view_mode)}&scope={esc(scope)}'>Заблокированные</a>
  <a class='btn sm' href='/admin/peers/?status=removed&per_page=25&view={esc(view_mode)}&scope={esc(scope)}'>Удалённые</a>
</p>
<p class='actions'><span class='small'>View:</span> {view_switch} <span class='small'>current=<code>{esc(view_mode)}</code></span></p>
{pager_top}
<div style='overflow:auto'>
<table>
{("<tr><th>Профиль</th><th>Статус</th><th>Тип</th><th>Edge</th><th>Effective edge</th><th>Режим</th><th>Preferred</th><th>Active</th><th>Failover</th><th>Edge drift</th><th>Создан</th><th>Последний HS</th><th>IP (реальный)</th><th>Действия</th></tr>" if view_mode == "full" else "<tr><th>Профиль</th><th>Статус</th><th>Маршрут</th><th>Handshake</th><th>IP (реальный)</th><th>Действия</th></tr>")}
{table}
</table>
</div>
 {self._pager_html("/admin/peers/", page_num, per_page, total_pages, total, extra_params={"status": status_filter, "q": q_filter, "view": view_mode, "scope": scope})}
<p class='small'>Клик по имени профиля открывает детальную страницу клиента. Compact mode теперь основной, full оставлен для deep-dive.</p>
</div>
<script>
(() => {{
  async function copyText(txt) {{
    try {{
      await navigator.clipboard.writeText(txt);
      return true;
    }} catch (e) {{
      return false;
    }}
  }}
  document.addEventListener('click', async (ev) => {{
    const btn = ev.target.closest('[data-copy]');
    if (!btn) return;
    ev.preventDefault();
    const txt = btn.getAttribute('data-copy') || '';
    const old = btn.textContent;
    const ok = await copyText(txt);
    btn.textContent = ok ? 'OK' : 'ERR';
    setTimeout(() => {{ btn.textContent = old; }}, 900);
  }});
}})();
</script>
{back_home_link()}
"""
        return self._send_html(page("WG Admin Peers", body), headers=headers)

    def _render_admin_peers_csv(self, parsed):
        data = backend_list()
        if not data.get("ok"):
            return self._send_json({"ok": False, "error": str(data.get("error") or "list_failed")}, 500)
        items = list(data.get("items", []))
        scope = self._qs_first(parsed, "scope", str(ADMIN_DATA_SCOPE.get() or "all")).lower()
        items = _filter_items_by_scope(items, scope)
        latest_label_map = _latest_peer_label_map()
        for x in items:
            _apply_admin_meta_to_item(x, latest_label_map=latest_label_map)
        uplink_map = {}
        if WG_PORTAL_UPLINK_ENABLED:
            out = backend_list_uplinks()
            if not out.get("ok"):
                return self._send_json({"ok": False, "error": str(out.get("error") or "uplink_list_failed")}, 500)
            uplink_map = _uplink_map_from_out(items, out)
        status_filter = self._qs_first(parsed, "status", "")
        q_filter = self._qs_first(parsed, "q", "").lower()
        default_statuses = ("active", "pending")
        if status_filter:
            items = [x for x in items if str(x.get("status") or "").strip() == status_filter]
        else:
            items = [x for x in items if str(x.get("status") or "").strip() in default_statuses]
        if q_filter:
            items = [
                x
                for x in items
                if q_filter
                in " ".join(
                    [
                        str(x.get("id") or ""),
                        str(x.get("admin_label") or x.get("label") or ""),
                        str(x.get("admin_comment") or ""),
                        str(x.get("allowed_ip") or ""),
                        str(x.get("status") or ""),
                    ]
                ).lower()
            ]
        items.sort(key=lambda x: str(x.get("created_at") or ""), reverse=True)
        rows = []
        for x in items:
            pid = str(x.get("id") or "").strip()
            routing = _peer_routing_view(x, uplink_map)
            rows.append(
                [
                    pid,
                    str(x.get("admin_label") or x.get("label") or ""),
                    str(x.get("admin_comment") or ""),
                    str(x.get("status") or ""),
                    str(CLIENT_TYPE_LABELS.get(_get_client_type(pid), CLIENT_TYPE_LABELS["normal"])),
                    str(routing.get("ingress_edge") or ""),
                    str(routing.get("effective_edge") or ""),
                    str(routing.get("policy_mode") or ""),
                    str(routing.get("preferred_uplink") or ""),
                    str(routing.get("effective_uplink") or ""),
                    str(routing.get("failover_uplink") or ""),
                    str(_routing_drift_view(routing).get("code") or ""),
                    str(x.get("allowed_ip") or ""),
                    str(x.get("created_at") or ""),
                    str(x.get("last_handshake_at") or ""),
                    str(x.get("expires_at") or ""),
                    str(x.get("rx_bytes") or 0),
                    str(x.get("tx_bytes") or 0),
                ]
            )
        return self._send_csv(
            "wg_admin_peers.csv",
            rows,
            ["id", "admin_label", "admin_comment", "status", "type", "ingress_edge", "effective_edge", "policy_mode", "preferred_uplink", "effective_uplink", "failover_uplink", "routing_state", "allowed_ip", "created_at", "last_handshake_at", "expires_at", "rx_bytes", "tx_bytes"],
        )

    def _render_admin_events(self, parsed, headers=None):
        page_num, per_page = self._parse_pagination(parsed, default_per_page=50, max_per_page=200)
        want_limit = max(per_page * max(page_num, 1), 200)
        events_data = backend_list_events(limit=want_limit)
        if not events_data.get("ok"):
            return self._send_html(page("WG Admin Events", f"<div class='card'><b>Ошибка events:</b> {esc(events_data.get('error'))}</div>"), 500, headers=headers)
        events = list(events_data.get("items", []))
        peer_items = {}
        peers_out = backend_list()
        latest_label_map = _latest_peer_label_map()
        if peers_out.get("ok"):
            for item in list(peers_out.get("items", []) or []):
                pid = str(item.get("id") or "").strip()
                if not pid:
                    continue
                _apply_admin_meta_to_item(item, latest_label_map=latest_label_map)
                peer_items[pid] = item
        events_source = str(events_data.get("source") or "legacy")
        event_filter = self._qs_first(parsed, "event", "")
        quick_filter = self._qs_first(parsed, "quick", "")
        peer_id_filter = self._qs_first(parsed, "peer_id", "")
        q_filter = self._qs_first(parsed, "q", "").lower()

        if event_filter:
            events = [e for e in events if str(e.get("event") or "").strip() == event_filter]
        if quick_filter:
            events = [e for e in events if _event_matches_quick_filter(e.get("event"), quick_filter)]
        if peer_id_filter:
            events = [e for e in events if str(e.get("peer_id") or "").strip() == peer_id_filter]
        if q_filter:
            def _match_event(e):
                s = " ".join(
                    [
                        str(e.get("event") or ""),
                        str(e.get("peer_id") or ""),
                        str((peer_items.get(str(e.get("peer_id") or "").strip()) or {}).get("admin_label") or ""),
                        str(e.get("label") or ""),
                        str(e.get("ip") or ""),
                        str(e.get("action") or ""),
                        str(e.get("reason") or ""),
                        str(e.get("error") or ""),
                    ]
                ).lower()
                return q_filter in s
            events = [e for e in events if _match_event(e)]

        page_items, total, total_pages, page_num = self._paginate(events, page_num, per_page)
        rows = []
        for e in page_items:
            ts = esc(one_line(fmt_dt_ui(e.get("ts") or "")))
            ev_raw = one_line(e.get("event") or "")
            ev_ru = event_label_ru(ev_raw)
            ev = esc(ev_raw)
            ev_ru_esc = esc(ev_ru)
            ip = esc(one_line(e.get("ip") or ""))
            pid_raw = one_line(e.get("peer_id") or "")
            pid = esc(pid_raw)
            label = esc(one_line((peer_items.get(pid_raw) or {}).get("admin_label") or e.get("label") or ""))
            action = esc(one_line(e.get("action") or ""))
            reason = esc(one_line(e.get("reason") or ""))
            ev_id = int(e.get("_event_id") or 0)
            ev_link = f"<a href='/admin/events/{ev_id}/'><code title='{ev}'>{ev_ru_esc}</code></a>" if ev_id > 0 else f"<code title='{ev}'>{ev_ru_esc}</code>"
            peer_link = (
                f"<a href='/admin/peers/{quote(pid_raw, safe='')}/'><b>{label or pid or '-'}</b></a><div class='small'><code>{pid}</code></div>"
                if pid_raw
                else f"<code>{label or '-'}</code>"
            )
            extra = ""
            if e.get("error"):
                extra = "error=" + esc(one_line(e.get("error")))
            rows.append(
                f"<tr><td><code>{ts}</code></td><td>{ev_link}</td><td><code>{ip}</code></td><td>{peer_link}</td><td><code>{action}</code></td><td><code>{reason}</code></td><td class='small'>{extra}</td></tr>"
            )
        table = "".join(rows) or "<tr><td colspan='7'>Пусто</td></tr>"
        event_values = sorted({str(e.get("event") or "").strip() for e in events if str(e.get("event") or "").strip()})
        event_opts = [""] + event_values
        event_opts_html = "".join(
            [
                f"<option value='{esc(s)}' {'selected' if s == event_filter else ''}>{esc(event_label_ru(s))}</option>"
                for s in event_opts
            ]
        )
        body = f"""
{self._admin_nav("events")}
<div class='card'>
<h2>Админ: лента событий</h2>
<form method='get' action='/admin/events/' class='actions'>
<label>Событие:
<select name='event'>{event_opts_html}</select>
</label>
<input type='hidden' name='quick' value='{esc(quick_filter)}'>
<label>Peer ID: <input name='peer_id' value='{esc(peer_id_filter)}' placeholder='p...'></label>
<label>Поиск: <input name='q' value='{esc(q_filter)}' placeholder='event/ip/reason'></label>
<label>На странице: <input name='per_page' type='number' min='1' max='200' value='{esc(per_page)}' style='width:90px'></label>
<button class='btn sm' type='submit'>Фильтровать</button>
<a class='btn sm' href='/admin/events/'>Сброс</a>
<a class='btn sm' href='/admin/events.csv/?{esc(urlencode({"event": event_filter, "quick": quick_filter, "peer_id": peer_id_filter, "q": q_filter}))}'>Экспорт CSV</a>
</form>
<p class='actions'>
  <a class='btn sm' href='/admin/events/?quick=login_denied&per_page=50'>Отказы логина</a>
  <a class='btn sm' href='/admin/events/?quick=admin_errors&per_page=50'>Ошибки админа</a>
  <a class='btn sm' href='/admin/events/?quick=admin_ok&per_page=50'>Успех админа</a>
  <a class='btn sm' href='/admin/events/?quick=api&per_page=50'>API события</a>
</p>
{self._pager_html("/admin/events/", page_num, per_page, total_pages, total, extra_params={"event": event_filter, "quick": quick_filter, "peer_id": peer_id_filter, "q": q_filter})}
<div style='overflow:auto'>
<table>
<tr><th>Время</th><th>Событие</th><th>IP</th><th>Peer</th><th>Action</th><th>Причина</th><th>Дополнительно</th></tr>
{table}
</table>
</div>
{self._pager_html("/admin/events/", page_num, per_page, total_pages, total, extra_params={"event": event_filter, "quick": quick_filter, "peer_id": peer_id_filter, "q": q_filter})}
<p class='small'>Клик по событию или peer открывает детальную страницу. Source: <code>{esc(events_source)}</code>. Лента ограничена последними {esc(events_data.get("limit") or AUDIT_MAX_LINES)} событиями.</p>
</div>
{back_home_link()}
"""
        return self._send_html(page("WG Admin Events", body), headers=headers)

    def _render_admin_events_csv(self, parsed):
        page_num, per_page = self._parse_pagination(parsed, default_per_page=50, max_per_page=200)
        want_limit = max(per_page * max(page_num, 1), 500)
        events_data = backend_list_events(limit=want_limit)
        if not events_data.get("ok"):
            return self._send_json({"ok": False, "error": str(events_data.get("error") or "events_list_failed")}, 500)
        events = list(events_data.get("items", []))
        peer_items = {}
        peers_out = backend_list()
        latest_label_map = _latest_peer_label_map()
        if peers_out.get("ok"):
            for item in list(peers_out.get("items", []) or []):
                pid = str(item.get("id") or "").strip()
                if not pid:
                    continue
                _apply_admin_meta_to_item(item, latest_label_map=latest_label_map)
                peer_items[pid] = item
        event_filter = self._qs_first(parsed, "event", "")
        quick_filter = self._qs_first(parsed, "quick", "")
        peer_id_filter = self._qs_first(parsed, "peer_id", "")
        q_filter = self._qs_first(parsed, "q", "").lower()
        if event_filter:
            events = [e for e in events if str(e.get("event") or "").strip() == event_filter]
        if quick_filter:
            events = [e for e in events if _event_matches_quick_filter(e.get("event"), quick_filter)]
        if peer_id_filter:
            events = [e for e in events if str(e.get("peer_id") or "").strip() == peer_id_filter]
        if q_filter:
            events = [
                e
                for e in events
                if q_filter
                in " ".join(
                    [
                        str(e.get("event") or ""),
                        str(e.get("peer_id") or ""),
                        str((peer_items.get(str(e.get("peer_id") or "").strip()) or {}).get("admin_label") or ""),
                        str(e.get("label") or ""),
                        str(e.get("ip") or ""),
                        str(e.get("action") or ""),
                        str(e.get("reason") or ""),
                        str(e.get("error") or ""),
                    ]
                ).lower()
            ]
        rows = []
        for e in events:
            rows.append(
                [
                    str(e.get("ts") or ""),
                    str(e.get("event") or ""),
                    str(e.get("ip") or ""),
                    str(e.get("peer_id") or ""),
                    str((peer_items.get(str(e.get("peer_id") or "").strip()) or {}).get("admin_label") or e.get("label") or ""),
                    str(e.get("action") or ""),
                    str(e.get("reason") or ""),
                    str(e.get("error") or ""),
                    str(e.get("ok") if e.get("ok") is not None else ""),
                ]
            )
        return self._send_csv(
            "wg_admin_events.csv",
            rows,
            ["ts", "event", "ip", "peer_id", "label", "action", "reason", "error", "ok"],
        )

    def _render_admin_peer_detail(self, peer_id_raw, headers=None):
        peer_id = str(peer_id_raw or "").strip()
        data = backend_list()
        if not data.get("ok"):
            return self._send_html(page("WG Admin Peer", f"<div class='card'><b>Ошибка:</b> {esc(data.get('error'))}</div>"), 500, headers=headers)
        item = None
        for x in data.get("items", []):
            if str(x.get("id") or "").strip() == peer_id:
                item = x
                break
        if not item:
            body = f"""
{self._admin_nav("peers")}
<div class='card warn'>
<h2>Клиент не найден</h2>
<p>Peer ID: <code>{esc(peer_id)}</code></p>
</div>
<p><a class='btn' href='/admin/peers/'>← К списку клиентов</a></p>
"""
            return self._send_html(page("WG Admin Peer", body), 404, headers=headers)

        events_data = backend_list_events(limit=AUDIT_MAX_LINES)
        if not events_data.get("ok"):
            return self._send_html(page("WG Admin Peer", f"<div class='card'><b>Ошибка events:</b> {esc(events_data.get('error'))}</div>"), 500, headers=headers)
        events = list(events_data.get("items", []))
        related = [e for e in events if str(e.get("peer_id") or "").strip() == peer_id][:80]
        _apply_admin_meta_to_item(item, latest_label_map=_latest_peer_label_map())
        latest_peer_ip = _latest_peer_real_ip_map()
        endpoint_by_pub, endpoint_by_allowed_ip = _wg_endpoint_maps()
        peer_pub = str(item.get("public_key") or "").strip()
        ip_short = str(item.get("allowed_ip") or "").split("/", 1)[0].strip()
        real_ip = str(item.get("real_ip") or "").strip()
        if peer_pub:
            real_ip = real_ip or endpoint_by_pub.get(peer_pub, "")
        if (not real_ip) and ip_short:
            real_ip = endpoint_by_allowed_ip.get(ip_short, "")
        if not real_ip:
            real_ip = str(latest_peer_ip.get(peer_id) or "-")
        real_geo = _geo_for_ip(real_ip) if real_ip not in ("", "-") else ""
        uplink = ""
        routing = {}
        if WG_PORTAL_UPLINK_ENABLED:
            out = backend_list_uplinks()
            if out.get("ok"):
                uplink = _uplink_map_from_out([item], out).get(str(item.get("id") or "").strip(), "")
            routing_out = backend_get_peer_routing(peer_id)
            if routing_out.get("ok"):
                routing = routing_out
        merged_item = dict(item)
        if routing:
            merged_item.update(
                {
                    "ingress_edge": routing.get("ingress_edge"),
                    "effective_edge": routing.get("effective_edge"),
                    "policy_mode": routing.get("policy_mode"),
                    "preferred_uplink": routing.get("preferred_uplink"),
                    "active_uplink": routing.get("active_uplink"),
                    "effective_uplink": routing.get("effective_uplink"),
                    "failover_uplink": routing.get("failover_uplink"),
                    "health_status": routing.get("health_status"),
                    "health_note": routing.get("health_note"),
                }
            )
        routing_view = _peer_routing_view(merged_item, {peer_id: uplink})
        routing_drift = _routing_drift_view(routing_view)
        migration_state = _get_migration_state(peer_id)
        migration_checklist = dict(migration_state.get("checklist") or {})
        edge_drift_note = "aligned"
        if str(routing_view.get("ingress_edge") or "").strip() != str(routing_view.get("effective_edge") or "").strip():
            edge_drift_note = f"{str(routing_view.get('ingress_edge') or '-').strip()} -> {str(routing_view.get('effective_edge') or '-').strip()}"
        uplink = routing_view.get("preferred_uplink") or uplink
        form_edge = str(routing_view.get("effective_edge") or routing_view.get("ingress_edge") or "edg").strip().lower() or "edg"
        preferred_options = [
            ("ams", _uplink_label_for_edge(form_edge, "ams")),
            ("fra", _uplink_label_for_edge(form_edge, "fra")),
            ("nyc", _uplink_label_for_edge(form_edge, "nyc")),
        ]
        admin_preferred_options_html = "".join(
            f"<option value='{esc(value)}' {'selected' if routing_view.get('preferred_uplink') == value else ''}>{esc(title)}</option>"
            for value, title in preferred_options
        )
        migrate_default_gateway = str(routing_view.get("effective_edge") or routing_view.get("ingress_edge") or "edg").strip().lower() or "edg"
        migrate_default_uplink = str(routing_view.get("effective_uplink") or routing_view.get("preferred_uplink") or "ams").strip().lower() or "ams"
        migrate_gateway_options_html = _edge_options_html(migrate_default_gateway)
        migrate_uplink_options_edg = [("ams", "AMS"), ("fra", "FRA"), ("nyc", "NYC")]
        migrate_uplink_options_vrn = [("ams", "AMS"), ("fra", "FRA"), ("nyc", "NYC")]
        migrate_uplink_edg_html = "".join(
            f"<option value='{esc(value)}' {'selected' if migrate_default_gateway == 'edg' and migrate_default_uplink == value else ''}>{esc(title)}</option>"
            for value, title in migrate_uplink_options_edg
        )
        migrate_uplink_vrn_html = "".join(
            f"<option value='{esc(value)}' {'selected' if migrate_default_gateway == 'vrn' and migrate_default_uplink == value else ''}>{esc(title)}</option>"
            for value, title in migrate_uplink_options_vrn
        )
        client_type = _get_client_type(peer_id)
        status_raw = str(item.get("status") or "").strip()
        is_active_status = (status_raw == "active")
        effective_edge = str(routing_view.get("effective_edge") or "-").strip()
        ingress_edge = str(routing_view.get("ingress_edge") or "-").strip()
        last_hs_unix = int(item.get("last_handshake_unix") or 0)
        now_unix = int(time.time())
        hs_recent = bool(last_hs_unix > 0 and max(0, now_unix - last_hs_unix) <= ACTIVE_WINDOW_SEC)
        seamless_supported = bool(_is_supported_edge(effective_edge) and _is_supported_edge(ingress_edge))
        seamless_candidate = bool(is_active_status and hs_recent and seamless_supported and effective_edge != ingress_edge)
        seamless_label = "candidate" if seamless_candidate else "hold"
        seamless_note = "peer active with recent handshake and cross-edge drift present" if seamless_candidate else "needs active session, recent handshake and edge drift before in-place move can be attempted"
        ev_rows = []
        for e in related:
            ev_id = int(e.get("_event_id") or 0)
            ev_href = f"/admin/events/{ev_id}/" if ev_id > 0 else "/admin/events/"
            ev_rows.append(
                "<tr>"
                f"<td><code>{esc(fmt_dt_ui(e.get('ts') or ''))}</code></td>"
                f"<td><a href='{ev_href}'><code>{esc(one_line(e.get('event') or ''))}</code></a></td>"
                f"<td><code>{esc(one_line(e.get('ip') or ''))}</code></td>"
                f"<td><code>{esc(one_line(e.get('action') or ''))}</code></td>"
                f"<td><code>{esc(one_line(e.get('reason') or ''))}</code></td>"
                "</tr>"
            )
        ev_table = "".join(ev_rows) or "<tr><td colspan='5' class='small'>Событий для этого peer пока нет.</td></tr>"
        status_ru = peer_status_ru(str(item.get("status") or ""))

        body = f"""
{self._admin_nav("peers")}
<div class='card'>
<div class='peer-head'>
  <h2 class='peer-title'>Детали клиента</h2>
  <code>{esc(status_ru)}</code>
</div>
<form method='post' action='/admin/action/' class='peer-edit'>
<input type='hidden' name='id' value='{esc(peer_id)}'>
<input type='hidden' name='next' value='/admin/peers/{quote(peer_id, safe="")}/'>
<label>Admin label:
<input name='admin_label' maxlength='128' value='{esc(item.get("admin_label") or item.get("label") or "")}' placeholder='Операторское имя профиля'>
</label>
<label>Admin comment:
<input name='admin_comment' maxlength='500' value='{esc(item.get("admin_comment") or "")}' placeholder='Комментарий для админки'>
</label>
<button class='btn sm' type='submit' name='action' value='set_admin_meta'>Сохранить свойства</button>
</form>
<form method='post' action='/admin/action/' class='peer-edit'>
<input type='hidden' name='id' value='{esc(peer_id)}'>
<input type='hidden' name='next' value='/admin/peers/{quote(peer_id, safe="")}/'>
<label>Label:
<input name='label' maxlength='64' value='{esc(item.get("label") or "")}' placeholder='Новый label'>
</label>
<button class='btn sm' type='submit' name='action' value='set_label'>Сохранить Label</button>
</form>
<div class='kv-grid'>
<div class='kv'><span class='k'>Admin label</span><div class='v'><code>{esc(item.get('admin_label') or item.get('label') or '')}</code></div></div>
<div class='kv'><span class='k'>Admin comment</span><div class='v'>{esc(item.get('admin_comment') or '-')}</div></div>
<div class='kv'><span class='k'>ID</span><div class='v'><code>{esc(item.get('id') or '')}</code></div></div>
<div class='kv'><span class='k'>IP</span><div class='v'><code>{esc(item.get('allowed_ip') or '')}</code></div></div>
<div class='kv'><span class='k'>IP (реальный)</span><div class='v'><code>{esc(real_ip)}</code>{(" <span class='small'>" + esc(real_geo) + "</span>") if real_geo else ""}</div></div>
<div class='kv'><span class='k'>Uplink</span><div class='v'><form method='post' action='/admin/action/' class='actions' style='margin-top:2px'><input type='hidden' name='id' value='{esc(peer_id)}'><input type='hidden' name='allowed_ip' value='{esc(ip_short)}'><input type='hidden' name='next' value='/admin/peers/{quote(peer_id, safe="")}/'><button class='btn sm' type='submit' name='action' value='uplink_ams' {'disabled' if uplink == 'ams' else ''}>AMS</button><button class='btn sm' type='submit' name='action' value='uplink_nyc' {'disabled' if uplink == 'nyc' else ''}>NYC</button><button class='btn sm' type='submit' name='action' value='uplink_fra' {'disabled' if uplink == 'fra' else ''}>FRA</button></form></div></div>
<div class='kv'><span class='k'>Routing policy</span><div class='v'><form method='post' action='/admin/action/' class='actions' style='margin-top:2px'><input type='hidden' name='id' value='{esc(peer_id)}'><input type='hidden' name='next' value='/admin/peers/{quote(peer_id, safe="")}/'><input type='hidden' name='preferred_uplink' value='{esc(routing_view.get("preferred_uplink") or "ams")}'><select name='policy_mode'><option value='auto' {'selected' if routing_view.get('policy_mode') == 'auto' else ''}>Auto</option><option value='manual' {'selected' if routing_view.get('policy_mode') == 'manual' else ''}>Manual</option></select><button class='btn sm' type='submit' name='action' value='set_policy_mode'>Сохранить режим</button></form></div></div>
<div class='kv'><span class='k'>Preferred uplink</span><div class='v'><form method='post' action='/admin/action/' class='actions' style='margin-top:2px'><input type='hidden' name='id' value='{esc(peer_id)}'><input type='hidden' name='next' value='/admin/peers/{quote(peer_id, safe="")}/'><input type='hidden' name='current_policy_mode' value='{esc(routing_view.get("policy_mode") or "auto")}'><select name='preferred_uplink'>{admin_preferred_options_html}</select><button class='btn sm' type='submit' name='action' value='set_preferred_uplink'>Сохранить uplink</button></form></div></div>
<div class='kv'><span class='k'>Target edge intent</span><div class='v'><form method='post' action='/admin/action/' class='actions' style='margin-top:2px'><input type='hidden' name='id' value='{esc(peer_id)}'><input type='hidden' name='next' value='/admin/peers/{quote(peer_id, safe="")}/'><select name='ingress_edge'>{_edge_options_html(routing_view.get('ingress_edge') or 'edg')}</select><button class='btn sm' type='submit' name='action' value='set_ingress_edge'>Сохранить intent</button></form></div></div>
<div class='kv'><span class='k'>Ingress edge</span><div class='v'><code>{esc(routing_view.get('ingress_edge') or '-')}</code></div></div>
<div class='kv'><span class='k'>Policy mode</span><div class='v'><code>{esc(routing_view.get('policy_mode') or '-')}</code></div></div>
<div class='kv'><span class='k'>Preferred uplink</span><div class='v'><code>{esc(routing_view.get('preferred_uplink') or '-')}</code></div></div>
<div class='kv'><span class='k'>Active uplink</span><div class='v'><code>{esc(routing_view.get('active_uplink') or '-')}</code></div></div>
<div class='kv'><span class='k'>Failover uplink</span><div class='v'><code>{esc(routing_view.get('failover_uplink') or '-')}</code></div></div>
<div class='kv'><span class='k'>Routing health</span><div class='v'><code>{esc(routing_view.get('health_status') or '-')}</code>{(" <span class='small'>" + esc(routing_view.get('health_note') or '') + "</span>") if routing_view.get('health_note') else ""}</div></div>
<div class='kv'><span class='k'>Тип</span><div class='v'><form method='post' action='/admin/action/' class='actions' style='margin-top:2px'><input type='hidden' name='id' value='{esc(peer_id)}'><input type='hidden' name='next' value='/admin/peers/{quote(peer_id, safe="")}/'><select name='client_type'><option value='admin' {'selected' if client_type == 'admin' else ''}>Админ</option><option value='employee' {'selected' if client_type == 'employee' else ''}>Сотрудник</option><option value='vip' {'selected' if client_type == 'vip' else ''}>ВИП</option><option value='normal' {'selected' if client_type == 'normal' else ''}>Обычный</option></select><button class='btn sm' type='submit' name='action' value='set_type'>Сохранить тип</button></form></div></div>
<div class='kv'><span class='k'>Создан</span><div class='v'><code>{esc(fmt_dt_ui(item.get('created_at') or ''))}</code></div></div>
{("<div class='kv'><span class='k'>Connected</span><div class='v'><code>" + esc(fmt_dt_ui(item.get('connected_at') or '')) + "</code></div></div><div class='kv'><span class='k'>Last handshake</span><div class='v'><code>" + esc(fmt_dt_ui(item.get('last_handshake_at') or '')) + "</code></div></div>") if is_active_status else ("<div class='kv'><span class='k'>Истекает</span><div class='v'><code>" + esc(fmt_dt_ui(item.get('expires_at') or '')) + "</code></div></div>")}
<div class='kv'><span class='k'>RX bytes</span><div class='v'><code>{esc(item.get('rx_bytes') or 0)}</code></div></div>
<div class='kv'><span class='k'>TX bytes</span><div class='v'><code>{esc(item.get('tx_bytes') or 0)}</code></div></div>
</div>
</div>
<div class='card'>
<h3>Routing policy vs effective</h3>
<div class='kv-grid'>
<div class='kv'><span class='k'>Ingress edge (policy)</span><div class='v'><code>{esc(routing_view.get('ingress_edge') or '-')}</code></div></div>
<div class='kv'><span class='k'>Effective edge</span><div class='v'><code>{esc(routing_view.get('effective_edge') or '-')}</code></div></div>
<div class='kv'><span class='k'>Policy mode</span><div class='v'><code>{esc(routing_view.get('policy_mode') or '-')}</code></div></div>
<div class='kv'><span class='k'>Preferred uplink</span><div class='v'><code>{esc(routing_view.get('preferred_uplink') or '-')}</code></div></div>
<div class='kv'><span class='k'>Effective uplink</span><div class='v'><code>{esc(routing_view.get('effective_uplink') or '-')}</code></div></div>
<div class='kv'><span class='k'>Failover uplink</span><div class='v'><code>{esc(routing_view.get('failover_uplink') or '-')}</code></div></div>
<div class='kv'><span class='k'>Routing state</span><div class='v'><code>{esc(routing_drift.get('label') or '-')}</code>{(" <span class='small'>" + esc(routing_drift.get('code') or '') + "</span>") if routing_drift.get('code') else ""}</div></div>
<div class='kv'><span class='k'>Edge drift</span><div class='v'><code>{esc(edge_drift_note)}</code></div></div>
<div class='kv'><span class='k'>Routing note</span><div class='v'><code>{esc(routing_drift.get('note') or '-')}</code></div></div>
</div>
</div>
<div class='card'>
<h3>Seamless move readiness</h3>
<div class='kv-grid'>
<div class='kv'><span class='k'>Current state</span><div class='v'><code>{esc(seamless_label)}</code></div></div>
<div class='kv'><span class='k'>Effective edge</span><div class='v'><code>{esc(effective_edge or '-')}</code></div></div>
<div class='kv'><span class='k'>Target edge intent</span><div class='v'><code>{esc(ingress_edge or '-')}</code></div></div>
<div class='kv'><span class='k'>Recent handshake</span><div class='v'><code>{esc('yes' if hs_recent else 'no')}</code></div></div>
<div class='kv'><span class='k'>Peer active</span><div class='v'><code>{esc('yes' if is_active_status else 'no')}</code></div></div>
<div class='kv'><span class='k'>Supported pair</span><div class='v'><code>{esc('yes' if seamless_supported else 'no')}</code></div></div>
<div class='kv'><span class='k'>Note</span><div class='v'><code>{esc(seamless_note)}</code></div></div>
</div>
<p class='small'>This is the operator prep layer for future in-place move. Execution path is still profile-based migration/reissue.</p>
</div>
<div class='card'>
<h3>Migration / reissue to target edge</h3>
<p class='small'>Этот сценарий выдаёт новый профиль на выбранном gateway. Старый peer остаётся активным до отдельного удаления после подтверждённого cutover.</p>
<form method='post' action='/admin/action/' class='actions' id='admin-migrate-form'>
<input type='hidden' name='id' value='{esc(peer_id)}'>
<input type='hidden' name='next' value='/admin/peers/{quote(peer_id, safe="")}/'>
<label>Gateway:
<select name='gateway' id='admin-migrate-gateway'>{migrate_gateway_options_html}</select>
</label>
<label>Uplink:
<select name='uplink' id='admin-migrate-uplink' data-edg-options="{esc(migrate_uplink_edg_html)}" data-vrn-options="{esc(migrate_uplink_vrn_html)}">{migrate_uplink_vrn_html if migrate_default_gateway == 'vrn' else migrate_uplink_edg_html}</select>
</label>
<button class='btn sm' type='submit' name='action' value='migrate_gateway' onclick="return confirm('Выдать новый профиль на выбранном gateway? Старый peer останется активным до отдельного удаления.');">Выдать migration profile</button>
</form>
<p class='small' id='admin-migrate-note'>{'Для VRN доступны AMS / FRA / NYC.' if migrate_default_gateway == 'vrn' else 'Для EDG доступны AMS / FRA / NYC.'}</p>
</div>
<div class='card'>
<h3>Migration state</h3>
<form method='post' action='/admin/action/' class='peer-edit'>
<input type='hidden' name='id' value='{esc(peer_id)}'>
<input type='hidden' name='next' value='/admin/peers/{quote(peer_id, safe="")}/'>
<input type='hidden' name='action' value='set_migration_meta'>
<label>Wave:
<input name='migration_wave' maxlength='64' value='{esc(migration_state.get("wave") or "")}' placeholder='vrn-wave-a'>
</label>
<label>Note:
<input name='migration_note' maxlength='2000' value='{esc(migration_state.get("note") or "")}' placeholder='операторская заметка по cutover'>
</label>
<button class='btn sm' type='submit'>Сохранить meta</button>
</form>
<form method='post' action='/admin/action/' class='actions' style='margin-top:10px'>
<input type='hidden' name='id' value='{esc(peer_id)}'>
<input type='hidden' name='next' value='/admin/peers/{quote(peer_id, safe="")}/'>
<input type='hidden' name='action' value='set_migration_checklist'>
<label><input type='checkbox' name='migration_issued' value='1' {'checked' if migration_checklist.get('issued') else ''}> issued</label>
<label><input type='checkbox' name='migration_client_imported' value='1' {'checked' if migration_checklist.get('client_imported') else ''}> client imported</label>
<label><input type='checkbox' name='migration_validated' value='1' {'checked' if migration_checklist.get('validated') else ''}> validated</label>
<label><input type='checkbox' name='migration_old_removed' value='1' {'checked' if migration_checklist.get('old_removed') else ''}> old removed</label>
<button class='btn sm' type='submit'>Сохранить checklist</button>
</form>
<p class='small'>Current wave: <code>{esc(migration_state.get("wave") or "-")}</code></p>
<p class='small'>Current note: <code>{esc(migration_state.get("note") or "-")}</code></p>
</div>
<div class='card'>
<h3>Связанные события</h3>
<div style='overflow:auto'>
<table>
<tr><th>Время</th><th>Событие</th><th>IP</th><th>Action</th><th>Причина</th></tr>
{ev_table}
</table>
</div>
</div>
<p><a class='btn' href='/admin/peers/'>← К списку клиентов</a></p>
<script>
(() => {{
  const gateway = document.getElementById('admin-migrate-gateway');
  const uplink = document.getElementById('admin-migrate-uplink');
  const note = document.getElementById('admin-migrate-note');
  if (!gateway || !uplink) return;
  const sync = () => {{
    const mode = String(gateway.value || 'edg').toLowerCase();
    uplink.innerHTML = mode === 'vrn' ? (uplink.dataset.vrnOptions || '') : (uplink.dataset.edgOptions || '');
    if (note) {{
      note.textContent = mode === 'vrn'
        ? 'Для VRN доступны AMS / FRA / NYC.'
        : 'Для EDG доступны AMS / FRA / NYC.';
    }}
  }};
  gateway.addEventListener('change', sync);
  sync();
}})();
</script>
"""
        return self._send_html(page("WG Admin Peer", body), headers=headers)

    def _render_admin_event_detail(self, event_id_raw, headers=None):
        try:
            event_id = int(str(event_id_raw or "0").strip())
        except Exception:
            event_id = 0
        events_data = backend_list_events(limit=AUDIT_MAX_LINES)
        if not events_data.get("ok"):
            return self._send_html(page("WG Admin Event", f"<div class='card'><b>Ошибка events:</b> {esc(events_data.get('error'))}</div>"), 500, headers=headers)
        events = list(events_data.get("items", []))
        item = None
        for e in events:
            if int(e.get("_event_id") or 0) == event_id:
                item = e
                break
        if not item:
            body = f"""
{self._admin_nav("events")}
<div class='card warn'>
<h2>Событие не найдено</h2>
<p>ID: <code>{esc(event_id)}</code></p>
</div>
<p><a class='btn' href='/admin/events/'>← К списку событий</a></p>
"""
            return self._send_html(page("WG Admin Event", body), 404, headers=headers)

        peer_id = str(item.get("peer_id") or "").strip()
        peer_link = (
            f"<a href='/admin/peers/{quote(peer_id, safe='')}/'><code>{esc(peer_id)}</code></a>"
            if peer_id
            else "<code></code>"
        )
        pretty = json.dumps(item, ensure_ascii=False, indent=2)
        body = f"""
{self._admin_nav("events")}
<div class='card'>
<h2>Детали события</h2>
<ul>
<li><b>ID:</b> <code>{esc(event_id)}</code></li>
<li><b>Время:</b> <code>{esc(fmt_dt_ui(item.get('ts') or ''))}</code></li>
<li><b>Event:</b> <code>{esc(one_line(item.get('event') or ''))}</code></li>
<li><b>Peer:</b> {peer_link}</li>
<li><b>IP:</b> <code>{esc(one_line(item.get('ip') or ''))}</code></li>
<li><b>Action:</b> <code>{esc(one_line(item.get('action') or ''))}</code></li>
<li><b>Reason:</b> <code>{esc(one_line(item.get('reason') or ''))}</code></li>
<li><b>Error:</b> <code>{esc(one_line(item.get('error') or ''))}</code></li>
</ul>
</div>
<div class='card'>
<h3>Полная запись (JSON)</h3>
<pre>{esc(pretty)}</pre>
</div>
<p><a class='btn' href='/admin/events/'>← К списку событий</a></p>
"""
        return self._send_html(page("WG Admin Event", body), headers=headers)

    def _render_admin_live(self, headers=None):
        data = backend_list()
        if not data.get("ok"):
            return self._send_html(page("WG Admin Live", f"<div class='card'><b>Ошибка:</b> {esc(data.get('error'))}</div>"), 500, headers=headers)
        current_scope = str(ADMIN_DATA_SCOPE.get() or "all").strip().lower()
        scope_label = self._admin_scope_label(current_scope)
        live = self._build_live_data(data.get("items", []), scope=current_scope)
        live_rows = []
        for x in live.get("online", []):
            hs_age = x.get("handshake_age_sec")
            rx_rate = x.get("rx_rate_bps")
            tx_rate = x.get("tx_rate_bps")
            uplink = str(x.get("uplink") or "ams")
            peer_id = str(x.get("id") or "")
            peer_href = "/admin/peers/" + quote(str(x.get("id") or ""), safe="") + "/"
            uplink_buttons = (
                "<form method='post' action='/admin/action/' class='actions'>"
                f"<input type='hidden' name='id' value='{esc(peer_id)}'>"
                f"<input type='hidden' name='allowed_ip' value='{esc(x.get('ip'))}'>"
                f"<input type='hidden' name='next' value='/admin/live/?scope={esc(current_scope)}'>"
                f"<button class='btn sm' type='submit' name='action' value='uplink_ams' {'disabled' if uplink == 'ams' else ''}>AMS</button>"
                f"<button class='btn sm' type='submit' name='action' value='uplink_nyc' {'disabled' if uplink == 'nyc' else ''}>NYC</button>"
                f"<button class='btn sm' type='submit' name='action' value='uplink_fra' {'disabled' if uplink == 'fra' else ''}>FRA</button>"
                "</form>"
                if WG_PORTAL_UPLINK_ENABLED and x.get("ip")
                else "<span class='small'>-</span>"
            )
            live_rows.append(
                "<tr>"
                f"<td><a href='{peer_href}'><b>{esc(x.get('admin_label') or x.get('label'))}</b></a><div class='small'>{esc(x.get('ip'))}</div></td>"
                f"<td><div><code>{esc(x.get('endpoint_ip') or '-')}</code></div>" + (f"<div class='small'>{esc(x.get('endpoint_geo') or '')}</div>" if x.get("endpoint_geo") else "") + "</td>"
                f"<td><code>{esc(hs_age)}</code> сек</td>"
                f"<td><code>{esc(x.get('rx_bytes'))}</code></td>"
                f"<td><code>{esc(x.get('tx_bytes'))}</code></td>"
                f"<td><code>{esc('' if rx_rate is None else f'{rx_rate:.1f}')}</code></td>"
                f"<td><code>{esc('' if tx_rate is None else f'{tx_rate:.1f}')}</code></td>"
                f"<td>{uplink_buttons}</td>"
                "</tr>"
            )
        live_online_table = "".join(live_rows) or "<tr><td colspan='8' class='small'>Сейчас нет активных peer.</td></tr>"
        live_ev_rows = []
        for e in live.get("events", []):
            live_ev_rows.append(
                "<tr>"
                f"<td><code>{esc(e.get('ts'))}</code></td>"
                f"<td><code>{esc(e.get('event'))}</code></td>"
                f"<td><code>{esc(e.get('admin_label') or e.get('label') or e.get('peer_id') or '-')}</code></td>"
                f"<td><code>{esc(e.get('ip') or '-')}</code></td>"
                "</tr>"
            )
        live_events_table = "".join(live_ev_rows) or "<tr><td colspan='4' class='small'>Пока нет событий.</td></tr>"
        body = f"""
{self._admin_nav("live")}
<div class='card'>
<h2>Admin: live-мониторинг</h2>
<p class='small'>Автообновление каждые {esc(live.get("poll_sec"))} сек. Онлайн считается по handshake не старше {esc(live.get("online_window_sec"))} сек.</p>
<p class='small'>Current scope: <code>{esc(scope_label)}</code>. Пустой список в этом разделе означает, что в выбранном scope сейчас нет peer-ов со свежим handshake в этом окне, а не то, что peer потерян.</p>
<p class='small'>Онлайн сейчас: <b id='live-online-count'>{esc(live.get("online_count"))}</b> | Обновлено: <code id='live-ts'>{esc(fmt_dt_ui(live.get("ts")))}</code></p>
<div style='overflow:auto'>
<table>
<tr><th>Peer</th><th>WG Endpoint IP</th><th>Возраст HS</th><th>RX байт</th><th>TX байт</th><th>RX Б/с</th><th>TX Б/с</th><th>Uplink</th></tr>
<tbody id='live-online-body'>{live_online_table}</tbody>
</table>
</div>
<div style='overflow:auto;margin-top:10px'>
<table>
<tr><th>Время</th><th>Событие</th><th>Peer</th><th>IP</th></tr>
<tbody id='live-events-body'>{live_events_table}</tbody>
</table>
</div>
</div>
<script>
(() => {{
  const POLL_SEC = {int(ADMIN_LIVE_POLL_SEC)};
  function escHtml(s) {{
    return String(s ?? '').replace(/[&<>"']/g, (m) => ({{'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}}[m]));
  }}
  function fmtRate(v) {{
    if (v === null || v === undefined || Number.isNaN(Number(v))) return '';
    return Number(v).toFixed(1);
  }}
  function uplinkButtons(x) {{
    const uplink = String(x.uplink || 'ams').toLowerCase();
    const peerId = escHtml(String(x.id || ''));
    const allowedIp = escHtml(String(x.ip || ''));
    if (!allowedIp) return "<span class='small'>-</span>";
    const btn = (name, label) => "<button class='btn sm' type='submit' name='action' value='uplink_" + name + "'" + (uplink === name ? " disabled" : "") + ">" + label + "</button>";
    return "<form method='post' action='/admin/action/' class='actions'>"
      + "<input type='hidden' name='id' value='" + peerId + "'>"
      + "<input type='hidden' name='allowed_ip' value='" + allowedIp + "'>"
      + "<input type='hidden' name='next' value='/admin/live/?scope=" + encodeURIComponent(LIVE_SCOPE) + "'>"
      + btn('ams', 'AMS')
      + btn('nyc', 'NYC')
      + btn('fra', 'FRA')
      + "</form>";
  }}
  const LIVE_SCOPE = {json.dumps(current_scope)};
  async function refreshLive() {{
    try {{
      const r = await fetch('/admin/live/data/?scope=' + encodeURIComponent(LIVE_SCOPE), {{ credentials: 'same-origin', cache: 'no-store' }});
      if (!r.ok) return;
      const data = await r.json();
      if (!data || !data.ok) return;
      const countEl = document.getElementById('live-online-count');
      if (countEl) countEl.textContent = String(data.online_count ?? 0);
      const tsEl = document.getElementById('live-ts');
      if (tsEl) tsEl.textContent = String(data.ts || '');
      const onlineBody = document.getElementById('live-online-body');
      if (onlineBody) {{
        const rows = Array.isArray(data.online) ? data.online : [];
        if (!rows.length) {{
          onlineBody.innerHTML = "<tr><td colspan='8' class='small'>Сейчас нет активных peer.</td></tr>";
        }} else {{
          onlineBody.innerHTML = rows.map((x) => {{
            const hsAge = (x.handshake_age_sec === null || x.handshake_age_sec === undefined) ? '' : String(x.handshake_age_sec);
            const peerHref = '/admin/peers/' + encodeURIComponent(String(x.id || '')) + '/';
            return "<tr>"
              + "<td><a href='" + peerHref + "'><b>" + escHtml(x.label || '') + "</b></a><div class='small'>" + escHtml(x.ip || '') + "</div></td>"
              + "<td><div><code>" + escHtml(x.endpoint_ip || '-') + "</code></div>" + (x.endpoint_geo ? "<div class='small'>" + escHtml(x.endpoint_geo) + "</div>" : "") + "</td>"
              + "<td><code>" + escHtml(hsAge) + "</code> сек</td>"
              + "<td><code>" + escHtml(x.rx_bytes || 0) + "</code></td>"
              + "<td><code>" + escHtml(x.tx_bytes || 0) + "</code></td>"
              + "<td><code>" + escHtml(fmtRate(x.rx_rate_bps)) + "</code></td>"
              + "<td><code>" + escHtml(fmtRate(x.tx_rate_bps)) + "</code></td>"
              + "<td>" + uplinkButtons(x) + "</td>"
              + "</tr>";
          }}).join('');
        }}
      }}
      const eventsBody = document.getElementById('live-events-body');
      if (eventsBody) {{
        const rows = Array.isArray(data.events) ? data.events : [];
        if (!rows.length) {{
          eventsBody.innerHTML = "<tr><td colspan='4' class='small'>Пока нет событий.</td></tr>";
        }} else {{
          eventsBody.innerHTML = rows.map((x) => {{
            const peer = x.peer_id || x.label || '-';
            const ip = x.ip || '-';
            return "<tr>"
              + "<td><code>" + escHtml(x.ts || '') + "</code></td>"
              + "<td><code>" + escHtml(x.event || '') + "</code></td>"
              + "<td><code>" + escHtml(peer) + "</code></td>"
              + "<td><code>" + escHtml(ip) + "</code></td>"
              + "</tr>";
          }}).join('');
        }}
      }}
    }} catch (e) {{
      // silent
    }}
  }}
  setInterval(refreshLive, Math.max(5, POLL_SEC) * 1000);
}})();
</script>
{back_home_link()}
"""
        return self._send_html(page("WG Admin Live", body), headers=headers)

    def _render_admin_denied(self, reason):
        body = f"""
<div class='card warn'>
<h2>Admin: доступ закрыт</h2>
<p><b>Причина:</b> {esc(reason)}</p>
<p class='small'>Откройте URL вида <code>/admin/?token=***</code>.</p>
</div>
{back_home_link()}
"""
        return self._send_html(page("WG Admin", body), 403)

    def _render_lk(self, peer_id_from_query, lk_token_from_query):
        data = backend_list_for_lk()
        if not data.get("ok"):
            return self._send_html(page("WG LK", f"<div class='card'><b>Ошибка:</b> {esc(data.get('error'))}</div>{back_home_link()}"), 500)
        auth = self._resolve_lk_access(data, peer_id_from_query=peer_id_from_query, lk_token_from_query=lk_token_from_query, require_active=True)
        headers = list(auth.get("headers") or []) or None
        if not auth.get("ok"):
            body = f"""
<div class='card warn'>
<h2>Личный кабинет</h2>
<p><b>Доступ ограничен:</b> WireGuard-подключение не определено.</p>
<p class='small'>Для входа в ЛК должно быть активно именно ваше WG-подключение. Ручной ID/Code больше не является основным сценарием.</p>
</div>
{back_home_link()}
"""
            return self._send_html(page("WG LK", body), 403)
        item = dict(auth.get("item") or {})
        peer_id = str(auth.get("peer_id") or item.get("id") or "").strip()
        now_unix = int(dt.datetime.now(dt.timezone.utc).timestamp())
        hs_unix = int(item.get("last_handshake_unix") or 0)
        is_active = self._is_peer_active_now(item, now_unix)
        hs_age = (now_unix - hs_unix) if hs_unix > 0 else None

        if not is_active:
            body = f"""
<div class='card warn'>
<h2>Личный кабинет</h2>
<p><b>У вас не включен WireGuard.</b> Доступ к кабинету закрыт до активного подключения.</p>
<p class='small'>Профиль: <code>{esc(peer_id)}</code> | Last Handshake: <code>{esc(item.get('last_handshake_at'))}</code></p>
</div>
{back_home_link()}
"""
            return self._send_html(page("WG LK", body), 403, headers=headers)

        routing_lk = backend_get_peer_routing_for_lk(peer_id)
        current_lk_uplink = "ams"
        current_lk_edge = "edg"
        if routing_lk.get("ok"):
            current_lk_uplink = str(routing_lk.get("effective_uplink") or routing_lk.get("preferred_uplink") or "ams").strip().lower() or "ams"
            current_lk_edge = str(routing_lk.get("effective_edge") or routing_lk.get("ingress_edge") or "edg").strip().lower() or "edg"
        lk_uplink_options = [
            ("ams", _uplink_label_for_edge(current_lk_edge, "ams")),
            ("fra", _uplink_label_for_edge(current_lk_edge, "fra")),
            ("nyc", _uplink_label_for_edge(current_lk_edge, "nyc")),
        ]
        lk_uplink_options_html = "".join(
            f"<option value='{esc(value)}' {'selected' if current_lk_uplink == value else ''}>{esc(title)}</option>"
            for value, title in lk_uplink_options
        )
        reissue_form = f"""
<form method='post' action='/lk/reissue/' class='actions' style='margin:0'>
  <label>Шлюз:
    <select name='gateway'>
      {_edge_options_html(current_lk_edge)}
    </select>
  </label>
  <label>Аплинк:
    <select name='uplink'>{lk_uplink_options_html}</select>
  </label>
  <button class='btn sm' type='submit' onclick=\"return confirm('Выдать новый профиль на выбранном gateway? Старый останется активным до отдельного отключения.');\">Перевыпустить профиль</button>
</form>
"""
        bill = billing_latest_for_peer(peer_id, limit=WG_BILLING_LIST_LIMIT)
        bill_item = bill.get("item") if bill.get("ok") else None
        bill_error = "" if bill.get("ok") else str(bill.get("error") or "billing_unavailable")
        payment_block = (
            f"<p><a class='btn' href='{esc(PAYMENT_URL)}' target='_blank' rel='noopener'>Оплатить / продлить</a></p>"
            if PAYMENT_URL
            else ""
        )
        bill_html = ""
        if bill_error:
            bill_html = f"<p class='warn'><b>Ошибка billing:</b> {esc(bill_error)}</p>"
        elif not bill_item:
            bill_html = "<p>Заказов пока нет.</p>"
        else:
            bo_id = str(bill_item.get("id") or "")
            bo_status = str(bill_item.get("status") or "")
            bo_url = str(bill_item.get("payment_url") or "")
            bo_amount = str(bill_item.get("amount_rub") or "")
            bo_created = fmt_dt_ui(bill_item.get("created_at") or "")
            bo_expires = fmt_dt_ui(bill_item.get("expires_at") or "")
            bo_paid = fmt_dt_ui(bill_item.get("paid_at") or "")
            pay_btn = (
                f"<p><a class='btn' href='{esc(bo_url)}' target='_blank' rel='noopener'>Оплатить заказ</a></p>"
                if bo_url and bo_status in ("NEW", "AWAITING_PAYMENT")
                else ""
            )
            bill_html = f"""
<ul>
<li>Order ID: <code>{esc(bo_id)}</code></li>
<li>Статус: <code>{esc(billing_status_ru(bo_status))}</code> (<code>{esc(bo_status)}</code>)</li>
<li>Сумма: <code>{esc(bo_amount)} RUB</code></li>
<li>Создан: <code>{esc(bo_created)}</code></li>
<li>Истекает: <code>{esc(bo_expires)}</code></li>
<li>Оплачен: <code>{esc(bo_paid)}</code></li>
</ul>
{pay_btn}
"""
        fallback_block = ""
        if WG_BILLING_FALLBACK_URL:
            fb = WG_BILLING_FALLBACK_URL
            fb_qr = "https://api.qrserver.com/v1/create-qr-code/?size=220x220&data=" + quote(fb, safe="")
            fallback_block = f"""
<div class='card'>
<h4 style='margin-top:0'>Временная оплата (многоразовая ссылка)</h4>
<p class='small'>Если создание одноразового QR временно недоступно, можно использовать резервный способ оплаты.</p>
<p><a class='btn' href='{esc(fb)}' target='_blank' rel='noopener'>Оплатить по резервной ссылке</a></p>
<p class='small'><code>{esc(fb)}</code></p>
<img alt='QR резервной оплаты' style='max-width:220px;width:100%;border:1px solid #e2e8f0;border-radius:8px' src='{esc(fb_qr)}'>
</div>
"""

        body = f"""
<div class='card ok'>
<h2>Личный кабинет</h2>
<p><b>WireGuard сейчас включен.</b></p>
<p class='small'>Порог проверки активности: {ACTIVE_WINDOW_SEC} сек по времени последнего handshake.</p>
<p class='small'>Авторизация: {_lk_mode_badge(auth.get("via"))}</p>
</div>
<div class=card>
<h3>Статистика подключения</h3>
<ul>
<li>ID: <code>{esc(item.get('id'))}</code></li>
<li>IP: <code>{esc(item.get('allowed_ip'))}</code></li>
<li>Status: <code>{esc(item.get('status'))}</code></li>
<li>Connected at: <code>{esc(fmt_dt_ui(item.get('connected_at')))}</code></li>
<li>Last handshake: <code>{esc(fmt_dt_ui(item.get('last_handshake_at')))}</code></li>
<li>Возраст последнего handshake: <code>{esc(hs_age)} сек</code></li>
</ul>
</div>
<div class='card'>
<h3>Маршрут</h3>
<form method='post' action='/lk/uplink/' class='actions'>
  <label>Аплинк:
    <select name='uplink'>{lk_uplink_options_html}</select>
  </label>
  <button class='btn sm' type='submit'>Сменить аплинк</button>
</form>
<p class='small'>Текущий uplink: <code>{esc(current_lk_uplink)}</code></p>
</div>
<div class=card>
<h3>Биллинг</h3>
{bill_html}
<p class='actions'>
  <form method='post' action='/lk/billing/create/' style='margin:0'>
    <button class='btn sm' type='submit'>Создать заказ</button>
  </form>
  <form method='post' action='/lk/billing/refresh/' style='margin:0'>
    <button class='btn sm' type='submit'>Обновить статус</button>
  </form>
</p>
{payment_block}
{fallback_block}
</div>
<div class='card'>
<h3>Управление</h3>
<p class='actions'>
  <a class='btn sm' href='/check/'>Проверка</a>
  <a class='btn sm' href='/logout/'>Выйти</a>
</p>
<p class='actions'>
  {reissue_form}
</p>
</div>
{back_home_link()}
"""
        return self._send_html(page("WG LK", body), headers=headers)

    def _render_check(self):
        data = backend_list()
        if not data.get("ok"):
            return self._send_html(page("WG Check", f"<div class='card'><b>Ошибка:</b> {esc(data.get('error'))}</div>{back_home_link()}"), 500)

        now = dt.datetime.now(dt.timezone.utc)
        now_unix = int(now.timestamp())
        now_local = now.astimezone(DISPLAY_TZ)

        body_top = f"""
<div class='card'>
<h2>Проверка подключения</h2>
<ul>
<li>Время сервера ({esc(WG_PORTAL_DISPLAY_TZ)}): <code>{esc(now_local.replace(microsecond=0).strftime('%Y-%m-%d %H:%M:%S %Z'))}</code></li>
<li>Ваш IP (по данным портала): <code>{esc(self._client_ip())}</code></li>
</ul>
</div>
"""

        auth = self._resolve_lk_access(data, require_active=False)
        if not auth.get("ok"):
            body = (
                body_top
                + f"""
<div class='card warn'>
<p><b>Профиль не определен.</b> Для проверки нужно активное WireGuard-подключение.</p>
</div>
{back_home_link()}
"""
            )
            return self._send_html(page("WG Check", body), 403)

        item = dict(auth.get("item") or {})
        peer_id = str(auth.get("peer_id") or item.get("id") or "").strip()
        if not item:
            body = body_top + f"""
<div class='card warn'>
<p><b>Профиль не найден:</b> <code>{esc(peer_id)}</code></p>
</div>
{back_home_link()}
"""
            return self._send_html(page("WG Check", body), 403)

        hs_unix = int(item.get("last_handshake_unix") or 0)
        is_active = self._is_peer_active_now(item, now_unix)
        hs_age = (now_unix - hs_unix) if hs_unix > 0 else None

        status_card = (
            f"""
<div class='card ok'>
<p><b>WireGuard сейчас включен.</b></p>
<p class='small'>Порог активности: {ACTIVE_WINDOW_SEC} сек по времени последнего handshake.</p>
</div>
"""
            if is_active
            else f"""
<div class='card warn'>
<p><b>WireGuard сейчас не виден (нет свежего handshake).</b></p>
<p class='small'>Порог активности: {ACTIVE_WINDOW_SEC} сек по времени последнего handshake.</p>
</div>
"""
        )

        wg_dns = (os.getenv("WG_DNS", "") or "").strip()
        wg_endpoint = (os.getenv("WG_ENDPOINT", "") or "").strip()
        hints = []
        if wg_endpoint:
            hints.append(f"<li>Endpoint (из env): <code>{esc(wg_endpoint)}</code></li>")
        if wg_dns:
            hints.append(f"<li>DNS (из env): <code>{esc(wg_dns)}</code></li>")
        hints_html = "<ul>" + "".join(hints) + "</ul>" if hints else "<p class='small'>Подсказки (DNS/Endpoint) не настроены в env.</p>"

        body = (
            body_top
            + status_card
            + f"""
<div class='card'>
<h3>Профиль</h3>
<ul>
<li>Admin label: <code>{esc(item.get('admin_label') or item.get('label'))}</code></li>
<li>System label: <code>{esc(item.get('label'))}</code></li>
<li>ID: <code>{esc(item.get('id'))}</code></li>
<li>IP: <code>{esc(str(item.get('allowed_ip') or '').split('/',1)[0])}</code></li>
<li>Status: <code>{esc(item.get('status'))}</code></li>
<li>Connected at: <code>{esc(fmt_dt_ui(item.get('connected_at')))}</code></li>
<li>Last handshake: <code>{esc(fmt_dt_ui(item.get('last_handshake_at')))}</code></li>
<li>Handshake age: <code>{esc(hs_age)} сек</code></li>
</ul>
</div>
<div class='card'>
<h3>Подсказки</h3>
{hints_html}
<p class='small'>Если handshake не появляется, проверьте что выбран правильный профиль WireGuard и он включен.</p>
</div>
<div class='card'>
<h3>Управление</h3>
<p class='actions'>
  <a class='btn sm' href='/logout/'>Выйти</a>
</p>
</div>
{back_home_link()}
"""
        )
        return self._send_html(page("WG Check", body))

    def log_message(self, fmt, *args):
        # Не логируем query-string, т.к. там могут быть token'ы.
        try:
            req = args[2]  # e.g. "GET /lk/?id=... HTTP/1.1"
            parts = req.split(" ")
            if len(parts) >= 2:
                parts[1] = parts[1].split("?", 1)[0]
                args = (args[0], args[1], " ".join(parts))
        except Exception:
            pass
        return super().log_message(fmt, *args)

    def do_GET(self):
        parsed = urlparse(self.path)
        p = parsed.path

        if p.startswith("/assets/"):
            return self._serve_static(p)

        if p in ("/", ""):
            return self._render_home()

        if p == "/new/":
            return self._redirect("/account/profiles/new", code=302)

        if p.startswith("/new/") and p.endswith("/") and p not in ("/new/",):
            peer_id = p[len("/new/") : -1]
            data = _load_issued_result(peer_id)
            if not data:
                return self._send_html(page("404", f"<div class='card'>Ссылка на профиль не найдена или уже недоступна.</div>{back_home_link()}"), 404)
            html, headers = _render_new_result_page(data)
            return self._send_html(html, headers=headers)

        if p == "/account/login":
            return self._render_account_login()

        if p == "/account/":
            return self._render_account_home()

        if p == "/account/profiles/new":
            return self._render_account_profile_new()

        if p.startswith("/account/profiles/") and p.endswith("/") and p not in ("/account/profiles/new", "/account/profiles/new/"):
            connection_profile_id = p[len("/account/profiles/") : -1]
            return self._render_account_profile_detail(connection_profile_id)

        if p == "/account/claims/new":
            return self._render_account_claim_new()

        if p in (
            "/admin/",
            "/admin/peers/",
            "/admin/peers.csv/",
            "/admin/edges/",
            "/admin/waves/",
            "/admin/waves.csv/",
            "/admin/uplinks/",
            "/admin/events/",
            "/admin/events.csv/",
            "/admin/live/",
            "/admin/live/data/",
        ) or p.startswith("/admin/peers/") or p.startswith("/admin/edges/") or p.startswith("/admin/events/"):
            ok, headers, reason = self._admin_auth(parsed)
            if not ok:
                return self._redirect("/", code=302)
            scope, scope_headers = self._admin_data_scope(parsed)
            mode = self._scope_to_read_mode(scope)
            headers = list(headers or []) + list(scope_headers or [])
            token = ADMIN_READ_MODE.set(mode)
            scope_token = ADMIN_DATA_SCOPE.set(scope)
            try:
                if p == "/admin/":
                    return self._render_admin_dashboard(headers=headers)
                if p == "/admin/peers/":
                    return self._render_admin_peers(parsed, headers=headers)
                if p == "/admin/peers.csv/":
                    return self._render_admin_peers_csv(parsed)
                if p == "/admin/edges/":
                    return self._render_admin_edges(headers=headers)
                if p.startswith("/admin/edges/") and p.endswith("/") and p not in ("/admin/edges/",):
                    edge_id = p[len("/admin/edges/") : -1]
                    return self._render_admin_edge_detail(edge_id, parsed=parsed, headers=headers)
                if p == "/admin/waves/":
                    return self._render_admin_waves(headers=headers)
                if p == "/admin/waves.csv/":
                    return self._render_admin_waves_csv(parsed)
                if p == "/admin/uplinks/":
                    return self._render_admin_uplinks(headers=headers)
                if p == "/admin/events/":
                    return self._render_admin_events(parsed, headers=headers)
                if p == "/admin/events.csv/":
                    return self._render_admin_events_csv(parsed)
                if p.startswith("/admin/peers/") and p.endswith("/") and p not in ("/admin/peers/",):
                    peer_id = p[len("/admin/peers/") : -1]
                    return self._render_admin_peer_detail(peer_id, headers=headers)
                if p.startswith("/admin/events/") and p.endswith("/") and p not in ("/admin/events/",):
                    event_id = p[len("/admin/events/") : -1]
                    return self._render_admin_event_detail(event_id, headers=headers)
                if p == "/admin/live/":
                    return self._render_admin_live(headers=headers)
                data = backend_list()
                if not data.get("ok"):
                    return self._send_json({"ok": False, "error": str(data.get("error") or "list_failed")}, 500, headers=headers)
                return self._send_json(self._build_live_data(data.get("items", []), scope=scope), headers=headers)
            finally:
                ADMIN_DATA_SCOPE.reset(scope_token)
                ADMIN_READ_MODE.reset(token)

        if p == "/lk/":
            # В URL намеренно НЕ принимаем секретный код (k), чтобы он не попадал
            # в историю браузера/рефералы. Recovery оставлен отдельным маршрутом.
            qs = parse_qs(parsed.query)
            return self._render_lk((qs.get("id") or [""])[0], "")

        if p == "/check/":
            return self._render_check()

        if p == "/help/":
            return self._render_help()

        if p in ("/login/", "/recover/"):
            qs = parse_qs(parsed.query)
            prefill_id = (qs.get("id") or [""])[0]
            body = f"""
<div class='card'>
<h2>Recovery доступ</h2>
<p class='small'>Основной сценарий: ЛК открывается автоматически по активному WG-подключению. Эта форма оставлена только как аварийный recovery path.</p>
</div>
{lk_login_form(prefill_id=prefill_id)}
{back_home_link()}
"""
            return self._send_html(page("JSTUN Recovery", body))

        if p == "/logout/":
            headers = [
                ("Set-Cookie", "wg_peer_id=; Path=/; Max-Age=0; SameSite=Lax; Secure; HttpOnly"),
                ("Set-Cookie", "wg_lk_token=; Path=/; Max-Age=0; SameSite=Lax; Secure; HttpOnly"),
                ("Set-Cookie", "wg_lk_ui=; Path=/; Max-Age=0; SameSite=Lax; Secure"),
            ]
            self._audit("logout", peer_id=self._cookies().get("wg_peer_id", ""))
            return self._redirect("/recover/", headers=headers)

        return self._send_html(page("404", f"<div class='card'>Not found</div>{back_home_link()}"), 404)

    def do_HEAD(self):
        return self.do_GET()

    def do_POST(self):
        p = urlparse(self.path).path

        if p == "/dlzip/":
            form = self._read_form()
            peer_id = safe_id((form.get("id", [""])[0] or "").strip())
            label = (form.get("label", [""])[0] or "").strip()
            cfg_b64 = (form.get("cfg_b64", [""])[0] or "").strip()
            if not cfg_b64:
                self._audit("dlzip_denied", reason="no_cfg", peer_id=peer_id, label=label)
                return self._send_html(page("WG Download", f"<div class='card warn'><b>Некорректный запрос:</b> нет конфига.</div>{back_home_link()}"), 400)
            try:
                cfg = base64.b64decode(cfg_b64.encode("ascii"), validate=True).decode("utf-8", errors="strict")
            except Exception:
                self._audit("dlzip_denied", reason="bad_cfg_b64", peer_id=peer_id, label=label)
                return self._send_html(page("WG Download", f"<div class='card warn'><b>Некорректный запрос:</b> не удалось прочитать конфиг.</div>{back_home_link()}"), 400)

            folder = safe_slug(label)
            zip_name = f"{folder}.zip"
            conf_name = f"{folder}.conf"
            arcname = f"{folder}/{conf_name}"

            buf = io.BytesIO()
            with zipfile.ZipFile(buf, "w", compression=zipfile.ZIP_DEFLATED) as zf:
                zf.writestr(arcname, cfg)
            zbytes = buf.getvalue()
            self.send_response(200)
            self.send_header("Content-Type", "application/zip")
            self.send_header("Content-Disposition", f'attachment; filename=\"{zip_name}\"')
            self.send_header("Content-Length", str(len(zbytes)))
            self.end_headers()
            self.wfile.write(zbytes)
            self._audit("dlzip_ok", peer_id=peer_id, zip=zip_name, arcname=arcname)
            return

        if p == "/account/login":
            svc = _account_lk_service()
            if svc is None:
                return self._send_html(page("Аккаунт", f"{self._account_lk_nav()}<div class='card warn'><b>Новый личный кабинет временно недоступен.</b></div>"), 503)
            form = self._read_form()
            account, session = svc.register_or_login_by_identity(
                provider=(form.get("provider", [""])[0] or "").strip(),
                subject=(form.get("subject", [""])[0] or "").strip(),
                email=(form.get("email", [""])[0] or "").strip(),
                display_name=(form.get("display_name", [""])[0] or "").strip(),
                ip=self._client_ip(),
                ua=(self.headers.get("User-Agent", "") or "")[:300],
                profile_payload={},
            )
            self._audit("account_login_ok", account_id=account.account_id, provider=(form.get("provider", [""])[0] or "").strip())
            headers = [
                ("Set-Cookie", f"{ACCOUNT_LK_SESSION_COOKIE}={session.session_token}; Path=/; Max-Age=2592000; SameSite=Lax; Secure; HttpOnly"),
            ]
            return self._redirect("/account/", headers=headers, code=303)

        if p == "/account/profile":
            svc = _account_lk_service()
            account = self._account_lk_current_account()
            if svc is None or account is None:
                return self._redirect("/account/login", code=302)
            form = self._read_form()
            display_name = (form.get("display_name", [""])[0] or "").strip()[:120]
            svc.store.update_account_profile(account.account_id, display_name=display_name or None)
            self._audit("account_profile_update_ok", account_id=account.account_id)
            return self._redirect("/account/", code=303)

        if p == "/account/profiles/new":
            svc = _account_lk_service()
            account = self._account_lk_current_account()
            if svc is None or account is None:
                return self._redirect("/account/login", code=302)
            form = self._read_form()
            display_label = (form.get("display_label", [""])[0] or "").strip()[:64] or "Мой профиль"
            gateway_req = (form.get("gateway", ["edg"])[0] or "edg").strip().lower()
            if not _is_supported_edge(gateway_req):
                gateway_req = "edg"
            uplink_req = (form.get("uplink", ["ams"])[0] or "ams").strip().lower()
            if uplink_req not in ("ams", "nyc", "fra"):
                uplink_req = "ams"
            issued = backend_create(display_label, gateway=gateway_req, uplink=uplink_req)
            if not issued.get("ok"):
                body = f"{self._account_lk_nav()}<div class='card warn'><b>Не удалось выпустить профиль:</b> {esc(issued.get('error') or 'unknown_error')}</div>"
                return self._send_html(page("Новый профиль", body), 400)
            peer_id = str(issued.get("id") or "").strip()
            issued["gateway"] = gateway_req
            issued["uplink"] = uplink_req
            issued["label"] = display_label
            _save_issued_result(peer_id, issued)
            routing = backend_get_peer_routing_for_lk(peer_id)
            profile = svc.register_issued_profile(
                account_id=account.account_id,
                legacy_peer_id=peer_id,
                display_label=display_label,
                system_label=display_label,
                admin_label=display_label,
                allowed_ip=str(issued.get("allowed_ip") or ""),
                status=str(issued.get("status") or "pending") or "pending",
                current_edge_id=str((routing.get("ingress_edge") if routing.get("ok") else "") or gateway_req),
                current_uplink_id=str((routing.get("active_uplink") if routing.get("ok") else "") or uplink_req),
                preferred_uplink_id=str((routing.get("preferred_uplink") if routing.get("ok") else "") or uplink_req),
                effective_uplink_id=str((routing.get("effective_uplink") if routing.get("ok") else "") or uplink_req),
                failover_uplink_id=str((routing.get("failover_uplink") if routing.get("ok") else "") or ""),
                routing_policy_mode=str((routing.get("policy_mode") if routing.get("ok") else "") or "auto"),
                runtime_source="account_issue",
            )
            self._audit(
                "account_profile_create_ok",
                account_id=account.account_id,
                connection_profile_id=profile.connection_profile_id,
                peer_id=peer_id,
                gateway=gateway_req,
                uplink=uplink_req,
            )
            return self._redirect(f"/account/profiles/{quote(profile.connection_profile_id, safe='')}/", code=303)

        if p.startswith("/account/profiles/") and p.endswith("/remove"):
            svc = _account_lk_service()
            account = self._account_lk_current_account()
            if svc is None or account is None:
                return self._redirect("/account/login", code=302)
            connection_profile_id = p[len("/account/profiles/") : -len("/remove")]
            connection_profile_id = str(connection_profile_id or "").strip().strip("/")
            profile = svc.store.get_connection_profile(connection_profile_id)
            if profile is None or str(profile.account_id or "") != str(account.account_id or ""):
                return self._send_html(page("Профиль подключения", f"{self._account_lk_nav()}<div class='card warn'><b>Профиль подключения не найден.</b></div>"), 404)
            peer_id = str(profile.legacy_peer_id or "").strip()
            if peer_id:
                out = backend_remove(peer_id)
                if not out.get("ok"):
                    return self._send_html(page("Профиль подключения", f"{self._account_lk_nav()}<div class='card warn'><b>Не удалось удалить профиль:</b> {esc(out.get('error') or 'unknown_error')}</div>"), 500)
            svc.store.update_connection_profile_metadata(connection_profile_id, status="removed")
            self._audit("account_profile_remove_ok", account_id=account.account_id, connection_profile_id=connection_profile_id, peer_id=peer_id)
            return self._redirect("/account/", code=303)

        if p == "/account/claims/new":
            svc = _account_lk_service()
            account = self._account_lk_current_account()
            if svc is None or account is None:
                return self._redirect("/account/login", code=302)
            form = self._read_form()
            legacy_peer_id = (form.get("legacy_peer_id", [""])[0] or "").strip()
            display_label = (form.get("display_label", [""])[0] or "").strip()
            claim_method = (form.get("claim_method", ["operator_attach"])[0] or "operator_attach").strip() or "operator_attach"
            claim = svc.attach_legacy_profile(
                account_id=account.account_id,
                legacy_peer_id=legacy_peer_id,
                display_label=display_label,
                claim_method=claim_method,
                proof_payload={"source": "portal_account_bridge"},
            )
            self._audit("account_claim_ok", account_id=account.account_id, legacy_peer_id=legacy_peer_id, profile_claim_id=claim.profile_claim_id)
            return self._redirect(f"/account/?attached_legacy_peer={quote(legacy_peer_id, safe='')}", code=303)

        if p in ("/login/", "/lk/login/", "/recover/"):
            form = self._read_form()
            peer_id = (form.get("id", [""])[0] or "").strip()
            k = (form.get("k", [""])[0] or "").strip()
            if not peer_id or not k:
                self._audit("lk_login_denied", reason="missing_fields", peer_id=peer_id)
                body = f"""
<div class='card warn'>
<h2>Личный кабинет</h2>
<p><b>Некорректный ввод:</b> укажите ID и код. Используйте это только как аварийный fallback, если WG-автоопределение недоступно.</p>
</div>
{lk_login_form()}
{back_home_link()}
"""
                return self._send_html(page("WG LK", body), 400)

            data = backend_list()
            if not data.get("ok"):
                return self._send_html(page("WG LK", f"<div class='card'><b>Ошибка:</b> {esc(data.get('error'))}</div>{back_home_link()}"), 500)

            item = None
            for x in data.get("items", []):
                if str(x.get("id", "")).strip() == peer_id:
                    item = x
                    break
            expected = str((item or {}).get("lk_token") or "").strip()
            if not item or not expected or k != expected:
                self._audit("lk_login_denied", reason="bad_id_or_code", peer_id=peer_id)
                body = f"""
<div class='card warn'>
<h2>Личный кабинет</h2>
<p><b>Доступ запрещен:</b> неверный ID или код.</p>
</div>
{lk_login_form()}
{back_home_link()}
"""
                return self._send_html(page("WG LK", body), 403)

            headers = [
                ("Set-Cookie", f"wg_peer_id={peer_id}; Path=/; Max-Age=31536000; SameSite=Lax; Secure; HttpOnly"),
                ("Set-Cookie", f"wg_lk_token={expected}; Path=/; Max-Age=31536000; SameSite=Lax; Secure; HttpOnly"),
                ("Set-Cookie", "wg_lk_ui=1; Path=/; Max-Age=31536000; SameSite=Lax; Secure"),
            ]
            self._audit("lk_login_ok", peer_id=peer_id)
            return self._redirect("/lk/", headers=headers)

        if p in ("/lk/billing/create/", "/lk/billing/refresh/"):
            data = backend_list()
            if not data.get("ok"):
                return self._send_html(page("JSTUN Billing", f"<div class='card'><b>Ошибка:</b> {esc(data.get('error'))}</div>{back_home_link()}"), 500)
            auth = self._resolve_lk_access(data, require_active=True)
            if not auth.get("ok"):
                self._audit("billing_denied", reason=str(auth.get("reason") or "auth_failed"), action=p)
                return self._redirect("/lk/")
            item = dict(auth.get("item") or {})
            peer_id = str(auth.get("peer_id") or item.get("id") or "").strip()

            now_unix = int(dt.datetime.now(dt.timezone.utc).timestamp())
            if not self._is_peer_active_now(item, now_unix):
                self._audit("billing_denied", reason="not_active", action=p, peer_id=peer_id)
                return self._send_html(
                    page("JSTUN Billing", f"<div class='card warn'><b>Нельзя выполнить billing-операцию:</b> WireGuard сейчас не активен.</div>{back_home_link()}"),
                    403,
                )

            if p == "/lk/billing/create/":
                self._audit("billing_create_attempt", peer_id=peer_id, label=str(item.get("label") or ""))
                # Перед проверкой активного заказа прогоняем одно обновление статусов,
                # чтобы локально истекшие AWAITING_PAYMENT не блокировали новый create.
                pre = billing_poll_once(limit=WG_BILLING_POLL_LIMIT)
                if not pre.get("ok"):
                    self._audit("billing_pre_refresh_error", peer_id=peer_id, error=str(pre.get("error") or ""))
                latest = billing_latest_for_peer(peer_id, limit=WG_BILLING_LIST_LIMIT)
                if latest.get("ok") and isinstance(latest.get("item"), dict):
                    lst = latest.get("item") or {}
                    st = str(lst.get("status") or "").upper()
                    if st in ("NEW", "AWAITING_PAYMENT"):
                        self._audit("billing_create_skip_existing", peer_id=peer_id, order_id=str(lst.get("id") or ""))
                        return self._send_html(
                            page(
                                "JSTUN Billing",
                                "<div class='card warn'><b>Новый заказ не создан:</b> уже есть активный заказ в ожидании оплаты.</div>"
                                "<p><a class='btn' href='/lk/'>Вернуться в ЛК</a></p>"
                                + back_home_link(),
                            ),
                            409,
                        )
                out = billing_create_order(peer_id, str(item.get("label") or ""))
                if not out.get("ok"):
                    self._audit("billing_create_error", peer_id=peer_id, error=str(out.get("error") or ""))
                    return self._send_html(
                        page("JSTUN Billing", f"<div class='card warn'><b>Ошибка создания заказа:</b> {esc(out.get('error'))}</div><p><a class='btn' href='/lk/'>Вернуться в ЛК</a></p>{back_home_link()}"),
                        500,
                    )
                self._audit(
                    "billing_create_ok",
                    peer_id=peer_id,
                    order_id=str(out.get("id") or ""),
                    payment_url=bool(str(out.get("payment_url") or "")),
                )
                return self._send_html(
                    page(
                        "JSTUN Billing",
                        "<div class='card ok'><b>Заказ создан.</b></div>"
                        "<p><a class='btn' href='/lk/'>Вернуться в ЛК</a></p>"
                        + back_home_link(),
                    )
                )

            self._audit("billing_refresh_attempt", peer_id=peer_id)
            out = billing_poll_once(limit=WG_BILLING_POLL_LIMIT)
            if not out.get("ok"):
                self._audit("billing_refresh_error", peer_id=peer_id, error=str(out.get("error") or ""))
                return self._send_html(
                    page("JSTUN Billing", f"<div class='card warn'><b>Ошибка обновления статуса:</b> {esc(out.get('error'))}</div><p><a class='btn' href='/lk/'>Вернуться в ЛК</a></p>{back_home_link()}"),
                    500,
                )
            self._audit(
                "billing_refresh_ok",
                peer_id=peer_id,
                checked=int(out.get("checked") or 0),
                paid=int(out.get("paid") or 0),
                failed=int(out.get("failed") or 0),
                expired=int(out.get("expired") or 0),
            )
            return self._send_html(
                page(
                    "JSTUN Billing",
                    f"<div class='card ok'><b>Статус обновлен.</b> checked={esc(out.get('checked'))}, paid={esc(out.get('paid'))}, failed={esc(out.get('failed'))}, expired={esc(out.get('expired'))}</div>"
                    "<p><a class='btn' href='/lk/'>Вернуться в ЛК</a></p>"
                    + back_home_link(),
                )
            )

        if p == "/lk/reissue/":
            data = backend_list()
            if not data.get("ok"):
                return self._send_html(page("WG Reissue", f"<div class='card'><b>Ошибка:</b> {esc(data.get('error'))}</div>{back_home_link()}"), 500)
            form = self._read_form()
            auth = self._resolve_lk_access(data, require_active=True)
            if not auth.get("ok"):
                self._audit("reissue_denied", reason=str(auth.get("reason") or "auth_failed"))
                return self._redirect("/lk/")
            item = dict(auth.get("item") or {})
            peer_id = str(auth.get("peer_id") or item.get("id") or "").strip()
            gateway_req = (form.get("gateway", ["edg"])[0] or "edg").strip().lower()
            uplink_req = (form.get("uplink", ["ams"])[0] or "ams").strip().lower()
            if not _is_supported_edge(gateway_req):
                gateway_req = "edg"
            if uplink_req not in ("ams", "nyc", "fra"):
                uplink_req = "ams"

            now_unix = int(dt.datetime.now(dt.timezone.utc).timestamp())
            if not self._is_peer_active_now(item, now_unix):
                self._audit("reissue_denied", reason="not_active", peer_id=peer_id)
                return self._send_html(
                    page(
                        "WG Reissue",
                        f"<div class='card warn'><b>Нельзя перевыпустить профиль:</b> WireGuard сейчас не активен.</div>{back_home_link()}",
                    ),
                    403,
                )

            out = backend_reissue(peer_id, remove_old=False, gateway=gateway_req, uplink=uplink_req)
            if not out.get("ok"):
                self._audit("reissue_error", peer_id=peer_id, error=str(out.get("error") or ""))
                return self._send_html(page("WG Reissue", f"<div class='card warn'><b>Ошибка перевыпуска:</b> {esc(out.get('error'))}</div>{back_home_link()}"), 400 if str(out.get("error") or "").startswith("invalid_") or out.get("error") == "vrn_gateway_unsupported_uplink" else 500)

            created = out.get("new_peer") if isinstance(out.get("new_peer"), dict) else out
            if not created.get("ok"):
                self._audit("reissue_error", peer_id=peer_id, error=str(created.get("error") or ""))
                return self._send_html(page("WG Reissue", f"<div class='card warn'><b>Ошибка перевыпуска:</b> {esc(created.get('error'))}</div>{back_home_link()}"), 500)

            new_id = str(created.get("id") or "")
            new_code = str(created.get("lk_token") or "")
            cfg = created.get("config", "")
            qrb64 = created.get("qr_png_b64", "")
            qr_is_placeholder = bool(created.get("qr_is_placeholder"))
            cfg_js = json.dumps(cfg, ensure_ascii=False)
            cfg_b64 = base64.b64encode(cfg.encode("utf-8")).decode("ascii")
            folder = safe_slug(str(created.get("label") or (str(item.get("label") or "web") + "-reissue")))

            self._audit("reissue_ok", peer_id=peer_id, new_id=new_id, gateway=gateway_req, uplink=uplink_req)

            body = f"""
<div class='card ok'>
<h2>Новый профиль выдан</h2>
<p class='small'>Сначала <b>добавьте новый профиль</b> в WireGuard (QR/конфиг). Старый профиль отключайте только когда вы готовы переключиться.</p>
<p><b>Gateway:</b> <code>{esc(gateway_req)}</code> | <b>Uplink:</b> <code>{esc(uplink_req)}</code></p>
</div>

<div class='card'>
<h3>Данные</h3>
<p><b>Новый ID:</b> <code id='peerId'>{esc(new_id)}</code></p>
<p><b>Новый Code:</b> <code id='lkToken'>{esc(new_code)}</code></p>
<p class='actions'>
  <button class='btn sm copy' type='button' id='copyPeerIdBtn'>ID</button>
  <button class='btn sm copy' type='button' id='copyLkTokenBtn'>Code</button>
  <span id='copyCredsState' class='small'></span>
</p>
</div>

{_qr_block_html(qrb64, qr_is_placeholder)}

<div class='card'>
<h3>Конфиг</h3>
<p style='display:flex;gap:10px;flex-wrap:wrap;align-items:center'>
  <button class='btn' type='button' id='copyCfgBtn'>Скопировать конфиг</button>
  <form method='post' action='/dlzip/' style='margin:0'>
    <input type='hidden' name='id' value='{esc(new_id)}'>
    <input type='hidden' name='label' value='{esc(folder)}'>
    <input type='hidden' name='cfg_b64' value='{esc(cfg_b64)}'>
    <button class='btn' type='submit'>Скачать конфиг</button>
  </form>
  <span id='copyState' class='small'></span>
</p>
<p class='small'>Скачивание отдаст ZIP <code>{esc(folder)}.zip</code> с файлом <code>{esc(new_id)}.conf</code> внутри.</p>
<pre id='cfgHolder' style='display:none'>{esc(cfg)}</pre>
<script>
(() => {{
  const cfg = {cfg_js};
  const btn = document.getElementById('copyCfgBtn');
  const state = document.getElementById('copyState');
  btn.addEventListener('click', async () => {{
    try {{
      await navigator.clipboard.writeText(cfg);
      state.textContent = 'Скопировано в буфер обмена';
    }} catch (e) {{
      state.textContent = 'Не удалось скопировать автоматически';
      document.getElementById('cfgHolder').style.display = 'block';
    }}
  }});

  const lkToken = {json.dumps(new_code, ensure_ascii=False)};
  const lkBtn = document.getElementById('copyLkTokenBtn');
  const idBtn = document.getElementById('copyPeerIdBtn');
  const peerId = {json.dumps(new_id, ensure_ascii=False)};
  const credsState = document.getElementById('copyCredsState');
  const setCredsState = (t) => {{ if (credsState) credsState.textContent = t || ''; }};

  idBtn.addEventListener('click', async () => {{
    try {{
      await navigator.clipboard.writeText(peerId);
      setCredsState('ID скопирован');
    }} catch (e) {{
      setCredsState('Не удалось скопировать ID');
    }}
  }});
  lkBtn.addEventListener('click', async () => {{
    try {{
      await navigator.clipboard.writeText(lkToken);
      setCredsState('Code скопирован');
    }} catch (e) {{
      setCredsState('Не удалось скопировать Code');
    }}
  }});
}})();
</script>
</div>

<div class='card warn'>
<h3>Отключить старый профиль</h3>
<p class='small'>Если нажать кнопку ниже, старый профиль будет удален с сервера. Если вы сейчас сидите через него, соединение может прерваться.</p>
<form method='post' action='/lk/reissue_finalize/'>
  <input type='hidden' name='old_id' value='{esc(peer_id)}'>
  <input type='hidden' name='new_id' value='{esc(new_id)}'>
  <input type='hidden' name='new_k' value='{esc(new_code)}'>
  <button class='btn' type='submit' onclick=\"return confirm('Удалить старый профиль? Соединение может прерваться.');\">Удалить старый профиль</button>
</form>
</div>

<p class='actions'>
  <a class='btn sm' href='/lk/'>Открыть ЛК после активации нового профиля</a>
  <a class='btn sm' href='/recover/?id={esc(new_id)}'>Recovery по ID/Code</a>
  <a class='btn sm' href='/check/'>/check/</a>
</p>
{back_home_link()}
"""
            return self._send_html(page("WG Reissue", body))

        if p == "/lk/reissue_finalize/":
            cookies = self._cookies()
            peer_id = (cookies.get("wg_peer_id") or "").strip()
            lk_token = (cookies.get("wg_lk_token") or "").strip()
            form = self._read_form()
            old_id = (form.get("old_id", [""])[0] or "").strip()
            new_id = (form.get("new_id", [""])[0] or "").strip()
            new_k = (form.get("new_k", [""])[0] or "").strip()

            if not peer_id or not lk_token or not old_id or old_id != peer_id:
                self._audit("reissue_finalize_denied", reason="bad_old_id")
                return self._redirect("/recover/")

            data = backend_list()
            if not data.get("ok"):
                return self._send_html(page("WG Reissue", f"<div class='card'><b>Ошибка:</b> {esc(data.get('error'))}</div>{back_home_link()}"), 500)

            item = None
            for x in data.get("items", []):
                if str(x.get("id") or "").strip() == peer_id:
                    item = x
                    break
            expected = str((item or {}).get("lk_token") or "").strip()
            if not item or not expected or lk_token != expected:
                self._audit("reissue_finalize_denied", reason="bad_cookie", peer_id=peer_id)
                return self._redirect("/recover/")

            out = backend_remove(peer_id)
            if not out.get("ok"):
                self._audit("reissue_finalize_error", peer_id=peer_id, error=str(out.get("error") or ""))
                return self._send_html(page("WG Reissue", f"<div class='card warn'><b>Ошибка удаления старого профиля:</b> {esc(out.get('error'))}</div>{back_home_link()}"), 500)

            self._audit("reissue_finalize_ok", peer_id=peer_id, new_id=new_id)
            headers = []
            if new_id and new_k:
                headers.append(("Set-Cookie", f"wg_peer_id={new_id}; Path=/; Max-Age=31536000; SameSite=Lax; Secure; HttpOnly"))
                headers.append(("Set-Cookie", f"wg_lk_token={new_k}; Path=/; Max-Age=31536000; SameSite=Lax; Secure; HttpOnly"))
                headers.append(("Set-Cookie", "wg_lk_ui=1; Path=/; Max-Age=31536000; SameSite=Lax; Secure"))
            return self._send_html(page("WG Reissue", f"<div class='card ok'><b>Старый профиль удален.</b></div><p><a class='btn' href='/check/'>Проверить подключение</a></p>{back_home_link()}"), headers=headers)

        if p == "/admin/action/":
            parsed = urlparse(self.path)
            ok, _, reason = self._admin_auth(parsed)
            if not ok:
                self._audit("admin_action_denied", reason=reason)
                return self._redirect("/", code=302)

            form = self._read_form()
            peer_id = (form.get("id", [""])[0] or "").strip()
            allowed_ip = (form.get("allowed_ip", [""])[0] or "").strip()
            action = (form.get("action", [""])[0] or "").strip()
            label_new = (form.get("label", [""])[0] or "").strip()
            admin_label_new = (form.get("admin_label", [""])[0] or "").strip()
            admin_comment_new = (form.get("admin_comment", [""])[0] or "").strip()
            client_type = (form.get("client_type", [""])[0] or "").strip()
            policy_mode = (form.get("policy_mode", [""])[0] or "").strip().lower()
            preferred_uplink = (form.get("preferred_uplink", [""])[0] or "").strip().lower()
            ingress_edge = (form.get("ingress_edge", [""])[0] or "").strip().lower()
            gateway_req = (form.get("gateway", ["edg"])[0] or "edg").strip().lower()
            uplink_req = (form.get("uplink", ["ams"])[0] or "ams").strip().lower()
            migration_wave = (form.get("migration_wave", [""])[0] or "").strip()
            migration_note = (form.get("migration_note", [""])[0] or "").strip()
            batch_peer_ids = _parse_peer_ids_text((form.get("peer_ids", [""])[0] or "").strip())
            migration_checklist = {
                "issued": ("migration_issued" in form),
                "client_imported": ("migration_client_imported" in form),
                "validated": ("migration_validated" in form),
                "old_removed": ("migration_old_removed" in form),
                "rollback_issued": ("migration_rollback_issued" in form),
                "rollback_validated": ("migration_rollback_validated" in form),
                "rollback_finalized": ("migration_rollback_finalized" in form),
            }
            current_policy_mode = (form.get("current_policy_mode", [""])[0] or "").strip().lower()
            next_url = (form.get("next", [""])[0] or "").strip()
            if next_url and not next_url.startswith("/admin/"):
                next_url = ""
            if action not in ("batch_migrate_gateway", "batch_set_migration_meta", "batch_set_migration_checklist", "batch_finalize_old_peers", "batch_wave_rollback_issue", "batch_wave_set_rollback_checklist", "batch_wave_finalize_rollback") and not peer_id:
                self._audit("admin_action_bad_request", peer_id=peer_id, action=action, allowed_ip=allowed_ip)
                return self._send_html(page("WG Admin", f"<div class='card warn'><b>Некорректный запрос.</b></div>{back_home_link()}"), 400)
            if action not in ("block", "delete", "uplink_ams", "uplink_nyc", "uplink_fra", "set_label", "set_admin_meta", "set_type", "set_policy_mode", "set_preferred_uplink", "set_ingress_edge", "migrate_gateway", "set_migration_meta", "set_migration_checklist", "batch_migrate_gateway", "batch_set_migration_meta", "batch_set_migration_checklist", "batch_finalize_old_peers", "batch_wave_rollback_issue", "batch_wave_set_rollback_checklist", "batch_wave_finalize_rollback"):
                self._audit("admin_action_bad_request", peer_id=peer_id, action=action, allowed_ip=allowed_ip)
                return self._send_html(page("WG Admin", f"<div class='card warn'><b>Некорректный запрос.</b></div>{back_home_link()}"), 400)

            if action == "set_admin_meta":
                if len(admin_label_new) > 128:
                    admin_label_new = admin_label_new[:128]
                if len(admin_comment_new) > 500:
                    admin_comment_new = admin_comment_new[:500]
                if not admin_label_new:
                    list_out = backend_list()
                    current_label = ""
                    if list_out.get("ok"):
                        for one in list_out.get("items", []):
                            if str(one.get("id") or "").strip() == peer_id:
                                current_label = str(one.get("label") or "").strip()
                                break
                    admin_label_new = current_label
                ok = _set_admin_meta(peer_id, admin_label=admin_label_new, admin_comment=admin_comment_new)
                if ok:
                    self._audit("admin_action_ok", action="set_admin_meta", peer_id=peer_id, admin_label=admin_label_new)
                    if next_url:
                        return self._redirect(next_url, code=302)
                    return self._send_html(page("WG Admin", f"<div class='card ok'><b>Свойства админки для {esc(peer_id)} обновлены.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться к профилю</a></p>"))
                self._audit("admin_action_error", action="set_admin_meta", peer_id=peer_id, error="admin_meta_write_failed")
                return self._send_html(page("WG Admin", f"<div class='card warn'><b>Не удалось сохранить свойства админки.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться</a></p>"), 500)

            if action == "set_label":
                if len(label_new) > 64:
                    label_new = label_new[:64]
                if not label_new:
                    self._audit("admin_action_bad_request", peer_id=peer_id, action="set_label", reason="empty_label")
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Label не должен быть пустым.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться</a></p>"), 400)
                ok = _set_label_override(peer_id, label_new)
                if ok:
                    self._audit("admin_action_ok", action="set_label", peer_id=peer_id, label=label_new)
                    if next_url:
                        return self._redirect(next_url, code=302)
                    return self._send_html(page("WG Admin", f"<div class='card ok'><b>Label для {esc(peer_id)} обновлен.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться к клиенту</a></p>"))
                self._audit("admin_action_error", action="set_label", peer_id=peer_id, error="label_override_write_failed")
                return self._send_html(page("WG Admin", f"<div class='card warn'><b>Не удалось сохранить label.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться</a></p>"), 500)

            if action == "set_type":
                ctype = _normalize_client_type(client_type)
                ok = _set_client_type(peer_id, ctype)
                if ok:
                    self._audit("admin_action_ok", action="set_type", peer_id=peer_id, client_type=ctype)
                    if next_url:
                        return self._redirect(next_url, code=302)
                    return self._send_html(page("WG Admin", f"<div class='card ok'><b>Тип для {esc(peer_id)} обновлен.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться к клиенту</a></p>"))
                self._audit("admin_action_error", action="set_type", peer_id=peer_id, error="type_override_write_failed")
                return self._send_html(page("WG Admin", f"<div class='card warn'><b>Не удалось сохранить тип клиента.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться</a></p>"), 500)

            if action == "set_policy_mode":
                if policy_mode not in ("auto", "manual"):
                    self._audit("admin_action_bad_request", peer_id=peer_id, action="set_policy_mode", reason="invalid_policy_mode")
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Некорректный policy mode.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться</a></p>"), 400)
                out = backend_set_routing_policy(peer_id, policy_mode, preferred_uplink=preferred_uplink or "ams")
                if out.get("ok"):
                    self._audit("admin_action_ok", action="set_policy_mode", peer_id=peer_id, policy_mode=policy_mode, preferred_uplink=out.get("preferred_uplink"))
                    if next_url:
                        return self._redirect(next_url, code=302)
                    return self._send_html(page("WG Admin", f"<div class='card ok'><b>Routing policy для {esc(peer_id)} обновлён: {esc(policy_mode)}.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться к клиенту</a></p>"))
                self._audit("admin_action_error", action="set_policy_mode", peer_id=peer_id, policy_mode=policy_mode, error=str(out.get("error") or ""))
                return self._send_html(page("WG Admin", f"<div class='card warn'><b>Не удалось обновить routing policy:</b> {esc(out.get('error'))}</div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться</a></p>"), 500)

            if action == "set_preferred_uplink":
                if preferred_uplink not in ("ams", "fra", "nyc"):
                    self._audit("admin_action_bad_request", peer_id=peer_id, action="set_preferred_uplink", reason="invalid_preferred_uplink")
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Некорректный preferred uplink.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться</a></p>"), 400)
                if current_policy_mode not in ("auto", "manual", "ams", "fra", "nyc"):
                    current_policy_mode = "auto"
                out = backend_set_preferred_uplink(peer_id, preferred_uplink, current_policy_mode)
                if out.get("ok"):
                    self._audit("admin_action_ok", action="set_preferred_uplink", peer_id=peer_id, preferred_uplink=preferred_uplink, policy_mode=out.get("policy_mode"))
                    if next_url:
                        return self._redirect(next_url, code=302)
                    return self._send_html(page("WG Admin", f"<div class='card ok'><b>Preferred uplink для {esc(peer_id)} обновлён: {esc(preferred_uplink)}.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться к клиенту</a></p>"))
                self._audit("admin_action_error", action="set_preferred_uplink", peer_id=peer_id, preferred_uplink=preferred_uplink, policy_mode=current_policy_mode, error=str(out.get("error") or ""))
                return self._send_html(page("WG Admin", f"<div class='card warn'><b>Не удалось обновить preferred uplink:</b> {esc(out.get('error'))}</div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться</a></p>"), 500)

            if action == "set_ingress_edge":
                if not _is_supported_edge(ingress_edge):
                    self._audit("admin_action_bad_request", peer_id=peer_id, action="set_ingress_edge", reason="invalid_ingress_edge")
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Некорректный ingress edge.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться</a></p>"), 400)
                out = backend_set_ingress_edge(peer_id, ingress_edge)
                if out.get("ok"):
                    self._audit("admin_action_ok", action="set_ingress_edge", peer_id=peer_id, ingress_edge=ingress_edge, effective_edge=out.get("effective_edge"), intent_only=out.get("intent_only"))
                    if next_url:
                        return self._redirect(next_url, code=302)
                    return self._send_html(page("WG Admin", f"<div class='card ok'><b>Ingress edge для {esc(peer_id)} обновлён: {esc(ingress_edge)}.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться к клиенту</a></p>"))
                self._audit("admin_action_error", action="set_ingress_edge", peer_id=peer_id, ingress_edge=ingress_edge, error=str(out.get("error") or ""))
                return self._send_html(page("WG Admin", f"<div class='card warn'><b>Не удалось обновить ingress edge:</b> {esc(out.get('error'))}</div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться</a></p>"), 500)

            if action == "migrate_gateway":
                if not _is_supported_edge(gateway_req):
                    self._audit("admin_action_bad_request", peer_id=peer_id, action="migrate_gateway", reason="invalid_gateway")
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Некорректный gateway.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться</a></p>"), 400)
                if uplink_req not in ("ams", "fra", "nyc"):
                    self._audit("admin_action_bad_request", peer_id=peer_id, action="migrate_gateway", reason="invalid_uplink")
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Некорректный uplink.</b></div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться</a></p>"), 400)
                out = backend_reissue(peer_id, remove_old=False, gateway=gateway_req, uplink=uplink_req)
                if not out.get("ok"):
                    self._audit("admin_action_error", action="migrate_gateway", peer_id=peer_id, gateway=gateway_req, uplink=uplink_req, error=str(out.get("error") or ""))
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Не удалось выдать профиль для migration:</b> {esc(out.get('error') or 'unknown')}</div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться</a></p>"), 400 if str(out.get("error") or "").startswith("invalid_") or out.get("error") == "vrn_gateway_unsupported_uplink" else 500)
                created = out.get("new_peer") if isinstance(out.get("new_peer"), dict) else out
                if not created.get("ok"):
                    self._audit("admin_action_error", action="migrate_gateway", peer_id=peer_id, gateway=gateway_req, uplink=uplink_req, error=str(created.get("error") or ""))
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Не удалось выдать профиль для migration:</b> {esc(created.get('error') or 'unknown')}</div><p><a class='btn' href='/admin/peers/{quote(peer_id, safe='')}/'>Вернуться</a></p>"), 500)
                new_id = str(created.get("id") or "")
                cfg = created.get("config", "")
                qrb64 = created.get("qr_png_b64", "")
                qr_is_placeholder = bool(created.get("qr_is_placeholder"))
                cfg_js = json.dumps(cfg, ensure_ascii=False)
                cfg_b64 = base64.b64encode(cfg.encode("utf-8")).decode("ascii")
                folder = safe_slug(str(created.get("label") or "peer-migration"))
                self._audit("admin_action_ok", action="migrate_gateway", peer_id=peer_id, gateway=gateway_req, uplink=uplink_req, new_peer_id=new_id)
                return self._send_html(page("WG Admin Migration", f"""
<div class='card ok'>
<h2>Migration profile выдан</h2>
<p class='small'>Новый профиль создан на <code>{esc(gateway_req)}</code> с uplink <code>{esc(uplink_req)}</code>. Старый peer <code>{esc(peer_id)}</code> остаётся активным до отдельного удаления.</p>
<p><b>Новый peer:</b> <code id='peerId'>{esc(new_id)}</code></p>
</div>
{_qr_block_html(qrb64, qr_is_placeholder)}
<div class='card'>
<h3>Конфиг</h3>
<p style='display:flex;gap:10px;flex-wrap:wrap;align-items:center'>
  <button class='btn' type='button' id='copyCfgBtn'>Скопировать конфиг</button>
  <form method='post' action='/dlzip/' style='margin:0'>
    <input type='hidden' name='id' value='{esc(new_id)}'>
    <input type='hidden' name='label' value='{esc(folder)}'>
    <input type='hidden' name='cfg_b64' value='{esc(cfg_b64)}'>
    <button class='btn' type='submit'>Скачать конфиг</button>
  </form>
  <span id='copyState' class='small'></span>
</p>
<pre id='cfgHolder' style='display:none'>{esc(cfg)}</pre>
</div>
<div class='card'>
<h3>Cutover</h3>
<p class='small'>1. Добавьте новый профиль в клиент. 2. Подтвердите, что новый tunnel активен. 3. После этого удалите старый peer вручную.</p>
<p><a class='btn' href='/admin/peers/{quote(peer_id, safe="")}/'>Вернуться к старому peer</a> <a class='btn' href='/admin/peers/{quote(new_id, safe="")}/'>Открыть новый peer</a></p>
</div>
<script>
(() => {{
  const cfg = {cfg_js};
  const btn = document.getElementById('copyCfgBtn');
  const state = document.getElementById('copyState');
  if (!btn) return;
  btn.addEventListener('click', async () => {{
    try {{
      await navigator.clipboard.writeText(cfg);
      if (state) state.textContent = 'Скопировано в буфер обмена';
    }} catch (e) {{
      if (state) state.textContent = 'Не удалось скопировать';
    }}
  }});
}})();
</script>
"""))

            if action == "set_migration_meta":
                ok = _set_migration_state(peer_id, wave=migration_wave, note=migration_note)
                if ok:
                    self._audit("admin_action_ok", action="set_migration_meta", peer_id=peer_id, wave=migration_wave)
                    if next_url:
                        return self._redirect(next_url, code=302)
                    return self._send_html(page("WG Admin", f"<div class='card ok'><b>Migration meta для {esc(peer_id)} сохранены.</b></div>"))
                self._audit("admin_action_error", action="set_migration_meta", peer_id=peer_id, error="migration_state_write_failed")
                return self._send_html(page("WG Admin", f"<div class='card warn'><b>Не удалось сохранить migration meta.</b></div>"), 500)

            if action == "set_migration_checklist":
                ok = _set_migration_state(peer_id, checklist=migration_checklist)
                if ok:
                    self._audit("admin_action_ok", action="set_migration_checklist", peer_id=peer_id)
                    if next_url:
                        return self._redirect(next_url, code=302)
                    return self._send_html(page("WG Admin", f"<div class='card ok'><b>Migration checklist для {esc(peer_id)} сохранён.</b></div>"))
                self._audit("admin_action_error", action="set_migration_checklist", peer_id=peer_id, error="migration_state_write_failed")
                return self._send_html(page("WG Admin", f"<div class='card warn'><b>Не удалось сохранить migration checklist.</b></div>"), 500)

            if action == "batch_set_migration_meta":
                if not batch_peer_ids:
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Список peer ID пуст.</b></div>"), 400)
                changed = 0
                for pid in batch_peer_ids:
                    if _set_migration_state(pid, wave=migration_wave, note=migration_note):
                        changed += 1
                self._audit("admin_action_ok", action="batch_set_migration_meta", peer_id=",".join(batch_peer_ids[:3]), count=changed, wave=migration_wave)
                if next_url:
                    return self._redirect(next_url, code=302)
                return self._send_html(page("WG Admin", f"<div class='card ok'><b>Wave / notes обновлены для {esc(changed)} peer(s).</b></div>"))

            if action == "batch_set_migration_checklist":
                if not batch_peer_ids:
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Список peer ID пуст.</b></div>"), 400)
                changed = 0
                for pid in batch_peer_ids:
                    if _set_migration_state(pid, checklist=migration_checklist):
                        changed += 1
                self._audit("admin_action_ok", action="batch_set_migration_checklist", peer_id=",".join(batch_peer_ids[:3]), count=changed)
                if next_url:
                    return self._redirect(next_url, code=302)
                return self._send_html(page("WG Admin", f"<div class='card ok'><b>Checklist обновлён для {esc(changed)} peer(s).</b></div>"))

            if action == "batch_migrate_gateway":
                if not _is_supported_edge(gateway_req):
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Некорректный gateway.</b></div>"), 400)
                if uplink_req not in ("ams", "fra", "nyc"):
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Некорректный uplink.</b></div>"), 400)
                if not batch_peer_ids:
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Список peer ID пуст.</b></div>"), 400)
                issued = []
                failed = []
                for pid in batch_peer_ids:
                    out = backend_reissue(pid, remove_old=False, gateway=gateway_req, uplink=uplink_req)
                    created = out.get("new_peer") if isinstance(out.get("new_peer"), dict) else out
                    if out.get("ok") and created.get("ok") and created.get("id"):
                        new_id = str(created.get("id") or "").strip()
                        issued.append((pid, new_id))
                        _set_migration_state(pid, wave=migration_wave, note=migration_note, checklist={"issued": True})
                        _set_migration_state(new_id, wave=migration_wave, note=migration_note, checklist={"issued": True})
                    else:
                        failed.append((pid, str((created if isinstance(created, dict) else out).get("error") or "unknown")))
                self._audit("admin_action_ok", action="batch_migrate_gateway", peer_id=",".join(batch_peer_ids[:3]), count=len(issued), gateway=gateway_req, uplink=uplink_req)
                issued_html = "".join(f"<li><code>{esc(old_id)}</code> → <code>{esc(new_id)}</code></li>" for old_id, new_id in issued) or "<li>-</li>"
                failed_html = "".join(f"<li><code>{esc(pid)}</code>: {esc(err)}</li>" for pid, err in failed) or "<li>-</li>"
                return self._send_html(page("WG Admin Batch Migration", f"""
<div class='card ok'>
<h2>Batch migration issue finished</h2>
<p><b>Gateway:</b> <code>{esc(gateway_req)}</code> | <b>Uplink:</b> <code>{esc(uplink_req)}</code></p>
<p><b>Wave:</b> <code>{esc(migration_wave or '-')}</code></p>
<p><b>Issued:</b> <code>{esc(len(issued))}</code> | <b>Failed:</b> <code>{esc(len(failed))}</code></p>
</div>
<div class='card'><h3>Issued pairs</h3><ul>{issued_html}</ul></div>
<div class='card'><h3>Failures</h3><ul>{failed_html}</ul></div>
<p><a class='btn' href='{esc(next_url or "/admin/edges/")}'>Вернуться</a></p>
"""))

            if action == "batch_finalize_old_peers":
                if not batch_peer_ids:
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Список peer ID пуст.</b></div>"), 400)
                removed = []
                failed = []
                for pid in batch_peer_ids:
                    out = backend_remove(pid)
                    if out.get("ok"):
                        removed.append(pid)
                        _set_migration_state(pid, checklist={"old_removed": True})
                    else:
                        failed.append((pid, str(out.get("error") or "unknown")))
                self._audit("admin_action_ok", action="batch_finalize_old_peers", peer_id=",".join(batch_peer_ids[:3]), count=len(removed))
                removed_html = "".join(f"<li><code>{esc(pid)}</code></li>" for pid in removed) or "<li>-</li>"
                failed_html = "".join(f"<li><code>{esc(pid)}</code>: {esc(err)}</li>" for pid, err in failed) or "<li>-</li>"
                return self._send_html(page("WG Admin Batch Finalize", f"""
<div class='card ok'>
<h2>Batch finalize finished</h2>
<p><b>Removed:</b> <code>{esc(len(removed))}</code> | <b>Failed:</b> <code>{esc(len(failed))}</code></p>
</div>
<div class='card'><h3>Removed old peers</h3><ul>{removed_html}</ul></div>
<div class='card'><h3>Failures</h3><ul>{failed_html}</ul></div>
<p><a class='btn' href='{esc(next_url or "/admin/edges/")}'>Вернуться</a></p>
"""))

            if action == "batch_wave_set_rollback_checklist":
                if not batch_peer_ids:
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Список peer ID пуст.</b></div>"), 400)
                changed = 0
                checklist = {
                    "rollback_issued": migration_checklist.get("rollback_issued"),
                    "rollback_validated": migration_checklist.get("rollback_validated"),
                    "rollback_finalized": migration_checklist.get("rollback_finalized"),
                }
                for pid in batch_peer_ids:
                    if _set_migration_state(pid, checklist=checklist):
                        changed += 1
                self._audit("admin_action_ok", action="batch_wave_set_rollback_checklist", peer_id=",".join(batch_peer_ids[:3]), count=changed)
                if next_url:
                    return self._redirect(next_url, code=302)
                return self._send_html(page("WG Admin", f"<div class='card ok'><b>Rollback checklist обновлён для {esc(changed)} peer(s).</b></div>"))

            if action == "batch_wave_finalize_rollback":
                if not batch_peer_ids:
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Список peer ID пуст.</b></div>"), 400)
                changed = 0
                for pid in batch_peer_ids:
                    if _set_migration_state(pid, checklist={"rollback_finalized": True, "rollback_validated": True, "rollback_issued": True}):
                        changed += 1
                self._audit("admin_action_ok", action="batch_wave_finalize_rollback", peer_id=",".join(batch_peer_ids[:3]), count=changed)
                if next_url:
                    return self._redirect(next_url, code=302)
                return self._send_html(page("WG Admin", f"<div class='card ok'><b>Rollback finalized для {esc(changed)} peer(s).</b></div>"))

            if action == "batch_wave_rollback_issue":
                if not _is_supported_edge(gateway_req):
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Некорректный gateway.</b></div>"), 400)
                if uplink_req not in ("ams", "fra", "nyc"):
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Некорректный uplink.</b></div>"), 400)
                if not batch_peer_ids:
                    return self._send_html(page("WG Admin", f"<div class='card warn'><b>Список peer ID пуст.</b></div>"), 400)
                issued = []
                failed = []
                for pid in batch_peer_ids:
                    out = backend_reissue(pid, remove_old=False, gateway=gateway_req, uplink=uplink_req)
                    created = out.get("new_peer") if isinstance(out.get("new_peer"), dict) else out
                    if out.get("ok") and created.get("ok") and created.get("id"):
                        new_id = str(created.get("id") or "").strip()
                        issued.append((pid, new_id))
                        _set_migration_state(pid, wave=migration_wave, note=migration_note, checklist={"rollback_issued": True})
                        _set_migration_state(new_id, wave=migration_wave, note=migration_note, checklist={"rollback_issued": True})
                    else:
                        failed.append((pid, str((created if isinstance(created, dict) else out).get("error") or "unknown")))
                self._audit("admin_action_ok", action="batch_wave_rollback_issue", peer_id=",".join(batch_peer_ids[:3]), count=len(issued), gateway=gateway_req, uplink=uplink_req)
                issued_html = "".join(f"<li><code>{esc(old_id)}</code> → <code>{esc(new_id)}</code></li>" for old_id, new_id in issued) or "<li>-</li>"
                failed_html = "".join(f"<li><code>{esc(pid)}</code>: {esc(err)}</li>" for pid, err in failed) or "<li>-</li>"
                return self._send_html(page("WG Admin Wave Rollback", f"""
<div class='card ok'>
<h2>Wave rollback issue finished</h2>
<p><b>Gateway:</b> <code>{esc(gateway_req)}</code> | <b>Uplink:</b> <code>{esc(uplink_req)}</code></p>
<p><b>Issued:</b> <code>{esc(len(issued))}</code> | <b>Failed:</b> <code>{esc(len(failed))}</code></p>
</div>
<div class='card'><h3>Issued rollback pairs</h3><ul>{issued_html}</ul></div>
<div class='card'><h3>Failures</h3><ul>{failed_html}</ul></div>
<p><a class='btn' href='{esc(next_url or "/admin/waves/")}'>Вернуться</a></p>
"""))

            if action in ("uplink_ams", "uplink_nyc", "uplink_fra"):
                uplink = "ams" if action == "uplink_ams" else ("nyc" if action == "uplink_nyc" else "fra")
                out = backend_set_uplink(peer_id, uplink)
                if out.get("ok"):
                    self._audit("admin_action_ok", action="set_uplink", peer_id=peer_id, allowed_ip=allowed_ip, uplink=uplink)
                    if next_url:
                        return self._redirect(next_url, code=302)
                    return self._send_html(page("WG Admin", f"<div class='card ok'><b>Uplink для {esc(peer_id)} переключен на {esc(uplink)}.</b></div><p><a class='btn' href='/admin/peers/'>Вернуться в peers</a></p>"))
                self._audit("admin_action_error", action="set_uplink", peer_id=peer_id, allowed_ip=allowed_ip, uplink=uplink, error=str(out.get("error") or ""))
                return self._send_html(page("WG Admin", f"<div class='card warn'><b>Ошибка переключения uplink:</b> {esc(out.get('error'))}</div><p><a class='btn' href='/admin/peers/'>Вернуться в peers</a></p>"), 500)

            if action == "block":
                out = backend_block(peer_id)
                if out.get("ok"):
                    self._audit("admin_action_ok", action="block", peer_id=peer_id)
                    return self._send_html(page("WG Admin", f"<div class='card ok'><b>Пользователь {esc(peer_id)} заблокирован.</b></div><p><a class='btn' href='/admin/peers/'>Вернуться в peers</a></p>"))
                self._audit("admin_action_error", action="block", peer_id=peer_id, error=str(out.get("error") or ""))
                return self._send_html(page("WG Admin", f"<div class='card warn'><b>Ошибка блокировки:</b> {esc(out.get('error'))}</div><p><a class='btn' href='/admin/peers/'>Вернуться в peers</a></p>"), 500)

            out = backend_remove(peer_id)
            if out.get("ok"):
                self._audit("admin_action_ok", action="delete", peer_id=peer_id)
                return self._send_html(page("WG Admin", f"<div class='card ok'><b>Пользователь {esc(peer_id)} удален.</b></div><p><a class='btn' href='/admin/peers/'>Вернуться в peers</a></p>"))
            self._audit("admin_action_error", action="delete", peer_id=peer_id, error=str(out.get("error") or ""))
            return self._send_html(page("WG Admin", f"<div class='card warn'><b>Ошибка удаления:</b> {esc(out.get('error'))}</div><p><a class='btn' href='/admin/peers/'>Вернуться в peers</a></p>"), 500)

        if p == "/lk/uplink/":
            data = backend_list()
            if not data.get("ok"):
                return self._send_html(page("WG LK", f"<div class='card'><b>Ошибка:</b> {esc(data.get('error'))}</div>{back_home_link()}"), 500)
            auth = self._resolve_lk_access(data, require_active=True)
            if not auth.get("ok"):
                self._audit("lk_uplink_denied", reason=str(auth.get("reason") or "auth_failed"))
                return self._redirect("/lk/")
            form = self._read_form()
            uplink = (form.get("uplink", [""])[0] or "").strip().lower()
            peer_id = str(auth.get("peer_id") or "").strip()
            out = backend_set_uplink(peer_id, uplink)
            if out.get("ok"):
                self._audit("lk_set_uplink_ok", peer_id=peer_id, uplink=uplink)
                return self._send_html(page("WG LK", f"<div class='card ok'><b>Аплинк обновлён:</b> {esc(uplink)}</div><p><a class='btn' href='/lk/'>Вернуться в ЛК</a></p>"))
            self._audit("lk_set_uplink_error", peer_id=peer_id, uplink=uplink, error=str(out.get('error') or ''))
            return self._send_html(page("WG LK", f"<div class='card warn'><b>Ошибка смены аплинка:</b> {esc(out.get('error') or 'unknown')}</div><p><a class='btn' href='/lk/'>Вернуться в ЛК</a></p>"), 500)

        if p == "/new/":
            return self._redirect("/account/profiles/new", code=303)

        if p != "/new/":
            return self._send_html(page("404", f"<div class='card'>Not found</div>{back_home_link()}"), 404)


if __name__ == "__main__":
    srv = ThreadingHTTPServer((HOST, PORT), H)
    print(f"wg-portal-http on {HOST}:{PORT}", flush=True)
    srv.serve_forever()
