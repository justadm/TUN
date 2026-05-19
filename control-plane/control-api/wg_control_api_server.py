#!/usr/bin/env python3
import datetime as dt
import json
import os
import re
import subprocess
import shlex
import fcntl
from pathlib import Path
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

HOST = os.getenv("WG_CONTROL_API_HOST", "127.0.0.1")
PORT = int(os.getenv("WG_CONTROL_API_PORT", "18110"))
CLI = os.getenv("WG_PORTAL_CLI", "/usr/local/bin/wg_portal.py")
API_TOKEN = os.getenv("WG_CONTROL_API_TOKEN", "").strip()
API_VERSION = "v1"
SUPPORTED_EDGES = ("edg", "vrn", "msk_d")
STATE_DIR = Path(os.getenv("WG_PORTAL_STATE", "/var/lib/wg-portal"))
AUDIT_LOG = STATE_DIR / "audit.jsonl"
AUDIT_LOCK = STATE_DIR / ".api_audit.lock"
NFT_BIN = os.getenv("WG_PORTAL_NFT_BIN", "sudo nft").strip() or "sudo nft"
NFT_TABLE = os.getenv("WG_PORTAL_UPLINK_TABLE", "msk_geo").strip() or "msk_geo"
NFT_FORCE_CHAIN = os.getenv("WG_PORTAL_UPLINK_FORCE_CHAIN", "prerouting").strip() or "prerouting"
NFT_UPLINK_COMMENT_PREFIX_NYC = os.getenv("WG_PORTAL_UPLINK_COMMENT_PREFIX_NYC", "iphone-edg-us-force-").strip() or "iphone-edg-us-force-"
NFT_UPLINK_COMMENT_PREFIX_FRA = os.getenv("WG_PORTAL_UPLINK_COMMENT_PREFIX_FRA", "iphone-edg-fra-force-").strip() or "iphone-edg-fra-force-"
DB_READ_ENABLED = (os.getenv("JSTUN_DB_READ_ENABLED", "0").strip() == "1")
DB_READ_PEERS_ENABLED = (os.getenv("JSTUN_DB_READ_PEERS_ENABLED", "1").strip() != "0")
DB_READ_UPLINKS_ENABLED = (os.getenv("JSTUN_DB_READ_UPLINKS_ENABLED", "1").strip() != "0")
DB_READ_EVENTS_ENABLED = (os.getenv("JSTUN_DB_READ_EVENTS_ENABLED", "1").strip() != "0")
DB_PSQL_BIN = os.getenv("JSTUN_DB_PSQL_BIN", "psql").strip() or "psql"
DB_HOST = os.getenv("JSTUN_DB_HOST", "127.0.0.1").strip() or "127.0.0.1"
DB_PORT = os.getenv("JSTUN_DB_PORT", "15432").strip() or "15432"
DB_NAME = os.getenv("JSTUN_DB_NAME", "jstun_shadow").strip() or "jstun_shadow"
DB_USER = os.getenv("JSTUN_DB_USER", "jstun_shadow").strip() or "jstun_shadow"
DB_PASSWORD = os.getenv("JSTUN_DB_PASSWORD", "").strip()
DB_WRITE_MIRROR_ENABLED = (os.getenv("JSTUN_DB_WRITE_MIRROR_ENABLED", "0").strip() == "1")
DB_WRITE_MIRROR_EVENTS_ENABLED = (os.getenv("JSTUN_DB_WRITE_MIRROR_EVENTS_ENABLED", "1").strip() != "0")
VRN_EDGE_SSH_HOST = os.getenv("JSTUN_VRN_EDGE_SSH_HOST", "user@91.221.109.60").strip() or "user@91.221.109.60"
VRN_EDGE_SSH_PORT = os.getenv("JSTUN_VRN_EDGE_SSH_PORT", "65022").strip() or "65022"
VRN_EDGE_IDENTITY_FILE = os.getenv("JSTUN_VRN_EDGE_IDENTITY_FILE", "/home/opsadmin/.ssh/jstun_shadow_read_ed25519").strip() or "/home/opsadmin/.ssh/jstun_shadow_read_ed25519"
VRN_EDGE_ENV = os.getenv("JSTUN_VRN_EDGE_ENV", "/etc/wireguard/wg-portal-vrn.env").strip() or "/etc/wireguard/wg-portal-vrn.env"
VRN_EDGE_PYTHON = os.getenv("JSTUN_VRN_EDGE_PYTHON", "python3").strip() or "python3"
VRN_EDGE_CLI = os.getenv("JSTUN_VRN_EDGE_CLI", "/usr/local/bin/wg_portal_vrn.py").strip() or "/usr/local/bin/wg_portal_vrn.py"
VRN_EDGE_ENDPOINT = os.getenv("JSTUN_VRN_EDGE_ENDPOINT", "91.221.109.60:51820").strip() or "91.221.109.60:51820"
VRN_EDGE_SERVER_PUB_PATH = os.getenv("JSTUN_VRN_EDGE_SERVER_PUB_PATH", "/etc/wireguard/vrn_server.pub").strip() or "/etc/wireguard/vrn_server.pub"
VRN_EDGE_ROUTE_BIN = os.getenv("JSTUN_VRN_EDGE_ROUTE_BIN", "/usr/local/sbin/vrn-peer-route").strip() or "/usr/local/sbin/vrn-peer-route"


def run_cli(args):
    p = subprocess.run([CLI] + args, capture_output=True, text=True)
    raw = (p.stdout or "").strip()
    try:
        data = json.loads(raw or "{}")
    except Exception:
        data = {"ok": False, "error": (p.stderr or raw or "invalid json").strip()}
    if p.returncode != 0 and data.get("ok", True):
        data = {"ok": False, "error": (p.stderr or raw or "cli failed").strip()}
    return data


def run_cmd(args):
    p = subprocess.run(args, capture_output=True, text=True)
    return {
        "ok": (p.returncode == 0),
        "code": p.returncode,
        "stdout": p.stdout or "",
        "stderr": p.stderr or "",
    }


def nft_cmd(*args):
    base = [x for x in NFT_BIN.split(" ") if x]
    return run_cmd(base + list(args))


def run_ssh(host, remote_cmd):
    args = ["ssh", "-o", "StrictHostKeyChecking=accept-new"]
    if VRN_EDGE_SSH_PORT:
        args += ["-p", VRN_EDGE_SSH_PORT]
    if VRN_EDGE_IDENTITY_FILE:
        args += ["-i", VRN_EDGE_IDENTITY_FILE]
    args += [host, remote_cmd]
    return run_cmd(args)


def run_vrn_cli(args):
    inner = "set -a; . {env}; set +a; exec {py} {cli} {args}".format(
        env=shlex.quote(VRN_EDGE_ENV),
        py=shlex.quote(VRN_EDGE_PYTHON),
        cli=shlex.quote(VRN_EDGE_CLI),
        args=" ".join(shlex.quote(str(x)) for x in args),
    )
    remote_cmd = "sudo sh -lc {inner}".format(inner=shlex.quote(inner))
    args = ["ssh", "-o", "StrictHostKeyChecking=accept-new"]
    if VRN_EDGE_SSH_PORT:
        args += ["-p", VRN_EDGE_SSH_PORT]
    if VRN_EDGE_IDENTITY_FILE:
        args += ["-i", VRN_EDGE_IDENTITY_FILE]
    args += [VRN_EDGE_SSH_HOST, remote_cmd]
    p = subprocess.run(args, capture_output=True, text=True)
    raw = (p.stdout or "").strip()
    try:
        data = json.loads(raw or "{}")
    except Exception:
        data = {"ok": False, "error": (p.stderr or raw or "invalid json").strip()}
    if p.returncode != 0 and data.get("ok", True):
        data = {"ok": False, "error": (p.stderr or raw or "remote cli failed").strip()}
    return data


def db_query_json(sql):
    env = os.environ.copy()
    if DB_PASSWORD:
        env["PGPASSWORD"] = DB_PASSWORD
    args = [
        DB_PSQL_BIN,
        "-h",
        DB_HOST,
        "-p",
        DB_PORT,
        "-U",
        DB_USER,
        "-d",
        DB_NAME,
        "-At",
        "-c",
        sql,
    ]
    p = subprocess.run(args, capture_output=True, text=True, env=env)
    if p.returncode != 0:
        return {
            "ok": False,
            "error": one_line(p.stderr or p.stdout or "db query failed"),
            "cmd": " ".join(shlex.quote(x) for x in args[:-1]) + " -c <sql>",
        }
    raw = (p.stdout or "").strip()
    if not raw:
        return {"ok": True, "value": None}
    try:
        return {"ok": True, "value": json.loads(raw)}
    except Exception:
        return {"ok": False, "error": "invalid json from db"}


def db_exec(sql):
    env = os.environ.copy()
    if DB_PASSWORD:
        env["PGPASSWORD"] = DB_PASSWORD
    args = [
        DB_PSQL_BIN,
        "-h",
        DB_HOST,
        "-p",
        DB_PORT,
        "-U",
        DB_USER,
        "-d",
        DB_NAME,
        "-v",
        "ON_ERROR_STOP=1",
        "-c",
        sql,
    ]
    p = subprocess.run(args, capture_output=True, text=True, env=env)
    if p.returncode != 0:
        return {
            "ok": False,
            "error": one_line(p.stderr or p.stdout or "db exec failed"),
            "cmd": " ".join(shlex.quote(x) for x in args[:-1]) + " -c <sql>",
        }
    return {"ok": True}


def sql_str(value):
    if value is None:
        return "NULL"
    return "'" + str(value).replace("'", "''") + "'"


def sql_int(value):
    try:
        return str(int(value))
    except Exception:
        return "0"


def sql_int_or_null(value):
    try:
        if value in (None, ""):
            return "NULL"
        return str(int(value))
    except Exception:
        return "NULL"


def sql_jsonb(value):
    if value is None:
        return "'{}'::jsonb"
    raw = json.dumps(value, ensure_ascii=False, sort_keys=True)
    return sql_str(raw) + "::jsonb"


def uplink_id_from_name(name):
    route = str(name or "").strip().lower()
    if route not in ("ams", "fra", "nyc"):
        route = "ams"
    return f"edg/{route}"


def failover_uplink_id_from_name(name):
    route = str(name or "").strip().lower()
    if route == "fra":
        return "edg/ams"
    if route == "nyc":
        return "edg/ams"
    return "edg/fra"


def legacy_find_peer(peer_id, payload=None):
    items = []
    if isinstance(payload, dict):
        items = payload.get("items") or []
    for item in items:
        if str(item.get("id") or "").strip() == str(peer_id or "").strip():
            return item
    return None


def mirror_peer_to_db(item, preferred_uplink=None, policy_mode=None, change_reason=None, updated_by="control-api", ingress_edge=None, effective_edge=None):
    if not DB_WRITE_MIRROR_ENABLED:
        return {"ok": True, "skipped": True}
    peer = dict(item or {})
    peer_id = str(peer.get("id") or "").strip()
    public_key = str(peer.get("public_key") or "").strip()
    allowed_ip = str(peer.get("allowed_ip") or "").strip()
    endpoint = str(peer.get("endpoint") or "").strip()
    created_at = peer.get("created_at")
    status = str(peer.get("status") or "").strip() or "pending"
    if not peer_id or not public_key or not allowed_ip or not endpoint or not created_at:
        return {"ok": False, "error": "mirror_peer_missing_required_fields"}

    peer_type = str(peer.get("peer_type") or "default").strip() or "default"
    ingress_edge_id = str(ingress_edge or peer.get("edge_id") or "edg").strip().lower() or "edg"
    if ingress_edge_id not in SUPPORTED_EDGES:
        ingress_edge_id = "edg"
    effective_edge_id = str(effective_edge or ingress_edge_id).strip().lower() or ingress_edge_id
    if effective_edge_id not in SUPPORTED_EDGES:
        effective_edge_id = ingress_edge_id

    preferred_name = str(preferred_uplink or "").strip().lower()
    if not preferred_name:
        preferred_name = "nyc" if ingress_edge_id == "vrn" else "ams"
    preferred_uplink_id = _effective_uplink_id_for_edge(ingress_edge_id, preferred_name, policy_mode or "auto")
    failover_uplink_id = _failover_uplink_id_for_edge(ingress_edge_id, preferred_name)
    policy_mode_value = _normalized_policy_mode(policy_mode or "auto")
    if policy_mode_value not in ("auto", "manual", "ru-direct"):
        policy_mode_value = "auto"

    last_handshake_at = peer.get("last_handshake_at")
    last_handshake_unix = peer.get("last_handshake_unix")
    rx_bytes = peer.get("rx_bytes")
    tx_bytes = peer.get("tx_bytes")
    connected_at = peer.get("connected_at")
    blocked_at = peer.get("blocked_at")
    removed_at = peer.get("removed_at")
    block_reason = peer.get("block_reason")
    remove_reason = peer.get("remove_reason")
    expires_at = peer.get("expires_at")
    absolute_expires_at = peer.get("absolute_expires_at")
    absolute_ttl_sec = peer.get("absolute_ttl_sec")
    ttl_sec = peer.get("ttl_sec") or 0
    lk_token = peer.get("lk_token")
    lk_token_created_at = peer.get("lk_token_created_at")

    pending_expires_at = expires_at if status == "pending" else None
    live_uplink_id = _effective_uplink_id_for_edge(ingress_edge_id, preferred_name, policy_mode_value)
    active_uplink_id = live_uplink_id if status in ("active", "pending") else None
    effective_uplink_id = live_uplink_id if status in ("active", "pending") else None
    effective_ru_path = "direct"
    effective_notru_path = "nyc" if ingress_edge_id == "vrn" else _uplink_name_from_id(effective_uplink_id)

    sql = f"""
    begin;
    insert into peers (
      peer_id, label, peer_type, status, public_key, allowed_ip, endpoint,
      created_at, pending_expires_at, ttl_sec, absolute_ttl_sec, absolute_expires_at,
      lk_token, lk_token_created_at, connected_at, blocked_at, block_reason,
      removed_at, remove_reason, updated_at
    ) values (
      {sql_str(peer_id)}, {sql_str(peer.get("label") or "")}, {sql_str(peer_type)}, {sql_str(status)}, {sql_str(public_key)},
      {sql_str(allowed_ip)}::cidr, {sql_str(endpoint)}, {sql_str(created_at)}, {sql_str(pending_expires_at)},
      {sql_int(ttl_sec)}, {sql_int_or_null(absolute_ttl_sec)},
      {sql_str(absolute_expires_at)}, {sql_str(lk_token)}, {sql_str(lk_token_created_at)}, {sql_str(connected_at)},
      {sql_str(blocked_at)}, {sql_str(block_reason)}, {sql_str(removed_at)}, {sql_str(remove_reason)}, now()
    )
    on conflict (peer_id) do update set
      label = excluded.label,
      peer_type = excluded.peer_type,
      status = excluded.status,
      public_key = excluded.public_key,
      allowed_ip = excluded.allowed_ip,
      endpoint = excluded.endpoint,
      pending_expires_at = excluded.pending_expires_at,
      ttl_sec = excluded.ttl_sec,
      absolute_ttl_sec = excluded.absolute_ttl_sec,
      absolute_expires_at = excluded.absolute_expires_at,
      lk_token = excluded.lk_token,
      lk_token_created_at = excluded.lk_token_created_at,
      connected_at = excluded.connected_at,
      blocked_at = excluded.blocked_at,
      block_reason = excluded.block_reason,
      removed_at = excluded.removed_at,
      remove_reason = excluded.remove_reason,
      updated_at = now();

    insert into peer_runtime_state (
      peer_id, last_handshake_at, last_handshake_unix, rx_bytes, tx_bytes, active_uplink_id, health_status, health_note, observed_at
    ) values (
      {sql_str(peer_id)}, {sql_str(last_handshake_at)}, {sql_str(last_handshake_unix) if last_handshake_unix not in (None, "") else 'NULL'},
      {sql_int(rx_bytes)}, {sql_int(tx_bytes)}, {sql_str(active_uplink_id)}, {sql_str('healthy' if status == 'active' else 'unknown')},
      {sql_str('mirrored from legacy write-path')}, now()
    )
    on conflict (peer_id) do update set
      last_handshake_at = excluded.last_handshake_at,
      last_handshake_unix = excluded.last_handshake_unix,
      rx_bytes = excluded.rx_bytes,
      tx_bytes = excluded.tx_bytes,
      active_uplink_id = excluded.active_uplink_id,
      health_status = excluded.health_status,
      health_note = excluded.health_note,
      observed_at = excluded.observed_at;

    insert into peer_routing_policy (
      peer_id, ingress_edge_id, policy_mode, preferred_uplink_id, failover_uplink_id, updated_at, updated_by, change_reason
    ) values (
      {sql_str(peer_id)}, {sql_str(ingress_edge_id)}, {sql_str(policy_mode_value)}, {sql_str(preferred_uplink_id)}, {sql_str(failover_uplink_id)}, now(), {sql_str(updated_by)}, {sql_str(change_reason)}
    )
    on conflict (peer_id) do update set
      ingress_edge_id = excluded.ingress_edge_id,
      policy_mode = excluded.policy_mode,
      preferred_uplink_id = excluded.preferred_uplink_id,
      failover_uplink_id = excluded.failover_uplink_id,
      updated_at = excluded.updated_at,
      updated_by = excluded.updated_by,
      change_reason = excluded.change_reason;

    insert into peer_effective_routing (
      peer_id, effective_edge_id, effective_uplink_id, effective_ru_path, effective_notru_path, effective_fallback_path, observed_at
    ) values (
      {sql_str(peer_id)}, {sql_str(effective_edge_id)}, {sql_str(effective_uplink_id)}, {sql_str(effective_ru_path)}, {sql_str(effective_notru_path)}, {sql_str(failover_uplink_id)}, now()
    )
    on conflict (peer_id) do update set
      effective_edge_id = excluded.effective_edge_id,
      effective_uplink_id = excluded.effective_uplink_id,
      effective_ru_path = excluded.effective_ru_path,
      effective_notru_path = excluded.effective_notru_path,
      effective_fallback_path = excluded.effective_fallback_path,
      observed_at = excluded.observed_at;
    commit;
    """
    return db_exec(sql)


def mirror_event_to_db(event_type, peer_id=None, uplink=None, reason=None, metadata=None, actor_id=None, edge_id=None):
    if not (DB_WRITE_MIRROR_ENABLED and DB_WRITE_MIRROR_EVENTS_ENABLED):
        return {"ok": True, "skipped": True}
    edge_id_value = str(edge_id or "edg").strip().lower() or "edg"
    if edge_id_value not in SUPPORTED_EDGES:
        edge_id_value = "edg"
    uplink_id = _effective_uplink_id_for_edge(edge_id_value, uplink, "manual") if uplink else None
    sql = f"""
    insert into events (
      event_type, occurred_at, actor_type, actor_id, peer_id, edge_id, uplink_id, reason, metadata
    ) values (
      {sql_str(event_type)}, now(), 'control_api', {sql_str(actor_id)}, {sql_str(peer_id)}, {sql_str(edge_id_value)}, {sql_str(uplink_id)}, {sql_str(reason)}, {sql_jsonb(metadata)}
    );
    """
    return db_exec(sql)


def db_list_peers():
    sql = """
    select json_build_object(
      'ok', true,
      'source', 'db',
      'items', coalesce(json_agg(row_to_json(t) order by t.created_at, t.id), '[]'::json)
    )
    from (
      select
        p.peer_id as id,
        p.label,
        p.peer_type,
        p.status,
        p.created_at,
        p.pending_expires_at as expires_at,
        p.absolute_expires_at,
        p.absolute_ttl_sec,
        p.allowed_ip::text as allowed_ip,
        p.endpoint,
        p.ttl_sec,
        p.lk_token,
        p.lk_token_created_at,
        p.connected_at,
        prs.last_handshake_unix,
        prs.last_handshake_at,
        prs.rx_bytes,
        prs.tx_bytes,
        host(prs.real_ip) as real_ip,
        coalesce(nullif(prp.ingress_edge_id, ''), nullif(per.effective_edge_id, ''), 'edg') as ingress_edge,
        case
          when prp.policy_mode in ('ams', 'fra', 'nyc') then 'manual'
          else coalesce(nullif(prp.policy_mode, ''), 'auto')
        end as policy_mode,
        coalesce(
          nullif(split_part(prp.preferred_uplink_id, '/', 2), ''),
          case when prp.policy_mode in ('ams', 'fra', 'nyc') then prp.policy_mode else '' end,
          'ams'
        ) as preferred_uplink,
        coalesce(nullif(prp.preferred_uplink_id, ''), 'edg/ams') as preferred_uplink_id,
        coalesce(
          nullif(split_part(prs.active_uplink_id, '/', 2), ''),
          nullif(split_part(per.effective_uplink_id, '/', 2), ''),
          nullif(split_part(prp.preferred_uplink_id, '/', 2), ''),
          'ams'
        ) as active_uplink,
        coalesce(
          nullif(prs.active_uplink_id, ''),
          nullif(per.effective_uplink_id, ''),
          nullif(prp.preferred_uplink_id, ''),
          'edg/ams'
        ) as active_uplink_id,
        coalesce(
          nullif(split_part(per.effective_uplink_id, '/', 2), ''),
          nullif(split_part(prs.active_uplink_id, '/', 2), ''),
          nullif(split_part(prp.preferred_uplink_id, '/', 2), ''),
          'ams'
        ) as effective_uplink,
        coalesce(
          nullif(per.effective_uplink_id, ''),
          nullif(prs.active_uplink_id, ''),
          nullif(prp.preferred_uplink_id, ''),
          'edg/ams'
        ) as effective_uplink_id,
        coalesce(nullif(split_part(prp.failover_uplink_id, '/', 2), ''), '') as failover_uplink,
        coalesce(nullif(prp.failover_uplink_id, ''), '') as failover_uplink_id,
        coalesce(nullif(per.effective_edge_id, ''), nullif(prp.ingress_edge_id, ''), 'edg') as effective_edge,
        coalesce(prs.health_status, '') as health_status,
        coalesce(prs.health_note, '') as health_note
      from peers p
      left join peer_runtime_state prs on prs.peer_id = p.peer_id
      left join peer_routing_policy prp on prp.peer_id = p.peer_id
      left join peer_effective_routing per on per.peer_id = p.peer_id
      order by p.created_at asc, p.peer_id asc
    ) t;
    """
    return db_query_json(sql)


def db_list_uplinks():
    sql = """
    select json_build_object(
      'ok', true,
      'source', 'db',
      'nyc_ips', coalesce((
        select json_agg(t.ip order by t.ip)
        from (
          select split_part(p.allowed_ip::text, '/', 1) as ip
          from peers p
          join peer_effective_routing per on per.peer_id = p.peer_id
          where p.status = 'active'
            and split_part(per.effective_uplink_id, '/', 2) = 'nyc'
          order by 1
        ) t
      ), '[]'::json),
      'fra_ips', coalesce((
        select json_agg(t.ip order by t.ip)
        from (
          select split_part(p.allowed_ip::text, '/', 1) as ip
          from peers p
          join peer_effective_routing per on per.peer_id = p.peer_id
          where p.status = 'active'
            and split_part(per.effective_uplink_id, '/', 2) = 'fra'
          order by 1
        ) t
      ), '[]'::json),
      'items', coalesce(json_agg(row_to_json(t) order by t.edge_id, t.name), '[]'::json)
    )
    from (
      select
        u.uplink_id,
        u.edge_id,
        u.name,
        u.kind,
        u.enabled,
        coalesce(urs.health_status, '') as health_status,
        coalesce(urs.health_note, '') as health_note,
        urs.rtt_ms,
        urs.loss_pct,
        urs.handshake_age_sec,
        urs.last_failed_at,
        urs.last_recovered_at,
        urs.observed_at,
        coalesce((
          select count(*)
          from peer_effective_routing per
          join peers p on p.peer_id = per.peer_id
          where p.status = 'active'
            and per.effective_uplink_id = u.uplink_id
        ), 0) as active_peer_count
      from uplinks u
      left join uplink_runtime_state urs on urs.uplink_id = u.uplink_id
      order by u.edge_id asc, u.name asc
    ) t;
    """
    return db_query_json(sql)


def db_list_edges():
    sql = """
    select json_build_object(
      'ok', true,
      'source', 'db',
      'items', coalesce(json_agg(row_to_json(t) order by t.edge_id), '[]'::json)
    )
    from (
      select
        e.edge_id,
        e.name,
        e.role,
        e.public_host,
        e.client_interface,
        e.client_subnet::text as client_subnet,
        e.ru_egress,
        e.state,
        e.description,
        coalesce(ers.clients_total, 0) as clients_total,
        coalesce(
          ers.clients_active,
          (
            select count(*)
            from peer_effective_routing per
            join peers p on p.peer_id = per.peer_id
            where p.status = 'active'
              and per.effective_edge_id = e.edge_id
          ),
          0
        ) as clients_active,
        coalesce(ers.health_status, '') as health_status,
        coalesce(ers.health_note, '') as health_note,
        ers.last_seen_at,
        ers.observed_at,
        coalesce((
          select json_agg(json_build_object(
            'uplink_id', u.uplink_id,
            'name', u.name,
            'kind', u.kind,
            'enabled', u.enabled,
            'health_status', coalesce(urs.health_status, ''),
            'health_note', coalesce(urs.health_note, ''),
            'active_peer_count', coalesce((
              select count(*)
              from peer_effective_routing per2
              join peers p2 on p2.peer_id = per2.peer_id
              where p2.status = 'active'
                and per2.effective_uplink_id = u.uplink_id
            ), 0)
          ) order by u.name)
          from uplinks u
          left join uplink_runtime_state urs on urs.uplink_id = u.uplink_id
          where u.edge_id = e.edge_id
        ), '[]'::json) as uplinks
      from edges e
      left join edge_runtime_state ers on ers.edge_id = e.edge_id
      order by e.edge_id asc
    ) t;
    """
    return db_query_json(sql)


def _uplink_name_from_id(value):
    raw = str(value or "").strip()
    if not raw:
        return ""
    return raw.split("/", 1)[1].strip().lower() if "/" in raw else raw.lower()


def _normalized_policy_mode(raw):
    mode = str(raw or "").strip().lower()
    if mode in ("ams", "fra", "nyc"):
        return "manual"
    if mode in ("auto", "manual"):
        return mode
    return "auto"


def _edge_endpoint(edge_id):
    edge = str(edge_id or "").strip().lower()
    if edge == "vrn":
        return VRN_EDGE_ENDPOINT
    return os.getenv("WG_ENDPOINT", "85.239.44.100:51820").strip() or "85.239.44.100:51820"


def _effective_uplink_id_for_edge(edge_id, preferred_uplink, policy_mode):
    edge = str(edge_id or "").strip().lower() or "edg"
    preferred = str(preferred_uplink or "").strip().lower() or "ams"
    mode = _normalized_policy_mode(policy_mode)
    if edge == "vrn":
        if preferred in ("ams", "fra", "nyc"):
            return f"vrn/{preferred}"
        return "vrn/ams"
    if mode == "auto":
        return "edg/ams"
    return uplink_id_from_name(preferred)


def _failover_uplink_id_for_edge(edge_id, preferred_uplink):
    edge = str(edge_id or "").strip().lower() or "edg"
    preferred = str(preferred_uplink or "").strip().lower() or "ams"
    if edge == "vrn":
        if preferred == "ams":
            return "vrn/fra"
        if preferred == "fra":
            return "vrn/ams"
        if preferred == "nyc":
            return "vrn/ams"
        return "vrn/fra"
    return failover_uplink_id_from_name(preferred)


def _vrn_route_mode_for_uplink(preferred_uplink, policy_mode):
    preferred = str(preferred_uplink or "").strip().lower() or "ams"
    if preferred in ("ams", "fra", "nyc"):
        return preferred
    return "ams"


def _vrn_supported_uplink(preferred_uplink, policy_mode):
    preferred = str(preferred_uplink or "").strip().lower()
    return preferred in ("ams", "fra", "nyc")


def db_get_peer_uplink(peer_id):
    safe_peer_id = str(peer_id or "").replace("'", "''")
    sql = f"""
    select json_build_object(
      'ok', true,
      'source', 'db',
      'peer_id', t.peer_id,
      'allowed_ip', t.allowed_ip,
      'uplink', t.uplink
    )
    from (
      select
        p.peer_id,
        p.allowed_ip::text as allowed_ip,
        coalesce(
          nullif(split_part(per.effective_uplink_id, '/', 2), ''),
          nullif(split_part(prp.preferred_uplink_id, '/', 2), ''),
          case
            when prp.policy_mode in ('ams', 'fra', 'nyc') then prp.policy_mode
            else 'ams'
          end
        ) as uplink
      from peers p
      left join peer_routing_policy prp on prp.peer_id = p.peer_id
      left join peer_effective_routing per on per.peer_id = p.peer_id
      where p.peer_id = '{safe_peer_id}'
      limit 1
    ) t;
    """
    return db_query_json(sql)


def db_get_peer_routing(peer_id):
    safe_peer_id = str(peer_id or "").replace("'", "''")
    sql = f"""
    select json_build_object(
      'ok', true,
      'source', 'db',
      'peer_id', t.peer_id,
      'allowed_ip', t.allowed_ip,
      'ingress_edge', t.ingress_edge,
      'effective_edge', t.effective_edge,
      'policy_mode', t.policy_mode,
      'preferred_uplink', t.preferred_uplink,
      'preferred_uplink_id', t.preferred_uplink_id,
      'active_uplink', t.active_uplink,
      'active_uplink_id', t.active_uplink_id,
      'effective_uplink', t.effective_uplink,
      'effective_uplink_id', t.effective_uplink_id,
      'failover_uplink', t.failover_uplink,
      'failover_uplink_id', t.failover_uplink_id,
      'health_status', t.health_status,
      'health_note', t.health_note
    )
    from (
      select
        p.peer_id,
        p.allowed_ip::text as allowed_ip,
        coalesce(nullif(prp.ingress_edge_id, ''), nullif(per.effective_edge_id, ''), 'edg') as ingress_edge,
        coalesce(nullif(per.effective_edge_id, ''), nullif(prp.ingress_edge_id, ''), 'edg') as effective_edge,
        case
          when prp.policy_mode in ('ams', 'fra', 'nyc') then 'manual'
          else coalesce(nullif(prp.policy_mode, ''), 'auto')
        end as policy_mode,
        coalesce(
          nullif(split_part(prp.preferred_uplink_id, '/', 2), ''),
          case when prp.policy_mode in ('ams', 'fra', 'nyc') then prp.policy_mode else '' end,
          'ams'
        ) as preferred_uplink,
        coalesce(nullif(prp.preferred_uplink_id, ''), 'edg/ams') as preferred_uplink_id,
        coalesce(
          nullif(split_part(prs.active_uplink_id, '/', 2), ''),
          nullif(split_part(per.effective_uplink_id, '/', 2), ''),
          nullif(split_part(prp.preferred_uplink_id, '/', 2), ''),
          'ams'
        ) as active_uplink,
        coalesce(
          nullif(prs.active_uplink_id, ''),
          nullif(per.effective_uplink_id, ''),
          nullif(prp.preferred_uplink_id, ''),
          'edg/ams'
        ) as active_uplink_id,
        coalesce(
          nullif(split_part(per.effective_uplink_id, '/', 2), ''),
          nullif(split_part(prs.active_uplink_id, '/', 2), ''),
          nullif(split_part(prp.preferred_uplink_id, '/', 2), ''),
          'ams'
        ) as effective_uplink,
        coalesce(
          nullif(per.effective_uplink_id, ''),
          nullif(prs.active_uplink_id, ''),
          nullif(prp.preferred_uplink_id, ''),
          'edg/ams'
        ) as effective_uplink_id,
        coalesce(nullif(split_part(prp.failover_uplink_id, '/', 2), ''), '') as failover_uplink,
        coalesce(nullif(prp.failover_uplink_id, ''), '') as failover_uplink_id,
        coalesce(prs.health_status, '') as health_status,
        coalesce(prs.health_note, '') as health_note
      from peers p
      left join peer_runtime_state prs on prs.peer_id = p.peer_id
      left join peer_routing_policy prp on prp.peer_id = p.peer_id
      left join peer_effective_routing per on per.peer_id = p.peer_id
      where p.peer_id = '{safe_peer_id}'
      limit 1
    ) t;
    """
    return db_query_json(sql)


def db_get_peer(peer_id):
    safe_peer_id = str(peer_id or "").replace("'", "''")
    sql = f"""
    select json_build_object(
      'ok', true,
      'peer_id', p.peer_id,
      'label', p.label,
      'status', p.status,
      'public_key', p.public_key,
      'allowed_ip', p.allowed_ip::text,
      'endpoint', p.endpoint,
      'created_at', p.created_at,
      'pending_expires_at', p.pending_expires_at,
      'ttl_sec', p.ttl_sec,
      'absolute_ttl_sec', p.absolute_ttl_sec,
      'absolute_expires_at', p.absolute_expires_at,
      'lk_token', p.lk_token,
      'lk_token_created_at', p.lk_token_created_at,
      'connected_at', p.connected_at
    )
    from peers p
    where p.peer_id = '{safe_peer_id}'
    limit 1;
    """
    return db_query_json(sql)


def db_set_peer_effective_routing(peer_id, effective_edge_id, effective_uplink_id, effective_ru_path, effective_notru_path, effective_fallback_path):
    pid = str(peer_id or "").strip()
    if not pid:
        return {"ok": False, "error": "invalid_peer_id"}
    sql = f"""
    insert into peer_effective_routing (
      peer_id, effective_edge_id, effective_uplink_id, effective_ru_path, effective_notru_path, effective_fallback_path, observed_at
    ) values (
      {sql_str(pid)}, {sql_str(effective_edge_id)}, {sql_str(effective_uplink_id)}, {sql_str(effective_ru_path)}, {sql_str(effective_notru_path)}, {sql_str(effective_fallback_path)}, now()
    )
    on conflict (peer_id) do update set
      effective_edge_id = excluded.effective_edge_id,
      effective_uplink_id = excluded.effective_uplink_id,
      effective_ru_path = excluded.effective_ru_path,
      effective_notru_path = excluded.effective_notru_path,
      effective_fallback_path = excluded.effective_fallback_path,
      observed_at = excluded.observed_at;
    """
    return db_exec(sql)


def db_update_peer_routing_intent(peer_id, policy_mode=None, preferred_uplink=None, ingress_edge=None, updated_by="control-api", change_reason=None):
    pid = str(peer_id or "").strip()
    if not pid:
        return {"ok": False, "error": "invalid_peer_id"}
    current = {}
    got = db_get_peer_routing(pid)
    if got.get("ok") and isinstance(got.get("value"), dict):
        current = dict(got.get("value") or {})
    mode = _normalized_policy_mode(policy_mode if policy_mode is not None else current.get("policy_mode"))
    preferred = str(preferred_uplink if preferred_uplink is not None else current.get("preferred_uplink") or "ams").strip().lower()
    if preferred not in ("ams", "fra", "nyc"):
        return {"ok": False, "error": "invalid_preferred_uplink"}
    if mode not in ("auto", "manual"):
        return {"ok": False, "error": "invalid_policy_mode"}
    failover = failover_uplink_id_from_name(preferred)
    ingress_edge_value = str(ingress_edge if ingress_edge is not None else current.get("ingress_edge") or "edg").strip().lower() or "edg"
    if ingress_edge_value not in SUPPORTED_EDGES:
        return {"ok": False, "error": "invalid_ingress_edge"}
    sql = f"""
    insert into peer_routing_policy (
      peer_id, ingress_edge_id, policy_mode, preferred_uplink_id, failover_uplink_id, updated_at, updated_by, change_reason
    ) values (
      {sql_str(pid)}, {sql_str(ingress_edge_value)}, {sql_str(mode)}, {sql_str(uplink_id_from_name(preferred))}, {sql_str(failover)}, now(), {sql_str(updated_by)}, {sql_str(change_reason)}
    )
    on conflict (peer_id) do update set
      ingress_edge_id = excluded.ingress_edge_id,
      policy_mode = excluded.policy_mode,
      preferred_uplink_id = excluded.preferred_uplink_id,
      failover_uplink_id = excluded.failover_uplink_id,
      updated_at = excluded.updated_at,
      updated_by = excluded.updated_by,
      change_reason = excluded.change_reason;
    """
    out = db_exec(sql)
    if not out.get("ok"):
        return out
    return {"ok": True, "peer_id": pid, "ingress_edge": ingress_edge_value, "policy_mode": mode, "preferred_uplink": preferred}


def get_peer_write_edge(peer_id):
    current = db_get_peer_routing(peer_id)
    if current.get("ok") and isinstance(current.get("value"), dict):
        row = dict(current.get("value") or {})
        effective = str(row.get("effective_edge") or "").strip().lower()
        if effective in SUPPORTED_EDGES:
            return effective
        ingress = str(row.get("ingress_edge") or "").strip().lower()
        if ingress in SUPPORTED_EDGES:
            return ingress
    return "edg"


def apply_vrn_peer_route(ip_short, preferred_uplink, policy_mode):
    route_mode = _vrn_route_mode_for_uplink(preferred_uplink, policy_mode)
    out = run_ssh(
        VRN_EDGE_SSH_HOST,
        "sudo {route_bin} {ip} {mode}".format(
            route_bin=shlex.quote(VRN_EDGE_ROUTE_BIN),
            ip=shlex.quote(ip_short),
            mode=shlex.quote(route_mode),
        ),
    )
    if not out.get("ok"):
        return {"ok": False, "error": one_line(out.get("stderr") or out.get("stdout") or "vrn_route_apply_failed")}
    return {"ok": True, "route_mode": route_mode}


def create_peer_on_vrn(label, ttl_sec=None, expire_sec=None, preferred_uplink="nyc", policy_mode="manual"):
    preferred = str(preferred_uplink or "").strip().lower() or "nyc"
    mode = _normalized_policy_mode(policy_mode or "manual")
    if not _vrn_supported_uplink(preferred, mode):
        return {"ok": False, "error": "vrn_gateway_unsupported_uplink"}
    args = ["create", "--label", label]
    if ttl_sec is not None:
        args += ["--ttl-sec", str(int(ttl_sec))]
    if expire_sec is not None:
        args += ["--expire-sec", str(int(expire_sec))]
    out = run_vrn_cli(args)
    if not out.get("ok"):
        return out
    peer_id = str(out.get("id") or "").strip()
    lst = run_vrn_cli(["list"])
    peer = legacy_find_peer(peer_id, lst)
    if not peer:
        return {"ok": False, "error": "vrn_created_peer_not_found"}
    route_out = apply_vrn_peer_route(_peer_ip_short(peer.get("allowed_ip")), preferred, mode)
    if not route_out.get("ok"):
        return route_out
    out["edge_id"] = "vrn"
    out["endpoint"] = peer.get("endpoint") or VRN_EDGE_ENDPOINT
    out["server_public_key"] = peer.get("server_public_key") or ""
    out["config"] = peer.get("config") or out.get("config")
    out["preferred_uplink"] = preferred
    out["policy_mode"] = mode
    out["effective_edge"] = "vrn"
    out["effective_uplink"] = "nyc" if preferred == "nyc" else "eth0"
    out["vrn_route_mode"] = route_out.get("route_mode")
    out["_peer"] = peer
    return out


def remove_peer_on_vrn(peer_id):
    pid = str(peer_id or "").strip()
    if not pid:
        return {"ok": False, "error": "invalid_peer_id"}
    lst_before = run_vrn_cli(["list"])
    peer_before = legacy_find_peer(pid, lst_before)
    out = run_vrn_cli(["remove", "--id", pid])
    if not out.get("ok"):
        return out
    if peer_before:
        apply_vrn_peer_route(_peer_ip_short(peer_before.get("allowed_ip")), "ams", "auto")
    lst_after = run_vrn_cli(["list"])
    peer_after = legacy_find_peer(pid, lst_after)
    out["_peer"] = peer_after or peer_before or {}
    out["effective_edge"] = "vrn"
    return out


def get_peer_item_for_edge(peer_id, edge_id):
    pid = str(peer_id or "").strip()
    edge = str(edge_id or "").strip().lower() or "edg"
    if not pid:
        return None
    lst = run_vrn_cli(["list"]) if edge == "vrn" else run_cli(["list"])
    return legacy_find_peer(pid, lst)


def set_peer_ingress_edge(peer_id, ingress_edge):
    pid = str(peer_id or "").strip()
    edge = str(ingress_edge or "").strip().lower()
    if not pid:
        return {"ok": False, "error": "invalid_peer_id"}
    if edge not in SUPPORTED_EDGES:
        return {"ok": False, "error": "invalid_ingress_edge"}
    mirrored = db_update_peer_routing_intent(
        pid,
        ingress_edge=edge,
        updated_by="control-api",
        change_reason="set_ingress_edge",
    )
    if not mirrored.get("ok"):
        return mirrored
    current = db_get_peer_routing(pid)
    effective_edge = edge
    if current.get("ok") and isinstance(current.get("value"), dict):
        effective_edge = str(current.get("value", {}).get("effective_edge") or edge).strip().lower() or edge
    return {
        "ok": True,
        "peer_id": pid,
        "ingress_edge": edge,
        "effective_edge": effective_edge,
        "intent_only": True,
    }


def set_peer_routing_policy(peer_id, allowed_ip, policy_mode, preferred_uplink=None):
    pid = str(peer_id or "").strip()
    ip_short = _peer_ip_short(allowed_ip)
    mode = _normalized_policy_mode(policy_mode)
    preferred = str(preferred_uplink or "").strip().lower()
    edge = get_peer_write_edge(pid)
    if not pid:
        return {"ok": False, "error": "invalid_peer_id"}
    if mode not in ("auto", "manual"):
        return {"ok": False, "error": "invalid_policy_mode"}
    if not preferred:
        current = db_get_peer_routing(pid)
        if current.get("ok") and isinstance(current.get("value"), dict):
            preferred = str(current.get("value", {}).get("preferred_uplink") or "").strip().lower()
    if mode == "auto":
        preferred = preferred or "ams"
    elif mode == "manual":
        preferred = preferred or "ams"
    if preferred not in ("ams", "fra", "nyc"):
        return {"ok": False, "error": "invalid_preferred_uplink"}
    if edge == "vrn":
        if not _vrn_supported_uplink(preferred, mode):
            return {"ok": False, "error": "vrn_gateway_unsupported_uplink"}
        applied = apply_vrn_peer_route(ip_short, preferred, mode)
        if not applied.get("ok"):
            return applied
        effective_uplink_id = _effective_uplink_id_for_edge("vrn", preferred, mode)
        failover_uplink_id = _failover_uplink_id_for_edge("vrn", preferred)
        mirrored = db_update_peer_routing_intent(pid, policy_mode=mode, preferred_uplink=preferred, updated_by="control-api", change_reason="set_policy_mode")
        if not mirrored.get("ok"):
            return mirrored
        routed = db_set_peer_effective_routing(
            pid,
            "vrn",
            effective_uplink_id,
            "direct",
            "nyc" if _uplink_name_from_id(effective_uplink_id) == "nyc" else "direct",
            failover_uplink_id,
        )
        if not routed.get("ok"):
            return routed
        return {
            "ok": True,
            "peer_id": pid,
            "allowed_ip": ip_short,
            "policy_mode": mode,
            "preferred_uplink": preferred,
            "effective_apply": _uplink_name_from_id(effective_uplink_id),
            "effective_edge": "vrn",
        }
    legacy_apply = "ams" if mode == "auto" else preferred
    applied = set_peer_uplink(ip_short, legacy_apply)
    if not applied.get("ok"):
        return applied
    mirrored = db_update_peer_routing_intent(pid, policy_mode=mode, preferred_uplink=preferred, updated_by="control-api", change_reason="set_policy_mode")
    if not mirrored.get("ok"):
        return mirrored
    return {
        "ok": True,
        "peer_id": pid,
        "allowed_ip": ip_short,
        "policy_mode": mode,
        "preferred_uplink": preferred,
        "effective_apply": legacy_apply,
    }


def set_peer_ingress_edge_via_routing(peer_id, ingress_edge):
    pid = str(peer_id or "").strip()
    edge = str(ingress_edge or "").strip().lower()
    if not pid:
        return {"ok": False, "error": "invalid_peer_id"}
    if edge not in SUPPORTED_EDGES:
        return {"ok": False, "error": "invalid_ingress_edge"}
    mirrored = db_update_peer_routing_intent(
        pid,
        ingress_edge=edge,
        updated_by="control-api",
        change_reason="set_ingress_edge",
    )
    if not mirrored.get("ok"):
        return mirrored
    current = db_get_peer_routing(pid)
    effective_edge = edge
    if current.get("ok") and isinstance(current.get("value"), dict):
        effective_edge = str(current.get("value", {}).get("effective_edge") or edge).strip().lower() or edge
    return {"ok": True, "peer_id": pid, "ingress_edge": edge, "effective_edge": effective_edge, "intent_only": True}


def set_peer_preferred_uplink(peer_id, allowed_ip, preferred_uplink, current_policy_mode):
    pid = str(peer_id or "").strip()
    ip_short = _peer_ip_short(allowed_ip)
    preferred = str(preferred_uplink or "").strip().lower()
    mode = _normalized_policy_mode(current_policy_mode or "auto")
    edge = get_peer_write_edge(pid)
    if not pid:
        return {"ok": False, "error": "invalid_peer_id"}
    if preferred not in ("ams", "fra", "nyc"):
        return {"ok": False, "error": "invalid_preferred_uplink"}
    if mode not in ("auto", "manual"):
        return {"ok": False, "error": "invalid_policy_mode"}
    if edge == "vrn":
        if not _vrn_supported_uplink(preferred, mode):
            return {"ok": False, "error": "vrn_gateway_unsupported_uplink"}
        applied = apply_vrn_peer_route(ip_short, preferred, mode)
        if not applied.get("ok"):
            return applied
        mirrored = db_update_peer_routing_intent(pid, policy_mode=mode, preferred_uplink=preferred, updated_by="control-api", change_reason="set_preferred_uplink")
        if not mirrored.get("ok"):
            return mirrored
        effective_uplink_id = _effective_uplink_id_for_edge("vrn", preferred, mode)
        failover_uplink_id = _failover_uplink_id_for_edge("vrn", preferred)
        routed = db_set_peer_effective_routing(
            pid,
            "vrn",
            effective_uplink_id,
            "direct",
            "nyc" if _uplink_name_from_id(effective_uplink_id) == "nyc" else "direct",
            failover_uplink_id,
        )
        if not routed.get("ok"):
            return routed
        return {
            "ok": True,
            "peer_id": pid,
            "allowed_ip": ip_short,
            "policy_mode": mode,
            "preferred_uplink": preferred,
            "effective_apply": _uplink_name_from_id(effective_uplink_id),
            "effective_edge": "vrn",
            **({"intent_only": True} if mode == "auto" else {}),
        }
    legacy_apply = "ams" if mode == "auto" else preferred
    if mode == "manual":
        applied = set_peer_uplink(ip_short, legacy_apply)
        if not applied.get("ok"):
            return applied
    else:
        applied = {"ok": True, "ip": ip_short, "uplink": legacy_apply, "intent_only": True}
    mirrored = db_update_peer_routing_intent(pid, policy_mode=mode, preferred_uplink=preferred, updated_by="control-api", change_reason="set_preferred_uplink")
    if not mirrored.get("ok"):
        return mirrored
    return {
        "ok": True,
        "peer_id": pid,
        "allowed_ip": ip_short,
        "policy_mode": mode,
        "preferred_uplink": preferred,
        "effective_apply": legacy_apply,
        **({"intent_only": True} if mode == "auto" else {}),
    }


def _safe_event_type_values(values):
    out = []
    for raw in values or []:
        txt = str(raw or "").strip()
        if not txt:
            continue
        if not re.fullmatch(r"[A-Za-z0-9_:-]+", txt):
            continue
        out.append(txt)
    return out


def db_list_events(limit=100, event_types=None):
    try:
        lim = max(1, min(int(limit), 500))
    except Exception:
        lim = 100
    safe_event_types = _safe_event_type_values(event_types)
    where_sql = ""
    if safe_event_types:
        quoted = ", ".join("'" + x.replace("'", "''") + "'" for x in safe_event_types)
        where_sql = f"where event_type in ({quoted})"
    sql = f"""
    select json_build_object(
      'ok', true,
      'source', 'db',
      'limit', {lim},
      'items', coalesce(json_agg(row_to_json(t) order by t.occurred_at desc, t.event_id desc), '[]'::json)
    )
    from (
      select
        event_id,
        event_type,
        occurred_at,
        actor_type,
        actor_id,
        peer_id,
        edge_id,
        uplink_id,
        request_id,
        reason,
        metadata
      from events
      {where_sql}
      order by occurred_at desc, event_id desc
      limit {lim}
    ) t;
    """
    return db_query_json(sql)


def get_peers_payload():
    if DB_READ_ENABLED and DB_READ_PEERS_ENABLED:
        db = db_list_peers()
        if db.get("ok") and isinstance(db.get("value"), dict):
            return db["value"]
    return run_cli(["list"])


def get_uplinks_payload():
    if DB_READ_ENABLED and DB_READ_UPLINKS_ENABLED:
        db = db_list_uplinks()
        if db.get("ok") and isinstance(db.get("value"), dict):
            return db["value"]
    forced_nyc = get_forced_peer_ips("nyc")
    forced_fra = get_forced_peer_ips("fra")
    if not forced_nyc.get("ok") or not forced_fra.get("ok"):
        return forced_nyc if not forced_nyc.get("ok") else forced_fra
    return {
        "ok": True,
        "source": "legacy",
        "nyc_ips": forced_nyc.get("items", []),
        "fra_ips": forced_fra.get("items", []),
    }


def get_edges_payload():
    if DB_READ_ENABLED and DB_READ_UPLINKS_ENABLED:
        db = db_list_edges()
        if db.get("ok") and isinstance(db.get("value"), dict):
            return db["value"]
        return db
    return {"ok": False, "error": "db edges read disabled"}


def get_events_payload(limit=100, event_types=None):
    if DB_READ_ENABLED and DB_READ_EVENTS_ENABLED:
        db = db_list_events(limit=limit, event_types=event_types)
        if db.get("ok") and isinstance(db.get("value"), dict):
            return db["value"]
        return db
    return {"ok": False, "error": "db events read disabled"}


def one_line(s, max_len=240):
    txt = " ".join(str(s or "").split())
    if len(txt) > max_len:
        return txt[: max_len - 1] + "…"
    return txt


def _peer_ip_short(value):
    raw = str(value or "").strip()
    if not raw:
        return ""
    short = raw.split("/", 1)[0].strip()
    if re.fullmatch(r"(?:\d{1,3}\.){3}\d{1,3}", short):
        return short
    return ""


def _force_comment_for_ip(ip_short):
    return f"{NFT_UPLINK_COMMENT_PREFIX_NYC}{ip_short}"


def _force_comment_for_ip_fra(ip_short):
    return f"{NFT_UPLINK_COMMENT_PREFIX_FRA}{ip_short}"


def get_forced_peer_ips(uplink):
    route = str(uplink or "").strip().lower()
    if route == "nyc":
        want_mark = "0x00000002"
        want_prefix = NFT_UPLINK_COMMENT_PREFIX_NYC
    elif route == "fra":
        want_mark = "0x00000003"
        want_prefix = NFT_UPLINK_COMMENT_PREFIX_FRA
    else:
        return {"ok": False, "error": "invalid_uplink"}
    out = nft_cmd("-a", "list", "chain", "inet", NFT_TABLE, NFT_FORCE_CHAIN)
    if not out.get("ok"):
        return {"ok": False, "error": one_line(out.get("stderr") or out.get("stdout") or "nft_list_failed")}
    items = set()
    for line in (out.get("stdout") or "").splitlines():
        if 'iifname "wg0"' not in line:
            continue
        if f"meta mark set {want_mark}" not in line:
            continue
        if "ip saddr " not in line:
            continue
        if want_prefix and want_prefix not in line:
            continue
        try:
            part = line.split("ip saddr ", 1)[1]
            ip_short = _peer_ip_short(part.split(" ", 1)[0].strip())
        except Exception:
            ip_short = ""
        if ip_short:
            items.add(ip_short)
    return {"ok": True, "items": sorted(items)}


def set_peer_uplink(ip_short, uplink):
    ip_short = _peer_ip_short(ip_short)
    route = str(uplink or "").strip().lower()
    if not ip_short:
        return {"ok": False, "error": "invalid_peer_ip"}
    if route not in ("ams", "nyc", "fra"):
        return {"ok": False, "error": "invalid_uplink"}

    listed = nft_cmd("-a", "list", "chain", "inet", NFT_TABLE, NFT_FORCE_CHAIN)
    if not listed.get("ok"):
        return {"ok": False, "error": one_line(listed.get("stderr") or listed.get("stdout") or "nft_list_failed")}

    handles = []
    for line in (listed.get("stdout") or "").splitlines():
        if f'ip saddr {ip_short} ' not in line:
            continue
        if (
            NFT_UPLINK_COMMENT_PREFIX_NYC not in line
            and NFT_UPLINK_COMMENT_PREFIX_FRA not in line
        ):
            continue
        if "# handle " not in line:
            continue
        try:
            handles.append(line.rsplit("# handle ", 1)[1].strip())
        except Exception:
            continue

    for handle in handles:
        deleted = nft_cmd("delete", "rule", "inet", NFT_TABLE, NFT_FORCE_CHAIN, "handle", handle)
        if not deleted.get("ok"):
            return {"ok": False, "error": one_line(deleted.get("stderr") or deleted.get("stdout") or f"nft_delete_failed:{handle}")}

    if route == "nyc":
        added = nft_cmd(
            "add",
            "rule",
            "inet",
            NFT_TABLE,
            NFT_FORCE_CHAIN,
            "iifname",
            "wg0",
            "ip",
            "saddr",
            ip_short,
            "meta",
            "mark",
            "set",
            "0x00000002",
            "comment",
            _force_comment_for_ip(ip_short),
        )
        if not added.get("ok"):
            return {"ok": False, "error": one_line(added.get("stderr") or added.get("stdout") or "nft_add_failed")}
    elif route == "fra":
        added = nft_cmd(
            "add",
            "rule",
            "inet",
            NFT_TABLE,
            NFT_FORCE_CHAIN,
            "iifname",
            "wg0",
            "ip",
            "saddr",
            ip_short,
            "meta",
            "mark",
            "set",
            "0x00000003",
            "comment",
            _force_comment_for_ip_fra(ip_short),
        )
        if not added.get("ok"):
            return {"ok": False, "error": one_line(added.get("stderr") or added.get("stdout") or "nft_add_failed")}

    return {"ok": True, "ip": ip_short, "uplink": route}


def json_bytes(obj):
    return json.dumps(obj, ensure_ascii=False).encode("utf-8")


def utc_iso():
    return dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def audit(event, **fields):
    rec = {"ts": utc_iso(), "event": str(event)}
    for k, v in fields.items():
        if v is None:
            continue
        rec[str(k)] = v
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    fd = os.open(str(AUDIT_LOCK), os.O_CREAT | os.O_RDWR, 0o600)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX)
        with AUDIT_LOG.open("a", encoding="utf-8") as f:
            f.write(json.dumps(rec, ensure_ascii=False) + "\n")
    finally:
        os.close(fd)


class H(BaseHTTPRequestHandler):
    def _client_ip(self):
        xff = (self.headers.get("X-Forwarded-For", "") or "").strip()
        if xff:
            return xff.split(",")[0].strip()
        xr = (self.headers.get("X-Real-IP", "") or "").strip()
        if xr:
            return xr
        return self.client_address[0] if self.client_address else ""

    def _send(self, code, obj):
        b = json_bytes(obj)
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def _read_json(self):
        n = int(self.headers.get("Content-Length", "0") or "0")
        if n <= 0:
            return {}
        raw = self.rfile.read(n).decode("utf-8", errors="replace")
        try:
            return json.loads(raw)
        except Exception:
            return None

    def _is_auth_ok(self):
        if not API_TOKEN:
            return False
        return (self.headers.get("X-API-Token", "") or "").strip() == API_TOKEN

    def _unauth(self):
        audit("api_unauthorized", ip=self._client_ip(), path=urlparse(self.path).path, method=self.command)
        self._send(401, {"ok": False, "error": "unauthorized"})

    def log_message(self, fmt, *args):
        try:
            req = args[2]
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
        if p == f"/{API_VERSION}/health":
            return self._send(200, {"ok": True, "service": "wg-control-api", "version": API_VERSION, "ts": utc_iso()})

        if not self._is_auth_ok():
            return self._unauth()

        if p == f"/{API_VERSION}/peers":
            out = get_peers_payload()
            audit("api_list", ip=self._client_ip(), ok=bool(out.get("ok")), source=out.get("source", "legacy"))
            return self._send(200 if out.get("ok") else 500, out)

        if p == f"/{API_VERSION}/uplinks":
            out = get_uplinks_payload()
            if not out.get("ok"):
                audit("api_list_uplinks", ip=self._client_ip(), ok=False, reason=out.get("error") or "uplink_read_failed")
                return self._send(500, out)
            audit(
                "api_list_uplinks",
                ip=self._client_ip(),
                ok=True,
                source=out.get("source", "legacy"),
                nyc_count=len(out.get("nyc_ips", [])),
                fra_count=len(out.get("fra_ips", [])),
            )
            return self._send(200, out)

        if p == f"/{API_VERSION}/edges":
            out = get_edges_payload()
            if not out.get("ok"):
                audit("api_list_edges", ip=self._client_ip(), ok=False, reason=out.get("error") or "edges_read_failed")
                return self._send(501 if out.get("error") == "db edges read disabled" else 500, out)
            audit("api_list_edges", ip=self._client_ip(), ok=True, source=out.get("source", "db"), count=len(out.get("items", [])))
            return self._send(200, out)

        if p == f"/{API_VERSION}/events":
            qs = parse_qs(parsed.query or "")
            try:
                limit = int((qs.get("limit") or ["100"])[0])
            except Exception:
                limit = 100
            event_types = []
            for raw in (qs.get("event_type") or []):
                event_types.extend([x for x in str(raw or "").split(",") if x.strip()])
            out = get_events_payload(limit=limit, event_types=event_types)
            if not out.get("ok"):
                audit("api_list_events", ip=self._client_ip(), ok=False, reason=out.get("error") or "events_read_failed", limit=limit)
                return self._send(501 if out.get("error") == "db events read disabled" else 500, out)
            audit("api_list_events", ip=self._client_ip(), ok=True, source=out.get("source", "db"), limit=limit, event_type=",".join(_safe_event_type_values(event_types)))
            return self._send(200, out)

        m = re.match(rf"^/{API_VERSION}/peers/([A-Za-z0-9_-]+)/uplink$", p)
        if m:
            pid = m.group(1)
            if DB_READ_ENABLED and DB_READ_PEERS_ENABLED and DB_READ_UPLINKS_ENABLED:
                db = db_get_peer_uplink(pid)
                if db.get("ok") and isinstance(db.get("value"), dict):
                    out = db["value"]
                    if out.get("peer_id"):
                        audit(
                            "api_get_uplink",
                            ip=self._client_ip(),
                            ok=True,
                            source=out.get("source", "db"),
                            peer_id=pid,
                            uplink=out.get("uplink"),
                            allowed_ip=out.get("allowed_ip"),
                        )
                        return self._send(200, out)
            lst = run_cli(["list"])
            if not lst.get("ok"):
                return self._send(500, lst)
            item = None
            for x in lst.get("items", []):
                if str(x.get("id") or "") == pid:
                    item = x
                    break
            if not item:
                audit("api_get_uplink", ip=self._client_ip(), ok=False, peer_id=pid, reason="peer_not_found")
                return self._send(404, {"ok": False, "error": "peer not found"})
            ip_short = _peer_ip_short(item.get("allowed_ip"))
            forced_nyc = get_forced_peer_ips("nyc")
            forced_fra = get_forced_peer_ips("fra")
            if not forced_nyc.get("ok") or not forced_fra.get("ok"):
                audit("api_get_uplink", ip=self._client_ip(), ok=False, peer_id=pid, reason="nft_list_failed")
                return self._send(500, forced_nyc if not forced_nyc.get("ok") else forced_fra)
            nyc_ips = set(forced_nyc.get("items", []))
            fra_ips = set(forced_fra.get("items", []))
            if ip_short and ip_short in fra_ips:
                uplink = "fra"
            elif ip_short and ip_short in nyc_ips:
                uplink = "nyc"
            else:
                uplink = "ams"
            out = {"ok": True, "peer_id": pid, "allowed_ip": str(item.get("allowed_ip") or ""), "uplink": uplink}
            audit("api_get_uplink", ip=self._client_ip(), ok=True, peer_id=pid, uplink=uplink, allowed_ip=out["allowed_ip"])
            return self._send(200, out)

        m = re.match(rf"^/{API_VERSION}/peers/([A-Za-z0-9_-]+)/routing$", p)
        if m:
            pid = m.group(1)
            if DB_READ_ENABLED and DB_READ_PEERS_ENABLED and DB_READ_UPLINKS_ENABLED:
                db = db_get_peer_routing(pid)
                if db.get("ok") and isinstance(db.get("value"), dict):
                    out = db["value"]
                    if out.get("peer_id"):
                        audit(
                            "api_get_routing",
                            ip=self._client_ip(),
                            ok=True,
                            source=out.get("source", "db"),
                            peer_id=pid,
                            ingress_edge=out.get("ingress_edge"),
                            preferred_uplink=out.get("preferred_uplink"),
                            effective_uplink=out.get("effective_uplink"),
                        )
                        return self._send(200, out)
            audit("api_get_routing", ip=self._client_ip(), ok=False, peer_id=pid, reason="routing_read_unavailable")
            return self._send(501, {"ok": False, "error": "routing_read_unavailable"})

        return self._send(404, {"ok": False, "error": "not found"})

    def do_POST(self):
        p = urlparse(self.path).path

        if not self._is_auth_ok():
            return self._unauth()

        if p == f"/{API_VERSION}/peers/create":
            body = self._read_json()
            if body is None:
                return self._send(400, {"ok": False, "error": "invalid json"})
            label = str((body.get("label") or "web")).strip()[:64]
            gateway = str((body.get("gateway") or "edg")).strip().lower() or "edg"
            preferred_uplink = str((body.get("uplink") or "ams")).strip().lower() or "ams"
            ttl_sec = body.get("ttl_sec")
            expire_sec = body.get("expire_sec")
            if gateway not in SUPPORTED_EDGES:
                return self._send(400, {"ok": False, "error": "invalid_gateway"})
            if gateway == "vrn":
                vrn_mode = "manual" if preferred_uplink == "nyc" else "auto"
                out = create_peer_on_vrn(label, ttl_sec=ttl_sec, expire_sec=expire_sec, preferred_uplink=preferred_uplink, policy_mode=vrn_mode)
            else:
                args = ["create", "--label", label]
                if ttl_sec is not None:
                    args += ["--ttl-sec", str(int(ttl_sec))]
                if expire_sec is not None:
                    args += ["--expire-sec", str(int(expire_sec))]
                out = run_cli(args)
            mirror_error = None
            if out.get("ok"):
                peer = dict(out.get("_peer") or {})
                if not peer:
                    lst = run_cli(["list"]) if gateway == "edg" else run_vrn_cli(["list"])
                    peer = legacy_find_peer(out.get("id"), lst)
                if peer:
                    mirrored = mirror_peer_to_db(
                        peer,
                        preferred_uplink=preferred_uplink,
                        policy_mode=("manual" if preferred_uplink == "nyc" and gateway == "vrn" else "auto"),
                        change_reason="api_create",
                        ingress_edge=gateway,
                        effective_edge=gateway,
                    )
                    if not mirrored.get("ok"):
                        mirror_error = mirrored.get("error") or "db_write_mirror_failed"
                    ev = mirror_event_to_db(
                        "api_create",
                        peer_id=out.get("id"),
                        uplink=preferred_uplink,
                        reason="api_create",
                        metadata={"label": label, "allowed_ip": peer.get("allowed_ip"), "gateway": gateway},
                        actor_id=self._client_ip(),
                        edge_id=gateway,
                    )
                    if not ev.get("ok") and not mirror_error:
                        mirror_error = ev.get("error") or "db_event_mirror_failed"
                else:
                    mirror_error = "created_peer_not_found_in_edge_list"
            if mirror_error:
                out["mirror_warning"] = mirror_error
            audit("api_create", ip=self._client_ip(), ok=bool(out.get("ok")), label=label, peer_id=out.get("id"), gateway=gateway, uplink=preferred_uplink)
            if "_peer" in out:
                out.pop("_peer", None)
            return self._send(200 if out.get("ok") else (400 if out.get("error") in ("invalid_gateway", "vrn_gateway_unsupported_uplink") else 500), out)

        m = re.match(rf"^/{API_VERSION}/peers/([A-Za-z0-9_-]+)/block$", p)
        if m:
            pid = m.group(1)
            write_edge = get_peer_write_edge(pid)
            out = run_vrn_cli(["block", "--id", pid]) if write_edge == "vrn" else run_cli(["block", "--id", pid])
            if out.get("ok"):
                lst = run_vrn_cli(["list"]) if write_edge == "vrn" else run_cli(["list"])
                peer = legacy_find_peer(pid, lst)
                mirror_error = None
                if peer:
                    mirrored = mirror_peer_to_db(peer, change_reason="api_block", ingress_edge=write_edge, effective_edge=write_edge)
                    if not mirrored.get("ok"):
                        mirror_error = mirrored.get("error") or "db_write_mirror_failed"
                ev = mirror_event_to_db("api_block", peer_id=pid, reason="api_block", metadata={"status": out.get("status")}, actor_id=self._client_ip(), edge_id=write_edge)
                if not ev.get("ok") and not mirror_error:
                    mirror_error = ev.get("error") or "db_event_mirror_failed"
                if mirror_error:
                    out["mirror_warning"] = mirror_error
            audit("api_block", ip=self._client_ip(), ok=bool(out.get("ok")), peer_id=pid, gateway=write_edge)
            return self._send(200 if out.get("ok") else 500, out)

        m = re.match(rf"^/{API_VERSION}/peers/([A-Za-z0-9_-]+)/remove$", p)
        if m:
            pid = m.group(1)
            write_edge = get_peer_write_edge(pid)
            out = remove_peer_on_vrn(pid) if write_edge == "vrn" else run_cli(["remove", "--id", pid])
            if out.get("ok"):
                peer = dict(out.get("_peer") or {})
                if not peer:
                    lst = run_vrn_cli(["list"]) if write_edge == "vrn" else run_cli(["list"])
                    peer = legacy_find_peer(pid, lst)
                mirror_error = None
                if peer:
                    mirrored = mirror_peer_to_db(peer, change_reason="api_remove", ingress_edge=write_edge, effective_edge=write_edge)
                    if not mirrored.get("ok"):
                        mirror_error = mirrored.get("error") or "db_write_mirror_failed"
                ev = mirror_event_to_db("api_remove", peer_id=pid, reason="api_remove", metadata={"status": out.get("status")}, actor_id=self._client_ip(), edge_id=write_edge)
                if not ev.get("ok") and not mirror_error:
                    mirror_error = ev.get("error") or "db_event_mirror_failed"
                if mirror_error:
                    out["mirror_warning"] = mirror_error
            audit("api_remove", ip=self._client_ip(), ok=bool(out.get("ok")), peer_id=pid, gateway=write_edge)
            out.pop("_peer", None)
            return self._send(200 if out.get("ok") else 500, out)

        m = re.match(rf"^/{API_VERSION}/peers/([A-Za-z0-9_-]+)/reissue$", p)
        if m:
            old_id = m.group(1)
            body = self._read_json()
            if body is None:
                return self._send(400, {"ok": False, "error": "invalid json"})
            remove_old = bool(body.get("remove_old", False))
            gateway = str(body.get("gateway") or "edg").strip().lower() or "edg"
            preferred_uplink = str(body.get("uplink") or "ams").strip().lower() or "ams"
            if gateway not in SUPPORTED_EDGES:
                return self._send(400, {"ok": False, "error": "invalid_gateway"})

            item = get_peer_item_for_edge(old_id, get_peer_write_edge(old_id))
            if not item:
                audit("api_reissue", ip=self._client_ip(), ok=False, peer_id=old_id, reason="peer_not_found")
                return self._send(404, {"ok": False, "error": "peer not found"})

            old_label = str(item.get("label") or "web")
            new_label = (old_label + "-reissue")[:64]
            abs_ttl = int(item.get("absolute_ttl_sec") or 0)
            if gateway == "vrn":
                create_mode = "manual" if preferred_uplink == "nyc" else "auto"
                created = create_peer_on_vrn(new_label, ttl_sec=(abs_ttl if abs_ttl > 0 else None), expire_sec=(abs_ttl if abs_ttl > 0 else None), preferred_uplink=preferred_uplink, policy_mode=create_mode)
            else:
                if abs_ttl > 0:
                    created = run_cli(["create", "--label", new_label, "--ttl-sec", str(abs_ttl), "--expire-sec", str(abs_ttl)])
                else:
                    created = run_cli(["create", "--label", new_label])
            if not created.get("ok"):
                audit("api_reissue", ip=self._client_ip(), ok=False, peer_id=old_id, reason="create_failed")
                return self._send(400 if created.get("error") == "vrn_gateway_unsupported_uplink" else 500, created)

            removed = None
            if remove_old:
                old_edge = get_peer_write_edge(old_id)
                removed = remove_peer_on_vrn(old_id) if old_edge == "vrn" else run_cli(["remove", "--id", old_id])

            mirror_error = None
            new_peer = dict(created.get("_peer") or {})
            if not new_peer:
                new_peer = get_peer_item_for_edge(created.get("id"), gateway)
            if new_peer:
                mirrored = mirror_peer_to_db(
                    new_peer,
                    preferred_uplink=preferred_uplink,
                    policy_mode=("manual" if gateway == "vrn" and preferred_uplink == "nyc" else "auto"),
                    change_reason="api_reissue_create",
                    ingress_edge=gateway,
                    effective_edge=gateway,
                )
                if not mirrored.get("ok"):
                    mirror_error = mirrored.get("error") or "db_write_mirror_failed"
            if remove_old:
                old_edge = get_peer_write_edge(old_id)
                old_peer = dict(removed.get("_peer") or {}) if isinstance(removed, dict) else {}
                if not old_peer:
                    old_peer = get_peer_item_for_edge(old_id, old_edge)
                if old_peer:
                    mirrored_old = mirror_peer_to_db(old_peer, change_reason="api_reissue_remove_old", ingress_edge=old_edge, effective_edge=old_edge)
                    if not mirrored_old.get("ok") and not mirror_error:
                        mirror_error = mirrored_old.get("error") or "db_write_mirror_failed"
            ev = mirror_event_to_db(
                "api_reissue",
                peer_id=old_id,
                reason="api_reissue",
                metadata={"new_peer_id": created.get("id"), "remove_old": bool(remove_old), "gateway": gateway, "uplink": preferred_uplink},
                actor_id=self._client_ip(),
                edge_id=gateway,
            )
            if not ev.get("ok") and not mirror_error:
                mirror_error = ev.get("error") or "db_event_mirror_failed"
            audit(
                "api_reissue",
                ip=self._client_ip(),
                ok=True,
                peer_id=old_id,
                new_peer_id=created.get("id"),
                remove_old=bool(remove_old),
                gateway=gateway,
                uplink=preferred_uplink,
            )

            created.pop("_peer", None)
            return self._send(
                200,
                {
                    "ok": True,
                    "old_id": old_id,
                    "new_peer": created,
                    "old_removed": removed,
                    **({"mirror_warning": mirror_error} if mirror_error else {}),
                },
            )

        m = re.match(rf"^/{API_VERSION}/peers/([A-Za-z0-9_-]+)/uplink$", p)
        if m:
            pid = m.group(1)
            body = self._read_json()
            if body is None:
                return self._send(400, {"ok": False, "error": "invalid json"})
            uplink = str(body.get("uplink") or "").strip().lower()
            if uplink not in ("ams", "nyc", "fra"):
                return self._send(400, {"ok": False, "error": "invalid uplink"})
            item = None
            peer_row = db_get_peer(pid)
            if peer_row.get("ok") and isinstance(peer_row.get("value"), dict):
                item = dict(peer_row.get("value") or {})
            if not item:
                lst = run_cli(["list"])
                if not lst.get("ok"):
                    return self._send(500, lst)
                item = legacy_find_peer(pid, lst)
            if not item:
                audit("api_set_uplink", ip=self._client_ip(), ok=False, peer_id=pid, uplink=uplink, reason="peer_not_found")
                return self._send(404, {"ok": False, "error": "peer not found"})
            ip_short = _peer_ip_short(item.get("allowed_ip"))
            write_edge = get_peer_write_edge(pid)
            if write_edge == "vrn":
                current = db_get_peer_routing(pid)
                current_mode = "manual"
                if current.get("ok") and isinstance(current.get("value"), dict):
                    current_mode = str(current.get("value", {}).get("policy_mode") or "auto").strip().lower() or "auto"
                if uplink == "ams":
                    current_mode = "auto"
                elif uplink == "nyc":
                    current_mode = "manual"
                out = set_peer_preferred_uplink(pid, ip_short, uplink, current_mode)
            else:
                out = set_peer_uplink(ip_short, uplink)
            audit("api_set_uplink", ip=self._client_ip(), ok=bool(out.get("ok")), peer_id=pid, uplink=uplink, allowed_ip=ip_short, error=out.get("error"))
            if not out.get("ok"):
                return self._send(400 if out.get("error") == "vrn_gateway_unsupported_uplink" else 500, out)
            payload = {"ok": True, "peer_id": pid, "allowed_ip": str(item.get("allowed_ip") or ""), "uplink": uplink}
            peer = get_peer_item_for_edge(pid, write_edge)
            mirror_error = None
            if peer:
                mirrored = mirror_peer_to_db(peer, preferred_uplink=uplink, policy_mode=("manual" if uplink == "nyc" else "auto"), change_reason="api_set_uplink", ingress_edge=write_edge, effective_edge=out.get("effective_edge") or write_edge)
                if not mirrored.get("ok"):
                    mirror_error = mirrored.get("error") or "db_write_mirror_failed"
            ev = mirror_event_to_db("api_set_uplink", peer_id=pid, uplink=uplink, reason="api_set_uplink", metadata={"allowed_ip": ip_short}, actor_id=self._client_ip(), edge_id=write_edge)
            if not ev.get("ok") and not mirror_error:
                mirror_error = ev.get("error") or "db_event_mirror_failed"
            if mirror_error:
                payload["mirror_warning"] = mirror_error
            return self._send(200, payload)

        m = re.match(rf"^/{API_VERSION}/peers/([A-Za-z0-9_-]+)/routing$", p)
        if m:
            pid = m.group(1)
            body = self._read_json()
            if body is None:
                return self._send(400, {"ok": False, "error": "invalid json"})
            policy_mode = str(body.get("policy_mode") or "").strip().lower()
            preferred_uplink = str(body.get("preferred_uplink") or "").strip().lower()
            ingress_edge = str(body.get("ingress_edge") or "").strip().lower()
            if ingress_edge and not policy_mode and not preferred_uplink:
                current = db_get_peer_routing(pid)
                if not (current.get("ok") and isinstance(current.get("value"), dict)):
                    audit("api_set_ingress_edge", ip=self._client_ip(), ok=False, peer_id=pid, reason="peer_not_found")
                    return self._send(404, {"ok": False, "error": "peer not found"})
                out = set_peer_ingress_edge_via_routing(pid, ingress_edge)
                audit(
                    "api_set_ingress_edge",
                    ip=self._client_ip(),
                    ok=bool(out.get("ok")),
                    peer_id=pid,
                    ingress_edge=ingress_edge,
                    error=out.get("error"),
                )
                if not out.get("ok"):
                    return self._send(400 if str(out.get("error") or "").startswith("invalid_") else 500, out)
                payload = dict(out)
                ev = mirror_event_to_db(
                    "api_set_ingress_edge",
                    peer_id=pid,
                    reason="api_set_ingress_edge",
                    metadata={"ingress_edge": out.get("ingress_edge"), "effective_edge": out.get("effective_edge"), "intent_only": True},
                    actor_id=self._client_ip(),
                    edge_id=get_peer_write_edge(pid),
                )
                if not ev.get("ok"):
                    payload["mirror_warning"] = ev.get("error") or "db_event_mirror_failed"
                return self._send(200, payload)
            item = None
            peer_row = db_get_peer(pid)
            if peer_row.get("ok") and isinstance(peer_row.get("value"), dict):
                item = dict(peer_row.get("value") or {})
            if not item:
                lst = run_cli(["list"])
                if not lst.get("ok"):
                    return self._send(500, lst)
                item = legacy_find_peer(pid, lst)
            if not item:
                audit("api_set_routing_policy", ip=self._client_ip(), ok=False, peer_id=pid, reason="peer_not_found")
                return self._send(404, {"ok": False, "error": "peer not found"})
            ip_short = _peer_ip_short(item.get("allowed_ip"))
            out = set_peer_routing_policy(pid, ip_short, policy_mode, preferred_uplink=preferred_uplink)
            audit(
                "api_set_routing_policy",
                ip=self._client_ip(),
                ok=bool(out.get("ok")),
                peer_id=pid,
                policy_mode=policy_mode,
                preferred_uplink=preferred_uplink or "",
                allowed_ip=ip_short,
                error=out.get("error"),
            )
            if not out.get("ok"):
                return self._send(400 if str(out.get("error") or "").startswith("invalid_") or out.get("error") in ("auto_mode_requires_ams_preferred", "vrn_gateway_unsupported_uplink") else 500, out)
            payload = dict(out)
            write_edge = get_peer_write_edge(pid)
            peer = get_peer_item_for_edge(pid, write_edge)
            mirror_error = None
            if peer:
                mirrored = mirror_peer_to_db(
                    peer,
                    preferred_uplink=out.get("preferred_uplink"),
                    policy_mode=out.get("policy_mode"),
                    change_reason="api_set_routing_policy",
                    ingress_edge=write_edge,
                    effective_edge=out.get("effective_edge") or write_edge,
                )
                if not mirrored.get("ok"):
                    mirror_error = mirrored.get("error") or "db_write_mirror_failed"
            ev = mirror_event_to_db(
                "api_set_routing_policy",
                peer_id=pid,
                uplink=out.get("preferred_uplink"),
                reason="api_set_routing_policy",
                metadata={"policy_mode": out.get("policy_mode"), "effective_apply": out.get("effective_apply")},
                actor_id=self._client_ip(),
                edge_id=write_edge,
            )
            if not ev.get("ok") and not mirror_error:
                mirror_error = ev.get("error") or "db_event_mirror_failed"
            if mirror_error:
                payload["mirror_warning"] = mirror_error
            return self._send(200, payload)

        m = re.match(rf"^/{API_VERSION}/peers/([A-Za-z0-9_-]+)/preferred-uplink$", p)
        if m:
            pid = m.group(1)
            body = self._read_json()
            if body is None:
                return self._send(400, {"ok": False, "error": "invalid json"})
            preferred_uplink = str(body.get("preferred_uplink") or "").strip().lower()
            current_policy_mode = str(body.get("current_policy_mode") or "").strip().lower()
            item = None
            peer_row = db_get_peer(pid)
            if peer_row.get("ok") and isinstance(peer_row.get("value"), dict):
                item = dict(peer_row.get("value") or {})
            if not item:
                lst = run_cli(["list"])
                if not lst.get("ok"):
                    return self._send(500, lst)
                item = legacy_find_peer(pid, lst)
            if not item:
                audit("api_set_preferred_uplink", ip=self._client_ip(), ok=False, peer_id=pid, reason="peer_not_found")
                return self._send(404, {"ok": False, "error": "peer not found"})
            ip_short = _peer_ip_short(item.get("allowed_ip"))
            out = set_peer_preferred_uplink(pid, ip_short, preferred_uplink, current_policy_mode)
            audit(
                "api_set_preferred_uplink",
                ip=self._client_ip(),
                ok=bool(out.get("ok")),
                peer_id=pid,
                preferred_uplink=preferred_uplink,
                current_policy_mode=current_policy_mode,
                allowed_ip=ip_short,
                error=out.get("error"),
            )
            if not out.get("ok"):
                return self._send(400 if str(out.get("error") or "").startswith("invalid_") or out.get("error") in ("auto_mode_requires_ams_preferred", "vrn_gateway_unsupported_uplink") else 500, out)
            payload = dict(out)
            write_edge = get_peer_write_edge(pid)
            peer = get_peer_item_for_edge(pid, write_edge)
            mirror_error = None
            if peer:
                mirrored = mirror_peer_to_db(
                    peer,
                    preferred_uplink=out.get("preferred_uplink"),
                    policy_mode=out.get("policy_mode"),
                    change_reason="api_set_preferred_uplink",
                    ingress_edge=write_edge,
                    effective_edge=out.get("effective_edge") or write_edge,
                )
                if not mirrored.get("ok"):
                    mirror_error = mirrored.get("error") or "db_write_mirror_failed"
            ev = mirror_event_to_db(
                "api_set_preferred_uplink",
                peer_id=pid,
                uplink=out.get("preferred_uplink"),
                reason="api_set_preferred_uplink",
                metadata={"policy_mode": out.get("policy_mode"), "effective_apply": out.get("effective_apply")},
                actor_id=self._client_ip(),
                edge_id=write_edge,
            )
            if not ev.get("ok") and not mirror_error:
                mirror_error = ev.get("error") or "db_event_mirror_failed"
            if mirror_error:
                payload["mirror_warning"] = mirror_error
            return self._send(200, payload)

        m = re.match(rf"^/{API_VERSION}/peers/([A-Za-z0-9_-]+)/ingress-edge$", p)
        if m:
            pid = m.group(1)
            body = self._read_json()
            if body is None:
                return self._send(400, {"ok": False, "error": "invalid json"})
            ingress_edge = str(body.get("ingress_edge") or "").strip().lower()
            lst = run_cli(["list"])
            if not lst.get("ok"):
                return self._send(500, lst)
            item = None
            for x in lst.get("items", []):
                if str(x.get("id") or "") == pid:
                    item = x
                    break
            if not item:
                audit("api_set_ingress_edge", ip=self._client_ip(), ok=False, peer_id=pid, reason="peer_not_found")
                return self._send(404, {"ok": False, "error": "peer not found"})
            out = set_peer_ingress_edge(pid, ingress_edge)
            audit(
                "api_set_ingress_edge",
                ip=self._client_ip(),
                ok=bool(out.get("ok")),
                peer_id=pid,
                ingress_edge=ingress_edge,
                error=out.get("error"),
            )
            if not out.get("ok"):
                return self._send(400 if str(out.get("error") or "").startswith("invalid_") else 500, out)
            payload = dict(out)
            ev = mirror_event_to_db(
                "api_set_ingress_edge",
                peer_id=pid,
                reason="api_set_ingress_edge",
                metadata={"ingress_edge": out.get("ingress_edge"), "effective_edge": out.get("effective_edge"), "intent_only": True},
                actor_id=self._client_ip(),
            )
            if not ev.get("ok"):
                payload["mirror_warning"] = ev.get("error") or "db_event_mirror_failed"
            return self._send(200, payload)

        return self._send(404, {"ok": False, "error": "not found"})

    def do_HEAD(self):
        return self.do_GET()


if __name__ == "__main__":
    if not API_TOKEN:
        raise SystemExit("WG_CONTROL_API_TOKEN is empty")
    srv = ThreadingHTTPServer((HOST, PORT), H)
    print(f"wg-control-api on {HOST}:{PORT}", flush=True)
    srv.serve_forever()
