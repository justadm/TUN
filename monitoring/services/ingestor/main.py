from __future__ import annotations

import json
import os
import re
import threading
import time
from datetime import datetime, timedelta, timezone
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlencode
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

from services.common.config import load_ingestor_config, load_node_sources
from services.common.helper_client import (
    HelperClientError,
    fetch_helper_links,
    fetch_helper_profile_current,
    fetch_helper_schema,
    fetch_helper_status,
    post_helper_link_action,
)
from services.common.db import DBError, exec_sql, fetch_json_value, load_db_config, sql_str


class State:
    def __init__(self) -> None:
        self.last_run_at: str = ""
        self.last_error: str = ""
        self.nodes_seen: int = 0
        self.links_seen: int = 0
        self.commands_dispatched: int = 0
        self.commands_failed: int = 0


STATE = State()
_SCHEMA_READY = False
_SCHEMA_LOCK = threading.Lock()


def _utc_now_iso() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _parse_ts(raw: str) -> datetime | None:
    txt = str(raw or "").strip()
    if not txt:
        return None
    if txt.endswith("Z"):
        txt = txt[:-1] + "+00:00"
    try:
        dt = datetime.fromisoformat(txt)
    except ValueError:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.astimezone(timezone.utc)


def _env(name: str, default: str = "") -> str:
    return str(os.getenv(name, default)).strip()


def _env_bool(name: str, default: bool = False) -> bool:
    raw = _env(name, "true" if default else "false").lower()
    return raw in {"1", "true", "yes", "on"}


def _request_json(url: str, *, timeout_sec: float, headers: dict[str, str] | None = None) -> dict:
    req_headers = {"Accept": "application/json"}
    if headers:
        req_headers.update(headers)
    req = Request(url, method="GET", headers=req_headers)
    try:
        with urlopen(req, timeout=timeout_sec) as resp:
            raw = resp.read().decode("utf-8", "replace")
    except HTTPError as exc:
        body = exc.read().decode("utf-8", "replace")
        raise HelperClientError(f"discovery http {exc.code}: {body.strip()}") from exc
    except URLError as exc:
        raise HelperClientError(f"discovery url error: {exc}") from exc
    try:
        out = json.loads(raw or "{}")
    except json.JSONDecodeError as exc:
        raise HelperClientError(f"discovery invalid json: {exc}") from exc
    if not isinstance(out, dict):
        raise HelperClientError("discovery response is not an object")
    return out


def _helper_auth_headers(auth_ref: str) -> dict[str, str]:
    ref = str(auth_ref or "").strip()
    if not ref or not ref.startswith("env:"):
        return {}
    env_name = ref.split(":", 1)[1].strip()
    token = _env(env_name, "")
    if not token:
        return {}
    return {
        "Authorization": f"Bearer {token}",
        "X-Helper-Token": token,
    }


def _parse_schema_version_to_int(raw: str) -> int:
    txt = str(raw or "").strip()
    if not txt:
        return 0
    digits = "".join(ch for ch in txt if ch.isdigit())
    if len(digits) < 8:
        return 0
    try:
        return int(digits[:8])
    except ValueError:
        return 0


def _validate_helper_schema(schema: dict) -> None:
    api_version = str(schema.get("apiVersion", "")).strip()
    if api_version != "v1":
        raise HelperClientError(f"unsupported helper apiVersion: {api_version or 'empty'}")
    bootstrap = schema.get("bootstrap")
    if not isinstance(bootstrap, dict):
        raise HelperClientError("helper schema missing bootstrap contract")
    schema_version = _parse_schema_version_to_int(str(bootstrap.get("schemaVersion", "")).strip())
    min_required = _parse_schema_version_to_int(_env("MONITORING_HELPER_MIN_SCHEMA_VERSION", "2026-04-14"))
    if schema_version < min_required:
        raise HelperClientError(
            f"helper bootstrap.schemaVersion too old: {bootstrap.get('schemaVersion', '')} < { _env('MONITORING_HELPER_MIN_SCHEMA_VERSION', '2026-04-14') }"
        )


def _read_env_token_file(path: str) -> str:
    raw_path = str(path or "").strip()
    if not raw_path:
        return ""
    try:
        with open(raw_path, "r", encoding="utf-8") as fh:
            for line in fh:
                txt = line.strip()
                if not txt or txt.startswith("#") or "=" not in txt:
                    continue
                key, value = txt.split("=", 1)
                if key.strip() == "WG_CONTROL_API_TOKEN":
                    return value.strip()
    except OSError:
        return ""
    return ""


def _discovery_headers() -> dict[str, str]:
    token = _env("MONITORING_DISCOVERY_API_TOKEN", _env("WG_CONTROL_API_TOKEN", ""))
    if not token:
        token = _read_env_token_file(_env("MONITORING_DISCOVERY_API_TOKEN_FILE", ""))
    if not token:
        return {}
    token_header = _env("MONITORING_DISCOVERY_TOKEN_HEADER", "X-API-Token")
    return {token_header: token}


def _csv_list(raw: str) -> list[str]:
    items: list[str] = []
    for part in str(raw or "").split(","):
        txt = part.strip()
        if txt:
            items.append(txt)
    return items


def _token_header_name() -> str:
    return _env("MONITORING_DISCOVERY_TOKEN_HEADER", "X-API-Token") or "X-API-Token"


def _header_for_token(token: str) -> dict[str, str]:
    txt = str(token or "").strip()
    if not txt:
        return {}
    return {_token_header_name(): txt}


def _derive_node_id(edge_id: str) -> str:
    template = _env("MONITORING_DISCOVERY_NODE_ID_TEMPLATE", "{edge_id}-1")
    token = str(edge_id or "").strip()
    if not token:
        return ""
    if "{edge_id}" not in template:
        return token
    return template.replace("{edge_id}", token)


def _derive_host(edge_id: str) -> str:
    template = _env("MONITORING_DISCOVERY_HOST_TEMPLATE", "")
    token = str(edge_id or "").strip()
    if not token or not template:
        return ""
    if "{edge_id}" not in template:
        return template
    return template.replace("{edge_id}", token)


def _discovery_endpoint_urls() -> list[str]:
    urls: list[str] = []
    csv_urls = _env("MONITORING_DISCOVERY_URLS", "")
    if csv_urls:
        for raw in csv_urls.split(","):
            url = raw.strip()
            if url:
                urls.append(url)
    edges_url = _env("MONITORING_DISCOVERY_EDGES_URL", "")
    uplinks_url = _env("MONITORING_DISCOVERY_UPLINKS_URL", "")
    if not edges_url and not csv_urls:
        edges_url = "http://host.docker.internal:18110/v1/edges"
    if not uplinks_url and not csv_urls:
        uplinks_url = "http://host.docker.internal:18110/v1/uplinks"
    if edges_url:
        urls.append(edges_url)
        if not uplinks_url and "/edges" in edges_url:
            uplinks_url = re.sub(r"/edges(?:\?.*)?$", "/uplinks", edges_url)
    if uplinks_url:
        urls.append(uplinks_url)
    uniq: list[str] = []
    seen = set()
    for url in urls:
        if url in seen:
            continue
        seen.add(url)
        uniq.append(url)
    return uniq


def _discovery_endpoint_specs() -> list[tuple[str, dict[str, str]]]:
    urls = _discovery_endpoint_urls()
    if not urls:
        return []
    explicit_tokens = _csv_list(_env("MONITORING_DISCOVERY_URL_TOKENS", ""))
    default_headers = _discovery_headers()
    specs: list[tuple[str, dict[str, str]]] = []
    for idx, url in enumerate(urls):
        headers = default_headers
        if idx < len(explicit_tokens):
            headers = _header_for_token(explicit_tokens[idx])
        specs.append((url, headers))
    return specs


def _control_peers_specs() -> list[tuple[str, dict[str, str]]]:
    enabled = _env_bool("MONITORING_CONTROL_PEERS_INGEST_ENABLED", True)
    if not enabled:
        return []
    urls = _csv_list(_env("MONITORING_CONTROL_PEERS_URLS", ""))
    if not urls:
        single_url = _env("MONITORING_CONTROL_PEERS_URL", "http://host.docker.internal:18110/v1/peers")
        if single_url:
            urls = [single_url]
    if not urls:
        return []
    explicit_tokens = _csv_list(_env("MONITORING_CONTROL_PEERS_TOKENS", ""))
    default_headers = _discovery_headers()
    specs: list[tuple[str, dict[str, str]]] = []
    for idx, url in enumerate(urls):
        headers = default_headers
        if idx < len(explicit_tokens):
            headers = _header_for_token(explicit_tokens[idx])
        specs.append((url, headers))
    return specs


def _is_uplink_record(item: dict) -> bool:
    return any(
        key in item
        for key in (
            "uplink_id",
            "preferred_uplink",
            "effective_uplink",
            "rtt_ms",
            "loss_pct",
            "hs_age_sec",
            "active_peers",
        )
    )


def _discover_display_name(item: dict, edge_id: str) -> str:
    # For uplink rows, `name` is usually uplink label (AMS/FRA/NYC), not edge/node label.
    preferred = (
        str(item.get("display_name", "")).strip()
        or str(item.get("edge_name", "")).strip()
        or str(item.get("edge_display_name", "")).strip()
        or str(item.get("site_name", "")).strip()
        or str(item.get("edge", "")).strip()
    )
    if preferred:
        return preferred
    if not _is_uplink_record(item):
        plain_name = str(item.get("name", "")).strip()
        if plain_name:
            return plain_name
    return edge_id


def _normalize_discovery_nodes(payload: dict) -> list[dict]:
    items = payload.get("items")
    if not isinstance(items, list):
        return []
    helper_port = _env("MONITORING_DISCOVERY_HELPER_PORT", "19090")
    helper_scheme = _env("MONITORING_DISCOVERY_HELPER_SCHEME", "http")
    helper_path = _env("MONITORING_DISCOVERY_HELPER_PATH", "").strip().lstrip("/")
    helper_auth_ref = _env("MONITORING_DISCOVERY_HELPER_AUTH_REF", "")
    only_active = _env_bool("MONITORING_DISCOVERY_ONLY_ACTIVE", True)
    out: list[dict] = []
    per_edge: dict[str, dict] = {}
    for item in items:
        if not isinstance(item, dict):
            continue
        edge_id = str(item.get("edge_id", "")).strip() or str(item.get("id", "")).strip()
        if not edge_id:
            edge_id = str(item.get("edge", "")).strip()
        if not edge_id:
            uplink_id = str(item.get("uplink_id", "")).strip()
            if uplink_id and "/" in uplink_id:
                edge_id = uplink_id.split("/", 1)[0].strip()
        if not edge_id:
            continue
        state = str(item.get("state", "")).strip().lower()
        if only_active and state not in {"", "active", "ready"}:
            continue
        host = (
            str(item.get("public_host", "")).strip()
            or str(item.get("host", "")).strip()
            or str(item.get("edge_public_host", "")).strip()
            or _derive_host(edge_id)
        )
        helper_base_url = str(item.get("helper_base_url", "")).strip()
        if not helper_base_url and host:
            helper_base_url = f"{helper_scheme}://{host}:{helper_port}"
            if helper_path:
                helper_base_url = f"{helper_base_url}/{helper_path}"
        node_id = str(item.get("node_id", "")).strip() or _derive_node_id(edge_id)
        if not node_id:
            continue
        node = {
            "node_id": node_id,
            "display_name": _discover_display_name(item, edge_id),
            "role": str(item.get("role", "")).strip() or "runtime",
            "region": str(item.get("region", "")).strip() or edge_id.split("-", 1)[0],
            "edge_id": edge_id,
            "host": host,
            "helper_base_url": helper_base_url,
            "helper_auth_ref": helper_auth_ref,
        }
        # Uplinks payload can include many rows per edge: keep one merged node per node_id.
        prev = per_edge.get(node_id)
        if not prev:
            per_edge[node_id] = node
            continue
        for key in ("display_name", "role", "region", "edge_id", "host", "helper_base_url", "helper_auth_ref"):
            value = str(node.get(key, "")).strip()
            if value:
                prev[key] = value
    out.extend(per_edge.values())
    return out


def _load_discovery_nodes(timeout_sec: int) -> tuple[list[dict], str]:
    specs = _discovery_endpoint_specs()
    if not specs:
        return [], ""
    all_nodes: dict[str, dict] = {}
    errors: list[str] = []
    successes = 0
    for url, headers in specs:
        try:
            payload = _request_json(url, timeout_sec=float(max(timeout_sec, 3)), headers=headers)
            successes += 1
            nodes = _normalize_discovery_nodes(payload)
            for node in nodes:
                node_id = str(node.get("node_id", "")).strip()
                if not node_id:
                    continue
                prev = all_nodes.get(node_id) or {}
                merged = dict(prev)
                for key in ("display_name", "role", "region", "edge_id", "host", "helper_base_url", "helper_auth_ref"):
                    value = str(node.get(key, "")).strip()
                    if value:
                        merged[key] = value
                merged["node_id"] = node_id
                all_nodes[node_id] = merged
        except HelperClientError as exc:
            errors.append(f"{url}: {exc}")
            continue
    error_txt = ""
    if successes == 0:
        error_txt = "; ".join(errors)
    return [all_nodes[k] for k in sorted(all_nodes.keys())], error_txt


def _merge_nodes(static_nodes: list[dict], discovered_nodes: list[dict]) -> list[dict]:
    merged: dict[str, dict] = {}
    for src in static_nodes:
        if not isinstance(src, dict):
            continue
        node_id = str(src.get("node_id", "")).strip()
        if not node_id:
            continue
        merged[node_id] = dict(src)
    for src in discovered_nodes:
        if not isinstance(src, dict):
            continue
        node_id = str(src.get("node_id", "")).strip()
        if not node_id:
            continue
        current = merged.get(node_id, {})
        nxt = dict(current)
        for key in ("display_name", "role", "region", "edge_id", "host", "helper_base_url", "helper_auth_ref"):
            val = str(src.get(key, "")).strip()
            if val:
                nxt[key] = val
        nxt["node_id"] = node_id
        merged[node_id] = nxt
    return [merged[k] for k in sorted(merged.keys())]


def _normalize_control_peer_links(payload: dict) -> list[tuple[str, dict]]:
    items = payload.get("items")
    if not isinstance(items, list):
        return []
    out: list[tuple[str, dict]] = []
    stale_starting_after_sec = max(int(_env("MONITORING_CONTROL_PEER_STARTING_MAX_AGE_SEC", "14400") or 14400), 60)
    for item in items:
        if not isinstance(item, dict):
            continue
        peer_id = str(item.get("peer_id", "")).strip() or str(item.get("id", "")).strip()
        if not peer_id:
            continue
        edge_id = str(item.get("effective_edge", "")).strip() or str(item.get("ingress_edge", "")).strip() or "edg"
        node_id = _derive_node_id(edge_id) or edge_id
        status = str(item.get("status", "")).strip().lower()
        health_raw = str(item.get("health_status", "")).strip().lower()
        observed_state = "unknown"
        if status == "active":
            observed_state = "established"
        elif status in {"pending", "queued", "waiting"}:
            observed_state = "starting"
        elif status in {"draining"}:
            observed_state = "draining"
        elif status in {"stopped", "disabled", "removed"}:
            observed_state = "stopped"
        elif status in {"failed", "error"}:
            observed_state = "failed"
        health = "unknown"
        if health_raw in {"healthy", "degraded", "failed", "draining", "stale", "down"}:
            health = health_raw
        elif health_raw in {"pending", "starting", "queued", "waiting", "unknown"}:
            health = "degraded"
        elif status in {"pending", "queued", "waiting", "starting"}:
            health = "degraded"
        elif status in {"active"}:
            health = "healthy"
        elif status in {"failed", "error"}:
            health = "failed"
        desired_state = "up"
        if status in {"disabled", "down", "removed"}:
            desired_state = "down"
        gateway_id = str(item.get("effective_uplink", "")).strip() or str(item.get("preferred_uplink", "")).strip()
        last_handshake_at = str(item.get("last_handshake_at", "")).strip()
        last_transition_at = (
            str(item.get("updated_at", "")).strip()
            or str(item.get("observed_at", "")).strip()
            or str(item.get("created_at", "")).strip()
            or _utc_now_iso()
        )
        event_dt = _parse_ts(last_transition_at)
        age_sec = 0.0
        if event_dt is not None:
            age_sec = max((datetime.now(timezone.utc) - event_dt).total_seconds(), 0.0)
        is_stuck_starting = status in {"pending", "queued", "waiting", "starting"} and age_sec >= float(stale_starting_after_sec)

        error_class = "none"
        if health == "failed":
            error_class = "control_peer_unhealthy"
        elif health == "degraded" and status in {"pending", "queued", "waiting", "starting"}:
            error_class = "control_peer_starting"
        if is_stuck_starting:
            observed_state = "stopped"
            desired_state = "down"
            health = "stale"
            error_class = "control_peer_stale"
        fake_link = {
            "linkID": f"peer:{peer_id}",
            "deviceID": peer_id,
            "role": "client",
            "desiredState": desired_state,
            "observedState": observed_state,
            "health": health,
            "running": status == "active",
            "sessionID": f"peer-session:{peer_id}",
            "leaseOwner": "",
            "leaseID": "",
            "lastError": (
                "stuck_in_starting_queue"
                if is_stuck_starting
                else str(item.get("health_note", "")).strip()
            ),
            "errorClass": error_class,
            "lastEventAt": last_transition_at,
            "lastTransitionAt": last_transition_at,
            "lastHandshakeAt": last_handshake_at,
            "lastRxAt": "",
            "lastTxAt": "",
            "rxBytes": 0,
            "txBytes": 0,
            "gatewayID": gateway_id,
            "gatewayAddr": "",
            "tunName": f"peer-{peer_id[:8]}",
            "source": "control-api",
            "monitor_source": "control_peer",
            "peer": item,
        }
        out.append((node_id, fake_link))
    return out


def _load_control_peer_links(timeout_sec: int) -> tuple[list[tuple[str, dict]], str]:
    specs = _control_peers_specs()
    if not specs:
        return [], ""
    merged: dict[str, tuple[str, dict]] = {}
    errors: list[str] = []
    successes = 0
    for url, headers in specs:
        try:
            payload = _request_json(url, timeout_sec=float(max(timeout_sec, 3)), headers=headers)
            successes += 1
            for node_id, item in _normalize_control_peer_links(payload):
                link_id = str(item.get("linkID", "")).strip()
                if not link_id:
                    continue
                merged[link_id] = (node_id, item)
        except HelperClientError as exc:
            errors.append(f"{url}: {exc}")
            continue
    error_txt = ""
    if successes == 0:
        error_txt = "; ".join(errors)
    links = [merged[key] for key in sorted(merged.keys())]
    return links, error_txt


def _upsert_node_sql(node: dict) -> str:
    node_id = str(node.get("node_id", "")).strip()
    if not node_id:
        return ""
    display_name = str(node.get("display_name", "")).strip()
    role = str(node.get("role", "runtime")).strip() or "runtime"
    region = str(node.get("region", "")).strip()
    edge_id = str(node.get("edge_id", "")).strip()
    host = str(node.get("host", "")).strip()
    helper_base_url = str(node.get("helper_base_url", "")).strip()
    helper_auth_ref = str(node.get("helper_auth_ref", "")).strip()
    return f"""
        insert into monitor_nodes (
            node_id,
            display_name,
            role,
            region,
            edge_id,
            host,
            helper_base_url,
            helper_auth_ref,
            status,
            last_seen_at,
            updated_at
        ) values (
            {sql_str(node_id)},
            {sql_str(display_name)},
            {sql_str(role)},
            {sql_str(region)},
            {sql_str(edge_id)},
            {sql_str(host)},
            {sql_str(helper_base_url)},
            {sql_str(helper_auth_ref)},
            'active',
            now(),
            now()
        )
        on conflict (node_id) do update set
            display_name = excluded.display_name,
            role = excluded.role,
            region = excluded.region,
            edge_id = excluded.edge_id,
            host = excluded.host,
            helper_base_url = excluded.helper_base_url,
            helper_auth_ref = excluded.helper_auth_ref,
            status = 'active',
            last_seen_at = now(),
            updated_at = now()
    """


def _set_node_status_sql(node_id: str, status: str) -> str:
    return f"""
        update monitor_nodes
        set
            status = {sql_str(status)},
            last_seen_at = now(),
            updated_at = now()
        where node_id = {sql_str(node_id)}
    """


def _disable_missing_nodes_sql(active_node_ids: list[str]) -> str:
    ids = [str(node_id or "").strip() for node_id in active_node_ids if str(node_id or "").strip()]
    if ids:
        in_list = ", ".join(sql_str(node_id) for node_id in sorted(set(ids)))
        where = f"node_id not in ({in_list})"
    else:
        where = "true"
    return f"""
        update monitor_nodes
        set
            status = 'disabled',
            updated_at = now()
        where {where}
          and status <> 'disabled'
    """


def _stale_disabled_nodes_links_sql() -> str:
    return """
        update monitor_link_snapshots s
        set
            health_status = 'stale',
            last_error = case
                when nullif(s.last_error, '') is null then 'node_not_discovered'
                else s.last_error
            end,
            stale_after = coalesce(s.stale_after, now() - interval '1 second'),
            updated_at = now()
        from monitor_links l
        join monitor_nodes n on n.node_id = l.node_id
        where s.link_id = l.link_id
          and n.status = 'disabled'
          and (
            s.health_status <> 'stale'
            or s.stale_after is null
            or s.stale_after > now()
          )
    """


def _upsert_link_sql(node_id: str, item: dict) -> str:
    link_id = str(item.get("linkID", "")).strip()
    if not link_id:
        return ""
    role = str(item.get("role", "unknown")).strip() or "unknown"
    gateway_id = str(item.get("gatewayID", "")).strip()
    gateway_addr = str(item.get("gatewayAddr", "")).strip()
    tun_name = str(item.get("tunName", "")).strip()
    desired_state = str(item.get("desiredState", "unknown")).strip() or "unknown"
    return f"""
        insert into monitor_links (
            link_id,
            node_id,
            gateway_id,
            role,
            transport_type,
            transport_addr,
            tun_name,
            desired_state,
            metadata,
            updated_at
        ) values (
            {sql_str(link_id)},
            {sql_str(node_id)},
            {sql_str(gateway_id)},
            {sql_str(role)},
            'helper',
            {sql_str(gateway_addr)},
            {sql_str(tun_name)},
            {sql_str(desired_state)},
            {sql_str(json.dumps(item, ensure_ascii=False, sort_keys=True))}::jsonb,
            now()
        )
        on conflict (link_id) do update set
            node_id = excluded.node_id,
            gateway_id = excluded.gateway_id,
            role = excluded.role,
            transport_addr = excluded.transport_addr,
            tun_name = excluded.tun_name,
            desired_state = excluded.desired_state,
            metadata = excluded.metadata,
            updated_at = now()
    """


def _upsert_snapshot_sql(link_id: str, item: dict, *, snapshot_source: str = "poll") -> str:
    observed_state = str(item.get("observedState", "unknown")).strip() or "unknown"
    health_status = str(item.get("health", "unknown")).strip().lower() or "unknown"
    session_id = str(item.get("sessionID", "")).strip()
    error_class = str(item.get("errorClass", "none")).strip() or "none"
    last_error = str(item.get("lastError", "")).strip()
    gateway_id_selected = str(item.get("gatewayID", "")).strip()
    gateway_addr_selected = str(item.get("gatewayAddr", "")).strip()
    reconnects = int(item.get("snapshot", {}).get("Reconnects", 0) or item.get("snapshot", {}).get("reconnects", 0) or 0)
    handshake_failures = int(item.get("snapshot", {}).get("HandshakeFailures", 0) or item.get("snapshot", {}).get("handshakeFailures", 0) or 0)
    rx_bytes = int(item.get("rxBytes", 0) or 0)
    tx_bytes = int(item.get("txBytes", 0) or 0)
    last_transition_at = _sql_ts(item.get("lastTransitionAt"))
    last_handshake_at = _sql_ts(item.get("lastHandshakeAt"))
    last_rx_at = _sql_ts(item.get("lastRxAt"))
    last_tx_at = _sql_ts(item.get("lastTxAt"))
    stale_after = _sql_ts(item.get("staleAfter"))
    snapshot_json = sql_str(json.dumps(item, ensure_ascii=False, sort_keys=True))
    return f"""
        insert into monitor_link_snapshots (
            link_id,
            observed_state,
            health_status,
            session_id,
            error_class,
            last_error,
            last_transition_at,
            last_handshake_at,
            last_rx_at,
            last_tx_at,
            rx_bytes,
            tx_bytes,
            reconnects,
            handshake_failures,
            gateway_id_selected,
            gateway_addr_selected,
            stale_after,
            snapshot_source,
            snapshot_version,
            observed_at,
            snapshot_json,
            updated_at
        ) values (
            {sql_str(link_id)},
            {sql_str(observed_state)},
            {sql_str(health_status)},
            {sql_str(session_id)},
            {sql_str(error_class)},
            {sql_str(last_error)},
            {last_transition_at},
            {last_handshake_at},
            {last_rx_at},
            {last_tx_at},
            {rx_bytes},
            {tx_bytes},
            {reconnects},
            {handshake_failures},
            {sql_str(gateway_id_selected)},
            {sql_str(gateway_addr_selected)},
            {stale_after},
            {sql_str(snapshot_source)},
            'v1',
            now(),
            {snapshot_json}::jsonb,
            now()
        )
        on conflict (link_id) do update set
            observed_state = excluded.observed_state,
            health_status = excluded.health_status,
            session_id = excluded.session_id,
            error_class = excluded.error_class,
            last_error = excluded.last_error,
            last_transition_at = excluded.last_transition_at,
            last_handshake_at = excluded.last_handshake_at,
            last_rx_at = excluded.last_rx_at,
            last_tx_at = excluded.last_tx_at,
            rx_bytes = excluded.rx_bytes,
            tx_bytes = excluded.tx_bytes,
            reconnects = excluded.reconnects,
            handshake_failures = excluded.handshake_failures,
            gateway_id_selected = excluded.gateway_id_selected,
            gateway_addr_selected = excluded.gateway_addr_selected,
            stale_after = excluded.stale_after,
            snapshot_source = excluded.snapshot_source,
            snapshot_version = excluded.snapshot_version,
            observed_at = excluded.observed_at,
            snapshot_json = excluded.snapshot_json,
            updated_at = now()
    """


def _mark_missing_links_stale_sql(node_id: str, present_ids: list[str], *, source: str = "") -> str:
    if present_ids:
        ids = ", ".join(sql_str(link_id) for link_id in present_ids)
        cond = f"and l.link_id not in ({ids})"
    else:
        cond = ""
    src_cond = ""
    src = str(source or "").strip()
    if src:
        src_cond = f"and coalesce(l.metadata->>'monitor_source', '') = {sql_str(src)}"
    return f"""
        update monitor_link_snapshots s
        set
            observed_state = 'orphaned',
            health_status = 'stale',
            stale_after = now(),
            last_error = case when coalesce(last_error, '') = '' then 'link_missing_in_poll' else last_error end,
            observed_at = now(),
            updated_at = now()
        from monitor_links l
        where s.link_id = l.link_id
          and l.node_id = {sql_str(node_id)}
          {src_cond}
          {cond}
    """


def _mark_old_snapshots_stale_sql(max_age_sec: int) -> str:
    ttl = max(int(max_age_sec), 1)
    return f"""
        update monitor_link_snapshots
        set
            observed_state = 'orphaned',
            health_status = 'stale',
            stale_after = now(),
            last_error = case
                when coalesce(last_error, '') = '' then 'stale_by_age'
                else last_error
            end,
            updated_at = now()
        where observed_at < now() - ({ttl} * interval '1 second')
          and health_status <> 'stale'
    """


def _sql_ts(value: object) -> str:
    raw = str(value or "").strip()
    if not raw or raw.lower() in {"none", "null", "nil"}:
        return "NULL"
    return f"{sql_str(raw)}::timestamptz"


def _ensure_runtime_schema(db_cfg) -> None:
    global _SCHEMA_READY
    if _SCHEMA_READY:
        return
    with _SCHEMA_LOCK:
        if _SCHEMA_READY:
            return
        exec_sql(
            db_cfg,
            """
            alter table if exists monitor_link_subjects
                add column if not exists security_profile text not null default '',
                add column if not exists profile_revision text not null default '',
                add column if not exists profile_index_key text not null default '',
                add column if not exists profile_bundle_version text not null default '',
                add column if not exists helper_profile_id text not null default '',
                add column if not exists helper_security_profile text not null default '',
                add column if not exists helper_profile_revision text not null default '',
                add column if not exists helper_profile_source text not null default ''
            """,
        )
        exec_sql(
            db_cfg,
            """
            create index if not exists monitor_link_subjects_profile_revision_idx
                on monitor_link_subjects(connection_profile_id, profile_revision)
            """,
        )
        exec_sql(
            db_cfg,
            """
            create index if not exists monitor_link_subjects_profile_index_key_idx
                on monitor_link_subjects(profile_index_key)
            """,
        )
        exec_sql(
            db_cfg,
            """
            create table if not exists monitor_profile_inventory (
                profile_id text not null default '',
                revision text not null default '',
                security_profile text not null default '',
                region text not null default '',
                ruleset_ref text not null default '',
                dns_mode text not null default '',
                dns_template text not null default '',
                source text not null default 'helper',
                metadata jsonb not null default '{}'::jsonb,
                observed_at timestamptz not null default now(),
                updated_at timestamptz not null default now(),
                primary key (profile_id, revision)
            )
            """,
        )
        exec_sql(
            db_cfg,
            """
            create table if not exists monitor_alert_deliveries (
                incident_id uuid not null references monitor_incidents(incident_id) on delete cascade,
                channel text not null check (channel in ('tg', 'max')),
                status text not null check (status in ('succeeded', 'failed')),
                attempt int not null default 1,
                last_error text not null default '',
                delivered_at timestamptz not null default now(),
                updated_at timestamptz not null default now(),
                primary key (incident_id, channel)
            )
            """,
        )
        exec_sql(
            db_cfg,
            """
            create index if not exists monitor_alert_deliveries_status_idx
                on monitor_alert_deliveries(status, delivered_at desc)
            """,
        )
        exec_sql(
            db_cfg,
            """
            create index if not exists monitor_profile_inventory_profile_idx
                on monitor_profile_inventory(profile_id)
            """,
        )
        exec_sql(
            db_cfg,
            """
            do $$
            begin
              if exists (
                select 1
                from pg_constraint
                where conname = 'monitor_incidents_kind_check'
              ) then
                alter table monitor_incidents drop constraint monitor_incidents_kind_check;
              end if;
            end
            $$;
            """,
        )
        exec_sql(
            db_cfg,
            """
            alter table monitor_incidents
            add constraint monitor_incidents_kind_check check (
                kind in (
                    'link_failed',
                    'link_flapping',
                    'link_stale',
                    'link_degraded',
                    'gateway_degraded',
                    'node_unreachable',
                    'ingestion_gap',
                    'high_risk_violation',
                    'profile_drift',
                    'startup_contract_failure',
                    'link_without_profile',
                    'gateway_flap_risk'
                )
            )
            """,
        )
        _SCHEMA_READY = True


def _first_non_empty(*values: object) -> str:
    for value in values:
        txt = str(value or "").strip()
        if txt:
            return txt
    return ""


def _snapshot_value(snapshot: dict, *keys: str) -> object:
    if not isinstance(snapshot, dict):
        return ""
    for key in keys:
        if key in snapshot:
            return snapshot.get(key)
    return ""


def _snapshot_int(snapshot: dict, *keys: str) -> int:
    raw = _snapshot_value(snapshot, *keys)
    try:
        return int(raw or 0)
    except (TypeError, ValueError):
        return 0


def _as_bool(value: object) -> bool:
    if isinstance(value, bool):
        return value
    txt = str(value or "").strip().lower()
    return txt in {"1", "true", "yes", "on"}


def _severity_rank(severity: str) -> int:
    s = str(severity or "").strip().lower()
    if s == "critical":
        return 3
    if s == "warning":
        return 2
    if s == "info":
        return 1
    return 0


def _pull_profile_doc(payload: dict) -> dict:
    if not isinstance(payload, dict):
        return {}
    prof = payload.get("profile")
    if isinstance(prof, dict):
        return prof
    return payload


def _extract_profile_context(item: dict, status_doc: dict, profile_doc: dict) -> dict:
    profile_id = _first_non_empty(
        item.get("profileID"),
        item.get("profile_id"),
        status_doc.get("profileID"),
        status_doc.get("profile_id"),
        profile_doc.get("profileID"),
        profile_doc.get("profile_id"),
    )
    security_profile = _first_non_empty(
        item.get("securityProfile"),
        item.get("security_profile"),
        status_doc.get("securityProfile"),
        status_doc.get("security_profile"),
        profile_doc.get("securityProfile"),
        profile_doc.get("security_profile"),
    ).lower()
    revision = _first_non_empty(
        item.get("revision"),
        status_doc.get("revision"),
        profile_doc.get("revision"),
    )
    device_id = _first_non_empty(
        item.get("deviceID"),
        item.get("device_id"),
        status_doc.get("deviceID"),
        status_doc.get("device_id"),
        profile_doc.get("deviceID"),
        profile_doc.get("device_id"),
    )
    return {
        "profile_id": profile_id,
        "security_profile": security_profile,
        "revision": revision,
        "device_id": device_id,
        "profile_index_key": f"{profile_id}+{revision}" if profile_id and revision else "",
        "profile_bundle_version": _first_non_empty(
            profile_doc.get("profileBundleVersion"),
            profile_doc.get("profile_bundle_version"),
            status_doc.get("profileBundleVersion"),
            status_doc.get("profile_bundle_version"),
        ),
        "helper_profile_id": _first_non_empty(profile_doc.get("profileID"), profile_doc.get("profile_id")),
        "helper_security_profile": _first_non_empty(profile_doc.get("securityProfile"), profile_doc.get("security_profile")).lower(),
        "helper_profile_revision": _first_non_empty(profile_doc.get("revision")),
        "helper_profile_source": _first_non_empty(profile_doc.get("source"), "helper"),
    }


def _upsert_link_subject_sql(link_id: str, profile_ctx: dict) -> str:
    profile_id = str(profile_ctx.get("profile_id", "")).strip()
    device_id = str(profile_ctx.get("device_id", "")).strip()
    security_profile = str(profile_ctx.get("security_profile", "")).strip()
    profile_revision = str(profile_ctx.get("revision", "")).strip()
    profile_index_key = str(profile_ctx.get("profile_index_key", "")).strip()
    profile_bundle_version = str(profile_ctx.get("profile_bundle_version", "")).strip()
    helper_profile_id = str(profile_ctx.get("helper_profile_id", "")).strip()
    helper_security_profile = str(profile_ctx.get("helper_security_profile", "")).strip()
    helper_profile_revision = str(profile_ctx.get("helper_profile_revision", "")).strip()
    helper_profile_source = str(profile_ctx.get("helper_profile_source", "")).strip()
    metadata = {
        "security_profile": security_profile,
        "profile_revision": profile_revision,
        "profile_index_key": profile_index_key,
        "profile_bundle_version": profile_bundle_version,
        "helper_profile_id": helper_profile_id,
        "helper_security_profile": helper_security_profile,
        "helper_profile_revision": helper_profile_revision,
        "helper_profile_source": helper_profile_source,
    }
    return f"""
        insert into monitor_link_subjects (
            link_id,
            account_id,
            device_id,
            connection_profile_id,
            source,
            observed_at,
            metadata,
            security_profile,
            profile_revision,
            profile_index_key,
            profile_bundle_version,
            helper_profile_id,
            helper_security_profile,
            helper_profile_revision,
            helper_profile_source,
            updated_at
        ) values (
            {sql_str(link_id)},
            '',
            {sql_str(device_id)},
            {sql_str(profile_id)},
            'runtime',
            now(),
            {sql_str(json.dumps(metadata, ensure_ascii=False, sort_keys=True))}::jsonb,
            {sql_str(security_profile)},
            {sql_str(profile_revision)},
            {sql_str(profile_index_key)},
            {sql_str(profile_bundle_version)},
            {sql_str(helper_profile_id)},
            {sql_str(helper_security_profile)},
            {sql_str(helper_profile_revision)},
            {sql_str(helper_profile_source)},
            now()
        )
        on conflict (link_id) do update set
            device_id = excluded.device_id,
            connection_profile_id = excluded.connection_profile_id,
            source = excluded.source,
            observed_at = excluded.observed_at,
            metadata = excluded.metadata,
            security_profile = excluded.security_profile,
            profile_revision = excluded.profile_revision,
            profile_index_key = excluded.profile_index_key,
            profile_bundle_version = excluded.profile_bundle_version,
            helper_profile_id = excluded.helper_profile_id,
            helper_security_profile = excluded.helper_security_profile,
            helper_profile_revision = excluded.helper_profile_revision,
            helper_profile_source = excluded.helper_profile_source,
            updated_at = now()
    """


def _upsert_profile_inventory_sql(profile_ctx: dict, profile_doc: dict) -> str:
    profile_id = str(profile_ctx.get("helper_profile_id") or profile_ctx.get("profile_id") or "").strip()
    revision = str(profile_ctx.get("helper_profile_revision") or profile_ctx.get("revision") or "").strip()
    if not profile_id:
        return ""
    if not revision:
        revision = "unknown"
    security_profile = _first_non_empty(profile_ctx.get("helper_security_profile"), profile_ctx.get("security_profile"), "balanced")
    region = _first_non_empty(profile_doc.get("region"), profile_doc.get("regionID"), profile_doc.get("region_id"))
    ruleset_ref = _first_non_empty(profile_doc.get("rulesetRef"), profile_doc.get("ruleset_ref"))
    dns = profile_doc.get("dns")
    dns_mode = ""
    dns_template = ""
    if isinstance(dns, dict):
        dns_mode = _first_non_empty(dns.get("mode"))
        dns_template = _first_non_empty(dns.get("template"), dns.get("resolverTemplate"))
    source = _first_non_empty(profile_ctx.get("helper_profile_source"), "helper")
    metadata = {
        "profile": profile_doc,
    }
    return f"""
        insert into monitor_profile_inventory (
            profile_id,
            revision,
            security_profile,
            region,
            ruleset_ref,
            dns_mode,
            dns_template,
            source,
            metadata,
            observed_at,
            updated_at
        ) values (
            {sql_str(profile_id)},
            {sql_str(revision)},
            {sql_str(security_profile)},
            {sql_str(region)},
            {sql_str(ruleset_ref)},
            {sql_str(dns_mode)},
            {sql_str(dns_template)},
            {sql_str(source)},
            {sql_str(json.dumps(metadata, ensure_ascii=False, sort_keys=True))}::jsonb,
            now(),
            now()
        )
        on conflict (profile_id, revision) do update set
            security_profile = excluded.security_profile,
            region = excluded.region,
            ruleset_ref = excluded.ruleset_ref,
            dns_mode = excluded.dns_mode,
            dns_template = excluded.dns_template,
            source = excluded.source,
            metadata = excluded.metadata,
            observed_at = excluded.observed_at,
            updated_at = now()
    """


def _snapshot_prev_sql(link_id: str) -> str:
    return f"""
        select row_to_json(t)
        from (
            select
                observed_state,
                health_status,
                session_id,
                error_class,
                last_error,
                gateway_id_selected,
                gateway_addr_selected,
                rx_bytes,
                tx_bytes,
                reconnects,
                handshake_failures,
                observed_at
            from monitor_link_snapshots
            where link_id = {sql_str(link_id)}
            limit 1
        ) t
    """


def _open_link_incidents_sql(link_id: str) -> str:
    return f"""
        select coalesce(json_agg(row_to_json(t)), '[]'::json)
        from (
            select
                i.incident_id,
                i.kind,
                i.severity,
                i.status
            from monitor_incidents i
            join monitor_incident_links il on il.incident_id = i.incident_id
            where il.link_id = {sql_str(link_id)}
              and i.status in ('open', 'acknowledged')
            order by i.opened_at desc
        ) t
    """


def _open_node_incidents_sql(node_id: str) -> str:
    return f"""
        select coalesce(json_agg(row_to_json(t)), '[]'::json)
        from (
            select
                incident_id,
                kind,
                severity,
                status
            from monitor_incidents
            where status in ('open', 'acknowledged')
              and kind = 'node_unreachable'
              and coalesce(metadata_json->>'node_id', '') = {sql_str(node_id)}
            order by opened_at desc
        ) t
    """


def _open_ingestion_gap_incidents_sql() -> str:
    return """
        select coalesce(json_agg(row_to_json(t)), '[]'::json)
        from (
            select
                incident_id,
                kind,
                severity,
                status
            from monitor_incidents
            where status in ('open', 'acknowledged')
              and kind = 'ingestion_gap'
            order by opened_at desc
        ) t
    """


def _event_insert_sql(
    *,
    link_id: str,
    node_id: str,
    session_id: str,
    event_type: str,
    state: str,
    health_status: str,
    error_class: str,
    cause: str,
    payload: dict,
    observed_at: str,
) -> str:
    return f"""
        insert into monitor_link_events (
            link_id,
            node_id,
            session_id,
            event_type,
            state,
            health_status,
            error_class,
            cause,
            payload_json,
            observed_at
        ) values (
            {sql_str(link_id)},
            {sql_str(node_id)},
            {sql_str(session_id)},
            {sql_str(event_type)},
            {sql_str(state)},
            {sql_str(health_status)},
            {sql_str(error_class)},
            {sql_str(cause)},
            {sql_str(json.dumps(payload, ensure_ascii=False, sort_keys=True))}::jsonb,
            {_sql_ts(observed_at)}
        )
    """


def _probe_insert_sql(*, link_id: str, item: dict) -> str:
    health_status = str(item.get("health", "unknown")).strip().lower() or "unknown"
    observed_state = str(item.get("observedState", "unknown")).strip() or "unknown"
    status_map = {
        "healthy": "ok",
        "degraded": "degraded",
        "failed": "failed",
        "stale": "timeout",
        "down": "failed",
        "draining": "degraded",
    }
    status = status_map.get(health_status, "unknown")
    details = {
        "observed_state": observed_state,
        "health_status": health_status,
        "session_id": str(item.get("sessionID", "")).strip(),
        "gateway_id": str(item.get("gatewayID", "")).strip(),
        "error_class": str(item.get("errorClass", "none")).strip() or "none",
        "last_error": str(item.get("lastError", "")).strip(),
    }
    return f"""
        insert into monitor_probes (
            link_id,
            probe_type,
            status,
            details_json,
            observed_at
        ) values (
            {sql_str(link_id)},
            'passive',
            {sql_str(status)},
            {sql_str(json.dumps(details, ensure_ascii=False, sort_keys=True))}::jsonb,
            {_sql_ts(_event_observed_at(item))}
        )
    """


def _build_incident_spec(item: dict) -> dict[str, dict] | None:
    health_status = str(item.get("health", "unknown")).strip().lower() or "unknown"
    observed_state = str(item.get("observedState", "unknown")).strip().lower() or "unknown"
    last_error = str(item.get("lastError", "")).strip()
    error_class = str(item.get("errorClass", "none")).strip() or "none"
    gateway_id = str(item.get("gatewayID", "")).strip()
    if health_status == "failed":
        return {
            "kind": "link_failed",
            "severity": "critical",
            "title": f"Link failed: {item.get('linkID', '')}",
            "summary": last_error or "Link health is failed",
            "metadata": {
                "gateway_id": gateway_id,
                "error_class": error_class,
                "observed_state": observed_state,
            },
        }
    if health_status == "stale":
        return {
            "kind": "link_stale",
            "severity": "warning",
            "title": f"Link stale: {item.get('linkID', '')}",
            "summary": "Link telemetry is stale",
            "metadata": {
                "gateway_id": gateway_id,
                "observed_state": observed_state,
            },
        }
    if health_status == "degraded" or observed_state in {"degraded", "failing_over"}:
        return {
            "kind": "link_degraded",
            "severity": "warning",
            "title": f"Link degraded: {item.get('linkID', '')}",
            "summary": last_error or f"Link state is {observed_state}",
            "metadata": {
                "gateway_id": gateway_id,
                "error_class": error_class,
                "observed_state": observed_state,
            },
        }
    return None


def _build_flapping_incident_spec(item: dict) -> dict[str, dict] | None:
    snapshot = item.get("snapshot")
    if not isinstance(snapshot, dict):
        snapshot = {}
    reconnects = _snapshot_int(snapshot, "Reconnects", "reconnects")
    handshake_failures = _snapshot_int(snapshot, "HandshakeFailures", "handshakeFailures", "handshake_failures")
    reconnects_threshold = max(int(_env("MONITORING_LINK_FLAP_RECONNECTS_THRESHOLD", "3") or 3), 1)
    hs_fail_threshold = max(int(_env("MONITORING_LINK_FLAP_HANDSHAKE_FAILURES_THRESHOLD", "3") or 3), 1)
    if reconnects < reconnects_threshold and handshake_failures < hs_fail_threshold:
        return None
    return {
        "kind": "link_flapping",
        "severity": "warning",
        "title": f"Link flapping: {item.get('linkID', '')}",
        "summary": f"reconnects={reconnects}, handshake_failures={handshake_failures}",
        "metadata": {
            "reconnects": reconnects,
            "handshake_failures": handshake_failures,
            "threshold_reconnects": reconnects_threshold,
            "threshold_handshake_failures": hs_fail_threshold,
            "observed_state": str(item.get("observedState", "unknown")).strip().lower(),
        },
    }


def _doc_flag(doc: dict, keys: tuple[str, ...]) -> bool:
    if not isinstance(doc, dict):
        return False
    for key in keys:
        if key in doc and _as_bool(doc.get(key)):
            return True
    return False


def _build_policy_incident_specs(item: dict, profile_ctx: dict, status_doc: dict, profile_doc: dict) -> dict[str, dict]:
    specs: dict[str, dict] = {}
    link_id = str(item.get("linkID", "")).strip()
    profile_id = str(profile_ctx.get("profile_id", "")).strip()
    security_profile = str(profile_ctx.get("security_profile", "")).strip().lower()
    revision = str(profile_ctx.get("revision", "")).strip()
    profile_index_key = str(profile_ctx.get("profile_index_key", "")).strip()
    snapshot = item.get("snapshot")
    if not isinstance(snapshot, dict):
        snapshot = {}

    if not profile_id or not security_profile:
        specs["link_without_profile"] = {
            "kind": "link_without_profile",
            "severity": "warning",
            "title": f"Link without profile: {link_id}",
            "summary": "link present but profileID/securityProfile missing",
            "metadata": {
                "link_id": link_id,
                "profile_id": profile_id,
                "security_profile": security_profile,
                "revision": revision,
            },
        }

    strict_flags = _doc_flag(
        status_doc,
        (
            "localBridgeDebug",
            "local_bridge_debug",
            "helperAllowApiFallback",
            "helper_allow_api_fallback",
            "forceDirectHTTP",
            "force_direct_http",
            "localControlAPIEnabled",
            "local_control_api_enabled",
        ),
    ) or _doc_flag(
        profile_doc,
        (
            "allowLocalBridgeDebug",
            "allow_local_bridge_debug",
            "allowApiFallback",
            "allow_api_fallback",
            "allowLocalControlApi",
            "allow_local_control_api",
        ),
    )
    if security_profile == "high_risk" and strict_flags:
        specs["high_risk_violation"] = {
            "kind": "high_risk_violation",
            "severity": "critical",
            "title": f"High-risk policy violation: {link_id}",
            "summary": "high_risk profile observed with local debug/fallback controls enabled",
            "metadata": {
                "link_id": link_id,
                "profile_id": profile_id,
                "security_profile": security_profile,
                "profile_index_key": profile_index_key,
            },
        }

    selected_profile_id = _first_non_empty(status_doc.get("selectedProfileID"), status_doc.get("selected_profile_id"))
    helper_profile_id = str(profile_ctx.get("helper_profile_id", "")).strip()
    if selected_profile_id and helper_profile_id and selected_profile_id != helper_profile_id:
        specs["profile_drift"] = {
            "kind": "profile_drift",
            "severity": "warning",
            "title": f"Profile drift: {link_id}",
            "summary": "app selected profile differs from helper profile.current",
            "metadata": {
                "link_id": link_id,
                "selected_profile_id": selected_profile_id,
                "helper_profile_id": helper_profile_id,
                "profile_index_key": profile_index_key,
            },
        }

    error_class = _first_non_empty(status_doc.get("last_error_class"), status_doc.get("lastErrorClass")).strip().lower()
    if error_class == "startup_contract_failure":
        specs["startup_contract_failure"] = {
            "kind": "startup_contract_failure",
            "severity": "critical",
            "title": f"Startup contract failure: {link_id}",
            "summary": "startup blocked by schema/profile contract mismatch",
            "metadata": {
                "link_id": link_id,
                "profile_id": profile_id,
                "profile_index_key": profile_index_key,
            },
        }

    switches = _snapshot_int(snapshot, "gatewaySwitches", "gateway_switches")
    hysteresis_keeps = _snapshot_int(snapshot, "gatewayHysteresisKeeps", "gateway_hysteresis_keeps")
    switch_threshold = max(int(_env("MONITORING_GATEWAY_FLAP_SWITCH_THRESHOLD", "3") or 3), 1)
    hysteresis_threshold = max(int(_env("MONITORING_GATEWAY_FLAP_KEEP_THRESHOLD", "1") or 1), 0)
    if switches >= switch_threshold and hysteresis_keeps <= hysteresis_threshold:
        specs["gateway_flap_risk"] = {
            "kind": "gateway_flap_risk",
            "severity": "warning",
            "title": f"Gateway flap risk: {link_id}",
            "summary": f"gatewaySwitches={switches} with gatewayHysteresisKeeps={hysteresis_keeps}",
            "metadata": {
                "link_id": link_id,
                "gateway_switches": switches,
                "gateway_hysteresis_keeps": hysteresis_keeps,
                "profile_index_key": profile_index_key,
            },
        }
    return specs


def _open_link_incident(db_cfg, *, link_id: str, spec: dict) -> None:
    existing = fetch_json_value(db_cfg, _open_link_incidents_sql(link_id), default=[]) or []
    if any(str(item.get("kind", "")) == spec["kind"] for item in existing if isinstance(item, dict)):
        return
    payload = sql_str(json.dumps(spec.get("metadata", {}), ensure_ascii=False, sort_keys=True))
    sql = f"""
        with ins as (
            insert into monitor_incidents (
                kind,
                severity,
                status,
                title,
                summary,
                metadata_json,
                opened_at
            ) values (
                {sql_str(spec['kind'])},
                {sql_str(spec['severity'])},
                'open',
                {sql_str(spec['title'])},
                {sql_str(spec['summary'])},
                {payload}::jsonb,
                now()
            )
            returning incident_id
        )
        insert into monitor_incident_links (incident_id, link_id)
        select incident_id, {sql_str(link_id)}
        from ins
    """
    exec_sql(db_cfg, sql)


def _resolve_link_incident(db_cfg, *, link_id: str, kind: str) -> None:
    sql = f"""
        update monitor_incidents i
        set
            status = 'resolved',
            resolved_at = now()
        where i.incident_id in (
            select il.incident_id
            from monitor_incident_links il
            where il.link_id = {sql_str(link_id)}
        )
          and i.kind = {sql_str(kind)}
          and i.status in ('open', 'acknowledged')
    """
    exec_sql(db_cfg, sql)


def _open_node_unreachable_incident(db_cfg, *, node_id: str, detail: str) -> None:
    existing = fetch_json_value(db_cfg, _open_node_incidents_sql(node_id), default=[]) or []
    if existing:
        return
    metadata = json.dumps({"node_id": node_id, "detail": detail}, ensure_ascii=False, sort_keys=True)
    sql = f"""
        insert into monitor_incidents (
            kind,
            severity,
            status,
            title,
            summary,
            metadata_json,
            opened_at
        ) values (
            'node_unreachable',
            'critical',
            'open',
            {sql_str(f'Node unreachable: {node_id}')},
            {sql_str(detail)},
            {sql_str(metadata)}::jsonb,
            now()
        )
    """
    exec_sql(db_cfg, sql)


def _resolve_node_unreachable_incident(db_cfg, *, node_id: str) -> None:
    sql = f"""
        update monitor_incidents
        set
            status = 'resolved',
            resolved_at = now()
        where kind = 'node_unreachable'
          and status in ('open', 'acknowledged')
          and coalesce(metadata_json->>'node_id', '') = {sql_str(node_id)}
    """
    exec_sql(db_cfg, sql)


def _resolve_old_node_unreachable_incidents(db_cfg, *, max_age_sec: int) -> None:
    ttl = max(int(max_age_sec), 1)
    sql = f"""
        update monitor_incidents
        set
            status = 'resolved',
            resolved_at = now()
        where kind = 'node_unreachable'
          and status in ('open', 'acknowledged')
          and opened_at < now() - ({ttl} * interval '1 second')
    """
    exec_sql(db_cfg, sql)


def _open_ingestion_gap_incident(db_cfg, *, detail: str) -> None:
    existing = fetch_json_value(db_cfg, _open_ingestion_gap_incidents_sql(), default=[]) or []
    if existing:
        return
    metadata = json.dumps({"detail": str(detail or "").strip()}, ensure_ascii=False, sort_keys=True)
    sql = f"""
        insert into monitor_incidents (
            kind,
            severity,
            status,
            title,
            summary,
            metadata_json,
            opened_at
        ) values (
            'ingestion_gap',
            'critical',
            'open',
            'Monitoring ingestion gap',
            {sql_str(str(detail or '').strip() or 'monitoring ingestion sources unavailable')},
            {sql_str(metadata)}::jsonb,
            now()
        )
    """
    exec_sql(db_cfg, sql)


def _resolve_ingestion_gap_incident(db_cfg) -> None:
    sql = """
        update monitor_incidents
        set
            status = 'resolved',
            resolved_at = now()
        where kind = 'ingestion_gap'
          and status in ('open', 'acknowledged')
    """
    exec_sql(db_cfg, sql)


def _resolve_inactive_link_incidents_sql(max_age_sec: int) -> str:
    ttl = max(int(max_age_sec), 1)
    return f"""
        with active_problem_links as (
            select c.link_id
            from monitor_v_link_current c
            where coalesce(c.desired_state, '') <> 'down'
              and coalesce(c.observed_state, '') not in ('stopped', 'removed')
              and (
                c.health_status in ('degraded', 'failed', 'stale')
                or c.observed_state in ('starting', 'failing_over', 'orphaned')
              )
        )
        update monitor_incidents i
        set
            status = 'resolved',
            resolved_at = now()
        where i.kind in ('link_failed', 'link_stale', 'link_degraded', 'link_flapping', 'gateway_flap_risk')
          and i.status in ('open', 'acknowledged')
          and i.opened_at < now() - ({ttl} * interval '1 second')
          and exists (
            select 1
            from monitor_incident_links il
            where il.incident_id = i.incident_id
          )
          and not exists (
            select 1
            from monitor_incident_links il
            join active_problem_links apl on apl.link_id = il.link_id
            where il.incident_id = i.incident_id
          )
    """


def _pending_alert_incidents_sql(limit: int, min_severity: str, max_age_sec: int) -> str:
    min_rank = _severity_rank(min_severity)
    ttl = max(int(max_age_sec), 60)
    return f"""
        with ranked as (
            select
                i.incident_id,
                i.kind,
                i.severity,
                i.status,
                i.title,
                i.summary,
                i.opened_at,
                i.metadata_json,
                case
                    when i.severity = 'critical' then 3
                    when i.severity = 'warning' then 2
                    when i.severity = 'info' then 1
                    else 0
                end as severity_rank
            from monitor_incidents i
            where i.status in ('open', 'acknowledged')
              and i.opened_at >= now() - ({ttl} * interval '1 second')
        )
        select coalesce(json_agg(row_to_json(t) order by t.severity_rank desc, t.opened_at desc), '[]'::json)
        from (
            select
                r.*,
                coalesce(
                    (
                        select json_agg(il.link_id order by il.link_id)
                        from monitor_incident_links il
                        where il.incident_id = r.incident_id
                    ),
                    '[]'::json
                ) as link_ids,
                coalesce(d_tg.status, '') as tg_status,
                coalesce(d_max.status, '') as max_status
            from ranked r
            left join monitor_alert_deliveries d_tg
              on d_tg.incident_id = r.incident_id
             and d_tg.channel = 'tg'
            left join monitor_alert_deliveries d_max
              on d_max.incident_id = r.incident_id
             and d_max.channel = 'max'
            where r.severity_rank >= {min_rank}
            order by r.severity_rank desc, r.opened_at desc
            limit {max(limit, 1)}
        ) t
    """


def _upsert_alert_delivery_sql(*, incident_id: str, channel: str, status: str, last_error: str) -> str:
    return f"""
        insert into monitor_alert_deliveries (
            incident_id,
            channel,
            status,
            attempt,
            last_error,
            delivered_at,
            updated_at
        ) values (
            {sql_str(incident_id)}::uuid,
            {sql_str(channel)},
            {sql_str(status)},
            1,
            {sql_str(last_error)},
            now(),
            now()
        )
        on conflict (incident_id, channel) do update set
            status = excluded.status,
            attempt = monitor_alert_deliveries.attempt + 1,
            last_error = excluded.last_error,
            delivered_at = excluded.delivered_at,
            updated_at = now()
    """


def _post_json(url: str, payload: dict, timeout_sec: float) -> None:
    body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = Request(
        str(url).strip(),
        method="POST",
        headers={"Content-Type": "application/json; charset=utf-8", "Accept": "application/json"},
        data=body,
    )
    with urlopen(req, timeout=timeout_sec) as resp:
        code = int(getattr(resp, "status", 200) or 200)
        if code >= 400:
            raise HelperClientError(f"alert http {code}")


def _post_form(url: str, payload: dict[str, str], timeout_sec: float) -> None:
    body = urlencode(payload).encode("utf-8")
    req = Request(
        str(url).strip(),
        method="POST",
        headers={
            "Content-Type": "application/x-www-form-urlencoded",
            "Accept": "application/json",
        },
        data=body,
    )
    with urlopen(req, timeout=timeout_sec) as resp:
        code = int(getattr(resp, "status", 200) or 200)
        if code >= 400:
            raise HelperClientError(f"alert http {code}")


def _tg_send_message(token: str, chat_id: str, text: str, timeout_sec: float) -> tuple[bool, str, str]:
    url = f"https://api.telegram.org/bot{token}/sendMessage"
    payload = {
        "chat_id": chat_id,
        "text": text,
        "parse_mode": "HTML",
        "disable_web_page_preview": "true",
    }
    body = urlencode(payload).encode("utf-8")
    req = Request(
        url,
        method="POST",
        headers={
            "Content-Type": "application/x-www-form-urlencoded",
            "Accept": "application/json",
        },
        data=body,
    )
    try:
        with urlopen(req, timeout=timeout_sec) as resp:
            code = int(getattr(resp, "status", 200) or 200)
            if code >= 400:
                return False, "", f"tg_http_{code}"
            return True, "", ""
    except HTTPError as exc:
        raw = exc.read().decode("utf-8", "ignore")
        migrate_to = ""
        try:
            data = json.loads(raw or "{}")
            params = data.get("parameters")
            if isinstance(params, dict):
                migrate_to = str(params.get("migrate_to_chat_id", "")).strip()
        except Exception:
            migrate_to = ""
        return False, migrate_to, raw.strip() or str(exc)
    except Exception as exc:
        return False, "", str(exc)


def _post_json_with_headers(url: str, payload: dict, timeout_sec: float, headers: dict[str, str]) -> None:
    body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req_headers = {"Content-Type": "application/json; charset=utf-8", "Accept": "application/json"}
    req_headers.update(headers)
    req = Request(
        str(url).strip(),
        method="POST",
        headers=req_headers,
        data=body,
    )
    with urlopen(req, timeout=timeout_sec) as resp:
        code = int(getattr(resp, "status", 200) or 200)
        if code >= 400:
            raise HelperClientError(f"alert http {code}")


def _format_alert_text(incident: dict) -> str:
    sev = str(incident.get("severity", "")).strip().upper()
    kind = str(incident.get("kind", "")).strip()
    title = str(incident.get("title", "")).strip()
    summary = str(incident.get("summary", "")).strip()
    incident_id = str(incident.get("incident_id", "")).strip()
    opened_at = str(incident.get("opened_at", "")).strip()
    links = incident.get("link_ids")
    links_total = 0
    if isinstance(links, list):
        links_total = len(links)
    return (
        f"[{sev}] {kind}\n"
        f"{title}\n"
        f"{summary}\n"
        f"incident={incident_id}\n"
        f"links={links_total}\n"
        f"opened={opened_at}"
    )


def _send_tg_alert(incident: dict) -> tuple[bool, str]:
    webhook = _env("MONITORING_ALERT_TG_WEBHOOK_URL", "")
    bot_token = _env("MONITORING_ALERT_TG_BOT_TOKEN", "")
    chat_id = _env("MONITORING_ALERT_TG_CHAT_ID", "")
    text = _format_alert_text(incident)
    timeout = float(max(int(_env("MONITORING_ALERT_TIMEOUT_SEC", "5") or 5), 1))
    try:
        if bot_token and chat_id:
            ok, migrate_to, err = _tg_send_message(bot_token, chat_id, text, timeout)
            if ok:
                return True, ""
            if migrate_to:
                ok2, _, err2 = _tg_send_message(bot_token, migrate_to, text, timeout)
                if ok2:
                    return True, ""
                return False, err2
            return False, err
        if webhook:
            _post_json(webhook, {"text": text, "incident": incident, "channel": "tg"}, timeout)
            return True, ""
        return False, "tg_not_configured"
    except Exception as exc:
        return False, str(exc)


def _send_max_alert(incident: dict) -> tuple[bool, str]:
    webhook = _env("MONITORING_ALERT_MAX_WEBHOOK_URL", "")
    max_token = _env("MONITORING_ALERT_MAX_TOKEN", "")
    max_base_url = _env("MONITORING_ALERT_MAX_BASE_URL", "")
    max_chat_id = _env("MONITORING_ALERT_MAX_CHAT_ID", "")
    text = _format_alert_text(incident)
    timeout = float(max(int(_env("MONITORING_ALERT_TIMEOUT_SEC", "5") or 5), 1))
    try:
        if max_token and max_base_url and max_chat_id:
            base = max_base_url.rstrip("/")
            url = f"{base}/messages?chat_id={max_chat_id}"
            _post_json_with_headers(
                url,
                {"text": text},
                timeout,
                {"Authorization": max_token},
            )
            return True, ""
        if webhook:
            _post_json(webhook, {"text": text, "incident": incident, "channel": "max"}, timeout)
            return True, ""
        return False, "max_not_configured"
    except Exception as exc:
        return False, str(exc)


def _dispatch_alert_notifications(db_cfg) -> None:
    if not _env_bool("MONITORING_ALERTS_ENABLED", True):
        return
    limit = max(int(_env("MONITORING_ALERTS_BATCH_SIZE", "50") or 50), 1)
    min_severity = _env("MONITORING_ALERT_MIN_SEVERITY", "warning") or "warning"
    max_age_sec = max(int(_env("MONITORING_ALERT_MAX_AGE_SEC", "604800") or 604800), 60)
    incidents = fetch_json_value(db_cfg, _pending_alert_incidents_sql(limit, min_severity, max_age_sec), default=[]) or []
    if not isinstance(incidents, list):
        return
    for incident in incidents:
        if not isinstance(incident, dict):
            continue
        incident_id = str(incident.get("incident_id", "")).strip()
        if not incident_id:
            continue
        tg_status = str(incident.get("tg_status", "")).strip().lower()
        max_status = str(incident.get("max_status", "")).strip().lower()
        if tg_status != "succeeded":
            ok, err = _send_tg_alert(incident)
            exec_sql(
                db_cfg,
                _upsert_alert_delivery_sql(
                    incident_id=incident_id,
                    channel="tg",
                    status="succeeded" if ok else "failed",
                    last_error="" if ok else err,
                ),
            )
        if max_status != "succeeded":
            ok, err = _send_max_alert(incident)
            exec_sql(
                db_cfg,
                _upsert_alert_delivery_sql(
                    incident_id=incident_id,
                    channel="max",
                    status="succeeded" if ok else "failed",
                    last_error="" if ok else err,
                ),
            )


def _event_observed_at(item: dict) -> str:
    for key in ("lastTransitionAt", "lastHandshakeAt", "lastRxAt", "lastTxAt"):
        raw = str(item.get(key, "")).strip()
        if raw:
            return raw
    return _utc_now_iso()


def _emit_link_events(db_cfg, *, node_id: str, link_id: str, prev: dict | None, item: dict) -> None:
    current = {
        "observed_state": str(item.get("observedState", "unknown")).strip() or "unknown",
        "health_status": str(item.get("health", "unknown")).strip().lower() or "unknown",
        "session_id": str(item.get("sessionID", "")).strip(),
        "error_class": str(item.get("errorClass", "none")).strip() or "none",
        "last_error": str(item.get("lastError", "")).strip(),
        "gateway_id_selected": str(item.get("gatewayID", "")).strip(),
        "gateway_addr_selected": str(item.get("gatewayAddr", "")).strip(),
    }
    observed_at = _event_observed_at(item)
    events: list[tuple[str, str, dict]] = []
    if not prev:
        events.append(
            (
                "link.discovered",
                "initial_observation",
                {
                    "current": current,
                    "snapshot": item,
                },
            )
        )
    else:
        transitions = (
            ("observed_state", "link.state_changed", "state_transition"),
            ("health_status", "link.health_changed", "health_transition"),
            ("session_id", "link.session_changed", "session_rotation"),
            ("gateway_id_selected", "link.gateway_changed", "gateway_reselection"),
            ("error_class", "link.error_class_changed", "error_transition"),
            ("last_error", "link.error_message_changed", "error_transition"),
        )
        for field, event_type, cause in transitions:
            before = str(prev.get(field, "") or "")
            after = str(current.get(field, "") or "")
            if before != after:
                events.append(
                    (
                        event_type,
                        cause,
                        {
                            "field": field,
                            "before": before,
                            "after": after,
                            "snapshot": item,
                        },
                    )
                )
    for event_type, cause, payload in events:
        exec_sql(
            db_cfg,
            _event_insert_sql(
                link_id=link_id,
                node_id=node_id,
                session_id=current["session_id"],
                event_type=event_type,
                state=current["observed_state"],
                health_status=current["health_status"],
                error_class=current["error_class"],
                cause=cause,
                payload=payload,
                observed_at=observed_at,
            ),
        )


def _reconcile_link_incidents(db_cfg, *, link_id: str, item: dict) -> None:
    monitor_source = str(item.get("monitor_source", "helper")).strip().lower()
    observed_state = str(item.get("observedState", "unknown")).strip().lower()
    error_class = str(item.get("errorClass", "none")).strip().lower()
    if monitor_source == "control_peer" and observed_state in {"starting", "planned"} and error_class == "control_peer_starting":
        for kind in ("link_failed", "link_stale", "link_degraded", "link_flapping"):
            _resolve_link_incident(db_cfg, link_id=link_id, kind=kind)
        return
    active = _build_incident_spec(item)
    expected_kind = str(active.get("kind", "")) if active else ""
    for kind in ("link_failed", "link_stale", "link_degraded"):
        if kind == expected_kind and active:
            _open_link_incident(db_cfg, link_id=link_id, spec=active)
            continue
        _resolve_link_incident(db_cfg, link_id=link_id, kind=kind)
    flap = _build_flapping_incident_spec(item)
    if flap:
        _open_link_incident(db_cfg, link_id=link_id, spec=flap)
    else:
        _resolve_link_incident(db_cfg, link_id=link_id, kind="link_flapping")


def _reconcile_policy_incidents(
    db_cfg,
    *,
    link_id: str,
    item: dict,
    profile_ctx: dict,
    status_doc: dict,
    profile_doc: dict,
) -> None:
    specs = _build_policy_incident_specs(item, profile_ctx, status_doc, profile_doc)
    all_kinds = (
        "high_risk_violation",
        "profile_drift",
        "startup_contract_failure",
        "link_without_profile",
        "gateway_flap_risk",
    )
    for kind in all_kinds:
        spec = specs.get(kind)
        if spec:
            _open_link_incident(db_cfg, link_id=link_id, spec=spec)
            continue
        _resolve_link_incident(db_cfg, link_id=link_id, kind=kind)


def _ingest_link_item(
    db_cfg,
    *,
    node_id: str,
    item: dict,
    snapshot_source: str,
    status_doc: dict,
    profile_doc: dict,
) -> bool:
    link_id = str(item.get("linkID", "")).strip()
    if not link_id:
        return False
    profile_ctx = _extract_profile_context(item, status_doc, profile_doc)
    prev = fetch_json_value(db_cfg, _snapshot_prev_sql(link_id), default={}) or {}
    link_sql = _upsert_link_sql(node_id, item)
    snap_sql = _upsert_snapshot_sql(link_id, item, snapshot_source=snapshot_source)
    subj_sql = _upsert_link_subject_sql(link_id, profile_ctx)
    profile_inventory_sql = _upsert_profile_inventory_sql(profile_ctx, profile_doc)
    if link_sql:
        exec_sql(db_cfg, link_sql)
    if snap_sql:
        exec_sql(db_cfg, snap_sql)
    if subj_sql:
        exec_sql(db_cfg, subj_sql)
    if profile_inventory_sql:
        exec_sql(db_cfg, profile_inventory_sql)
    exec_sql(db_cfg, _probe_insert_sql(link_id=link_id, item=item))
    _emit_link_events(db_cfg, node_id=node_id, link_id=link_id, prev=prev if isinstance(prev, dict) and prev else None, item=item)
    _reconcile_link_incidents(db_cfg, link_id=link_id, item=item)
    monitor_source = str(item.get("monitor_source", "helper")).strip().lower() or "helper"
    if monitor_source == "helper" and snapshot_source != "sse":
        _reconcile_policy_incidents(
            db_cfg,
            link_id=link_id,
            item=item,
            profile_ctx=profile_ctx,
            status_doc=status_doc,
            profile_doc=profile_doc,
        )
    return True


def _ingest_node_links(db_cfg, node: dict, timeout_sec: int) -> int:
    node_id = str(node.get("node_id", "")).strip()
    base_url = str(node.get("helper_base_url", "")).strip()
    auth_ref = str(node.get("helper_auth_ref", "")).strip()
    if not node_id or not base_url:
        return 0
    schema = fetch_helper_schema(base_url, auth_ref=auth_ref, timeout_sec=float(timeout_sec))
    _validate_helper_schema(schema)
    items = fetch_helper_links(base_url, auth_ref=auth_ref, timeout_sec=float(timeout_sec))
    status_doc: dict = {}
    profile_doc: dict = {}
    try:
        status_raw = fetch_helper_status(base_url, auth_ref=auth_ref, timeout_sec=float(timeout_sec))
        if isinstance(status_raw, dict):
            status_doc = status_raw
    except HelperClientError:
        status_doc = {}
    try:
        profile_raw = fetch_helper_profile_current(base_url, auth_ref=auth_ref, timeout_sec=float(timeout_sec))
        profile_doc = _pull_profile_doc(profile_raw)
    except HelperClientError:
        profile_doc = {}
    exec_sql(db_cfg, _set_node_status_sql(node_id, "active"))
    _resolve_node_unreachable_incident(db_cfg, node_id=node_id)
    count = 0
    present_link_ids: list[str] = []
    for item in items:
        if isinstance(item, dict):
            item = dict(item)
            item["monitor_source"] = "helper"
        link_id = str(item.get("linkID", "")).strip()
        if not link_id:
            continue
        present_link_ids.append(link_id)
        if _ingest_link_item(
            db_cfg,
            node_id=node_id,
            item=item,
            snapshot_source="poll",
            status_doc=status_doc,
            profile_doc=profile_doc,
        ):
            count += 1
    exec_sql(db_cfg, _mark_missing_links_stale_sql(node_id, present_link_ids, source="helper"))
    return count


def _ingest_control_peer_links(db_cfg, links: list[tuple[str, dict]]) -> int:
    count = 0
    per_node_present: dict[str, list[str]] = {}
    for node_id, item in links:
        node_id = str(node_id or "").strip()
        if not node_id or not isinstance(item, dict):
            continue
        peer_doc = item.get("peer")
        if not isinstance(peer_doc, dict):
            peer_doc = {}
        edge_id = _first_non_empty(
            peer_doc.get("effective_edge"),
            peer_doc.get("ingress_edge"),
            node_id.split("-", 1)[0],
        )
        display_name = _first_non_empty(
            peer_doc.get("edge_name"),
            peer_doc.get("edge"),
            edge_id.upper() if edge_id else node_id,
        )
        exec_sql(
            db_cfg,
            _upsert_node_sql(
                {
                    "node_id": node_id,
                    "display_name": display_name or node_id,
                    "role": "runtime",
                    "region": edge_id or "",
                    "edge_id": edge_id or "",
                    "host": "",
                    "helper_base_url": "",
                    "helper_auth_ref": "",
                }
            ),
        )
        exec_sql(db_cfg, _set_node_status_sql(node_id, "active"))
        _resolve_node_unreachable_incident(db_cfg, node_id=node_id)
        link_id = str(item.get("linkID", "")).strip()
        if not link_id:
            continue
        per_node_present.setdefault(node_id, []).append(link_id)
        if _ingest_link_item(
            db_cfg,
            node_id=node_id,
            item=item,
            snapshot_source="derived",
            status_doc={},
            profile_doc={},
        ):
            count += 1
    for node_id, present in per_node_present.items():
        exec_sql(db_cfg, _mark_missing_links_stale_sql(node_id, present, source="control_peer"))
    return count


def _pending_commands_sql(limit: int) -> str:
    return f"""
        select coalesce(json_agg(row_to_json(t) order by t.accepted_at asc), '[]'::json)
        from (
            select
                command_id,
                command_type,
                node_id,
                link_id,
                request_json,
                response_json,
                accepted_at
            from monitor_commands
            where status = 'accepted'
              and (
                nullif(coalesce(response_json->'dispatch'->>'next_retry_at', ''), '') is null
                or (response_json->'dispatch'->>'next_retry_at')::timestamptz <= now()
              )
            order by accepted_at asc
            limit {max(limit, 1)}
        ) t
    """


def _mark_command_dispatched_sql(command_id: str, response_json: dict) -> str:
    return f"""
        update monitor_commands
        set
            status = 'dispatched',
            dispatched_at = now(),
            response_json = {sql_str(json.dumps(response_json, ensure_ascii=False, sort_keys=True))}::jsonb
        where command_id::text = {sql_str(command_id)}
          and status = 'accepted'
    """


def _mark_command_succeeded_sql(command_id: str, response_json: dict) -> str:
    return f"""
        update monitor_commands
        set
            status = 'succeeded',
            dispatched_at = coalesce(dispatched_at, now()),
            finished_at = now(),
            response_json = {sql_str(json.dumps(response_json, ensure_ascii=False, sort_keys=True))}::jsonb
        where command_id::text = {sql_str(command_id)}
    """


def _mark_command_failed_sql(command_id: str, response_json: dict) -> str:
    return f"""
        update monitor_commands
        set
            status = 'failed',
            finished_at = now(),
            response_json = {sql_str(json.dumps(response_json, ensure_ascii=False, sort_keys=True))}::jsonb
        where command_id::text = {sql_str(command_id)}
    """


def _mark_command_retry_sql(command_id: str, response_json: dict) -> str:
    return f"""
        update monitor_commands
        set
            status = 'accepted',
            dispatched_at = coalesce(dispatched_at, now()),
            response_json = {sql_str(json.dumps(response_json, ensure_ascii=False, sort_keys=True))}::jsonb
        where command_id::text = {sql_str(command_id)}
    """


def _dispatch_attempt(command: dict) -> int:
    response_json = command.get("response_json")
    if not isinstance(response_json, dict):
        return 0
    dispatch = response_json.get("dispatch")
    if not isinstance(dispatch, dict):
        return 0
    try:
        return int(dispatch.get("attempt", 0) or 0)
    except (TypeError, ValueError):
        return 0


def _next_backoff_seconds(attempt: int, base_sec: int, max_sec: int) -> int:
    if attempt <= 1:
        return max(base_sec, 1)
    delay = base_sec * (2 ** (attempt - 1))
    return min(max(delay, 1), max(max_sec, 1))


def _dispatch_commands(
    db_cfg,
    *,
    node_index: dict[str, dict],
    timeout_sec: int,
    batch_size: int,
    retry_max_attempts: int,
    retry_base_sec: int,
    retry_max_sec: int,
) -> tuple[int, int]:
    commands = fetch_json_value(db_cfg, _pending_commands_sql(batch_size), default=[]) or []
    if not isinstance(commands, list):
        return 0, 0
    action_map = {
        "reconnect": "reconnect",
        "drain": "drain",
        "resume": "resume",
        "select_gateway": "gateway.select",
    }
    dispatched = 0
    failed = 0
    for cmd in commands:
        if not isinstance(cmd, dict):
            continue
        command_id = str(cmd.get("command_id", "")).strip()
        command_type = str(cmd.get("command_type", "")).strip()
        node_id = str(cmd.get("node_id", "")).strip()
        link_id = str(cmd.get("link_id", "")).strip()
        attempt = _dispatch_attempt(cmd) + 1
        if not command_id or not node_id:
            continue
        action = action_map.get(command_type)
        if not action:
            exec_sql(
                db_cfg,
                _mark_command_failed_sql(
                    command_id,
                    {"error": "command_type_not_dispatchable", "command_type": command_type},
                ),
            )
            failed += 1
            continue
        if not link_id:
            exec_sql(
                db_cfg,
                _mark_command_failed_sql(
                    command_id,
                    {"error": "link_id_required_for_dispatch", "command_type": command_type},
                ),
            )
            failed += 1
            continue
        node = node_index.get(node_id) or {}
        base_url = str(node.get("helper_base_url", "")).strip()
        auth_ref = str(node.get("helper_auth_ref", "")).strip()
        if not base_url:
            continue
        request_json = cmd.get("request_json")
        if not isinstance(request_json, dict):
            request_json = {}
        args = request_json.get("args")
        if not isinstance(args, dict):
            args = {}
        payload = {}
        if action == "gateway.select":
            payload = {"gatewayID": str(args.get("gateway_id", "")).strip()}
        dispatch_note = {
            "dispatcher": "monitoring-ingestor",
            "action": action,
            "helper_base_url": base_url,
            "at": _utc_now_iso(),
            "attempt": attempt,
        }
        exec_sql(db_cfg, _mark_command_dispatched_sql(command_id, dispatch_note))
        try:
            helper_response = post_helper_link_action(
                base_url=base_url,
                link_id=link_id,
                action=action,
                payload=payload,
                auth_ref=auth_ref,
                timeout_sec=float(timeout_sec),
            )
            exec_sql(
                db_cfg,
                _mark_command_succeeded_sql(
                    command_id,
                    {
                        "dispatch": {
                            "attempt": attempt,
                            "last_error": "",
                            "last_error_at": "",
                            "next_retry_at": "",
                            "backoff_sec": 0,
                        },
                        "dispatcher": dispatch_note,
                        "helper_response": helper_response,
                    },
                ),
            )
            dispatched += 1
        except HelperClientError as exc:
            delay_sec = _next_backoff_seconds(attempt, retry_base_sec, retry_max_sec)
            next_retry_at = datetime.now(timezone.utc) + timedelta(seconds=delay_sec)
            retry_payload = {
                "dispatch": {
                    "attempt": attempt,
                    "last_error": str(exc),
                    "last_error_at": _utc_now_iso(),
                    "next_retry_at": next_retry_at.strftime("%Y-%m-%dT%H:%M:%SZ"),
                    "backoff_sec": delay_sec,
                }
            }
            if attempt < max(retry_max_attempts, 1):
                exec_sql(db_cfg, _mark_command_retry_sql(command_id, retry_payload))
                continue
            exec_sql(
                db_cfg,
                _mark_command_failed_sql(
                    command_id,
                    {
                        "dispatcher": dispatch_note,
                        "dispatch": retry_payload["dispatch"],
                        "error": str(exc),
                    },
                ),
            )
            failed += 1
    return dispatched, failed


def _sse_stream_once(
    *,
    url: str,
    auth_ref: str,
    timeout_sec: float,
    on_event,
) -> None:
    headers = {"Accept": "text/event-stream"}
    headers.update(_helper_auth_headers(auth_ref))
    req = Request(url, method="GET", headers=headers)
    try:
        resp = urlopen(req, timeout=timeout_sec)
    except HTTPError as exc:
        body = exc.read().decode("utf-8", "replace")
        raise HelperClientError(f"sse http {exc.code}: {body.strip()}") from exc
    except URLError as exc:
        raise HelperClientError(f"sse url error: {exc}") from exc
    with resp:
        event_name = "message"
        data_lines: list[str] = []
        while True:
            raw = resp.readline()
            if not raw:
                break
            line = raw.decode("utf-8", "replace").rstrip("\r\n")
            if line == "":
                if data_lines:
                    payload_raw = "\n".join(data_lines)
                    payload: dict = {}
                    if payload_raw:
                        try:
                            parsed = json.loads(payload_raw)
                            if isinstance(parsed, dict):
                                payload = parsed
                        except json.JSONDecodeError:
                            payload = {"raw": payload_raw}
                    on_event(event_name, payload)
                event_name = "message"
                data_lines = []
                continue
            if line.startswith(":"):
                continue
            if line.startswith("event:"):
                event_name = line.split(":", 1)[1].strip() or "message"
                continue
            if line.startswith("data:"):
                data_lines.append(line.split(":", 1)[1].lstrip())
                continue


class _HelperRealtimeWorker(threading.Thread):
    def __init__(self, *, db_cfg, node: dict) -> None:
        super().__init__(daemon=True)
        self.db_cfg = db_cfg
        self.node = dict(node)
        self.node_id = str(self.node.get("node_id", "")).strip()
        self.base_url = str(self.node.get("helper_base_url", "")).strip().rstrip("/")
        self.auth_ref = str(self.node.get("helper_auth_ref", "")).strip()
        self._stop_event = threading.Event()
        self._last_refresh_at = 0.0

    @property
    def signature(self) -> str:
        return f"{self.base_url}|{self.auth_ref}"

    def stop(self) -> None:
        self._stop_event.set()

    def _on_links_snapshot(self, links: list[dict]) -> None:
        present: list[str] = []
        count = 0
        for item in links:
            if not isinstance(item, dict):
                continue
            row = dict(item)
            row["monitor_source"] = "helper"
            link_id = str(row.get("linkID", "")).strip()
            if not link_id:
                continue
            present.append(link_id)
            if _ingest_link_item(
                self.db_cfg,
                node_id=self.node_id,
                item=row,
                snapshot_source="sse",
                status_doc={},
                profile_doc={},
            ):
                count += 1
        if present:
            exec_sql(self.db_cfg, _mark_missing_links_stale_sql(self.node_id, present, source="helper"))
        if count > 0:
            exec_sql(self.db_cfg, _set_node_status_sql(self.node_id, "active"))
            _resolve_node_unreachable_incident(self.db_cfg, node_id=self.node_id)

    def _refresh_from_poll_if_needed(self) -> None:
        refresh_sec = max(int(_env("MONITORING_HELPER_STREAM_REFRESH_SEC", "15") or 15), 1)
        now = time.time()
        if now - self._last_refresh_at < refresh_sec:
            return
        _ingest_node_links(self.db_cfg, self.node, max(int(_env("MONITORING_HELPER_TIMEOUT_SEC", "5") or 5), 1))
        self._last_refresh_at = now

    def _run_stream(self, *, path: str, duration_sec: int, interval: str) -> None:
        url = f"{self.base_url}{path}?interval={interval}&duration={duration_sec}s"
        timeout_sec = float(duration_sec + 15)

        def _handle_event(event_name: str, payload: dict) -> None:
            event = str(event_name or "").strip().lower()
            if event == "links":
                links = payload.get("links")
                if isinstance(links, list):
                    self._on_links_snapshot([x for x in links if isinstance(x, dict)])
                return
            if event in {"runtime", "status", "daemon"}:
                self._refresh_from_poll_if_needed()
                return
            if event == "done":
                return

        _sse_stream_once(url=url, auth_ref=self.auth_ref, timeout_sec=timeout_sec, on_event=_handle_event)

    def run(self) -> None:
        if not self.node_id or not self.base_url:
            return
        duration_sec = max(int(_env("MONITORING_HELPER_STREAM_DURATION_SEC", "25") or 25), 5)
        interval = _env("MONITORING_HELPER_STREAM_INTERVAL", "5s") or "5s"
        retry_sleep_sec = max(int(_env("MONITORING_HELPER_STREAM_RETRY_SEC", "3") or 3), 1)
        while not self._stop_event.is_set():
            try:
                # Primary realtime feed
                self._run_stream(path="/v1/helper/links/health.stream", duration_sec=duration_sec, interval=interval)
                continue
            except Exception:
                pass
            try:
                # Fallback / extra context stream
                self._run_stream(path="/v1/helper/bridge.status.stream", duration_sec=duration_sec, interval=interval)
                continue
            except Exception:
                pass
            self._stop_event.wait(retry_sleep_sec)


class _RealtimeManager:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._workers: dict[str, _HelperRealtimeWorker] = {}

    def sync(self, db_cfg, nodes: list[dict]) -> None:
        if not _env_bool("MONITORING_HELPER_STREAM_ENABLED", True):
            self.stop_all()
            return
        desired: dict[str, dict] = {}
        for node in nodes:
            if not isinstance(node, dict):
                continue
            node_id = str(node.get("node_id", "")).strip()
            base_url = str(node.get("helper_base_url", "")).strip()
            if not node_id or not base_url:
                continue
            desired[node_id] = node
        with self._lock:
            current = set(self._workers.keys())
            target = set(desired.keys())
            for node_id in sorted(current - target):
                worker = self._workers.pop(node_id, None)
                if worker:
                    worker.stop()
            for node_id in sorted(target):
                node = desired[node_id]
                signature = f"{str(node.get('helper_base_url', '')).strip().rstrip('/')}|{str(node.get('helper_auth_ref', '')).strip()}"
                worker = self._workers.get(node_id)
                if worker and worker.signature == signature and worker.is_alive():
                    continue
                if worker:
                    worker.stop()
                nxt = _HelperRealtimeWorker(db_cfg=db_cfg, node=node)
                self._workers[node_id] = nxt
                nxt.start()

    def stop_all(self) -> None:
        with self._lock:
            for worker in self._workers.values():
                worker.stop()
            self._workers = {}


REALTIME_MANAGER = _RealtimeManager()


class Handler(BaseHTTPRequestHandler):
    server_version = "tun-monitoring-ingestor/0.1"

    def do_GET(self) -> None:
        if self.path == "/healthz":
            self._write_json(HTTPStatus.OK, {"ok": True, "service": "monitoring-ingestor"})
            return
        if self.path == "/status":
            self._write_json(
                HTTPStatus.OK,
                {
                    "ok": True,
                    "status": {
                        "last_run_at": STATE.last_run_at,
                        "last_error": STATE.last_error,
                        "nodes_seen": STATE.nodes_seen,
                        "links_seen": STATE.links_seen,
                        "commands_dispatched": STATE.commands_dispatched,
                        "commands_failed": STATE.commands_failed,
                    },
                },
            )
            return
        self._write_json(HTTPStatus.NOT_FOUND, {"ok": False, "error": "not_found"})

    def log_message(self, fmt: str, *args) -> None:
        return

    def _write_json(self, status: HTTPStatus, payload: dict) -> None:
        raw = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


def _poll_loop() -> None:
    cfg = load_ingestor_config()
    db_cfg = load_db_config(cfg.postgres)
    while True:
        try:
            _ensure_runtime_schema(db_cfg)
            static_nodes = load_node_sources(cfg.node_sources_file).get("nodes", [])
            discovery_nodes: list[dict] = []
            discovery_error = ""
            try:
                discovery_nodes, discovery_error = _load_discovery_nodes(cfg.helper_timeout_sec)
            except HelperClientError as exc:
                discovery_error = str(exc)
            nodes = _merge_nodes(static_nodes if isinstance(static_nodes, list) else [], discovery_nodes)
            REALTIME_MANAGER.sync(db_cfg, nodes)
            node_index = {
                str(node.get("node_id", "")).strip(): node
                for node in nodes
                if str(node.get("node_id", "")).strip()
            }
            exec_sql(db_cfg, _disable_missing_nodes_sql(list(node_index.keys())))
            links_seen = 0
            for node in nodes:
                sql = _upsert_node_sql(node)
                if sql:
                    exec_sql(db_cfg, sql)
                try:
                    links_seen += _ingest_node_links(db_cfg, node, cfg.helper_timeout_sec)
                except HelperClientError as exc:
                    node_id = str(node.get("node_id", "")).strip()
                    if node_id:
                        exec_sql(db_cfg, _set_node_status_sql(node_id, "unknown"))
                        _open_node_unreachable_incident(db_cfg, node_id=node_id, detail=str(exc))
                    continue
            peer_links, peer_error = _load_control_peer_links(cfg.helper_timeout_sec)
            if peer_links:
                links_seen += _ingest_control_peer_links(db_cfg, peer_links)
            snapshot_ttl = max(int(_env("MONITORING_LINK_STALE_MAX_AGE_SEC", "180") or 180), 10)
            exec_sql(db_cfg, _mark_old_snapshots_stale_sql(snapshot_ttl))
            exec_sql(db_cfg, _stale_disabled_nodes_links_sql())
            stale_incident_ttl = max(int(_env("MONITORING_NODE_UNREACHABLE_AUTO_RESOLVE_AFTER_SEC", "1800") or 1800), 1)
            _resolve_old_node_unreachable_incidents(db_cfg, max_age_sec=stale_incident_ttl)
            link_incident_ttl = max(int(_env("MONITORING_AUTO_RESOLVE_LINK_ALERTS_AFTER_SEC", "900") or 900), 1)
            exec_sql(db_cfg, _resolve_inactive_link_incidents_sql(link_incident_ttl))
            _dispatch_alert_notifications(db_cfg)
            commands_dispatched = 0
            commands_failed = 0
            if cfg.command_dispatch_enabled:
                commands_dispatched, commands_failed = _dispatch_commands(
                    db_cfg,
                    node_index=node_index,
                    timeout_sec=cfg.command_dispatch_timeout_sec,
                    batch_size=cfg.command_dispatch_batch_size,
                    retry_max_attempts=cfg.command_retry_max_attempts,
                    retry_base_sec=cfg.command_retry_backoff_base_sec,
                    retry_max_sec=cfg.command_retry_backoff_max_sec,
                )
            err_parts = [txt for txt in (discovery_error, peer_error) if txt]
            if err_parts:
                _open_ingestion_gap_incident(db_cfg, detail="; ".join(err_parts))
            else:
                _resolve_ingestion_gap_incident(db_cfg)
            STATE.nodes_seen = len(nodes)
            STATE.links_seen = links_seen
            STATE.commands_dispatched = commands_dispatched
            STATE.commands_failed = commands_failed
            STATE.last_error = "; ".join(err_parts)
        except (Exception, DBError) as exc:  # pragma: no cover - defensive bootstrap code
            STATE.last_error = str(exc)
        STATE.last_run_at = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        time.sleep(max(cfg.poll_interval_sec, 5))


def main() -> None:
    cfg = load_ingestor_config()
    worker = threading.Thread(target=_poll_loop, daemon=True)
    worker.start()
    try:
        srv = ThreadingHTTPServer((cfg.http.host, cfg.http.port), Handler)
        print(f"monitoring-ingestor listening on {cfg.http.host}:{cfg.http.port}")
        srv.serve_forever()
    finally:
        REALTIME_MANAGER.stop_all()


if __name__ == "__main__":
    main()
