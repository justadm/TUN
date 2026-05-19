from __future__ import annotations

import json
from datetime import datetime, timedelta, timezone
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

from services.common.config import load_api_config
from services.common.db import DBError, fetch_json_value, load_db_config, sql_str


COMMAND_TYPES = {
    "reconnect",
    "drain",
    "resume",
    "freeze_autoselect",
    "unfreeze_autoselect",
    "select_gateway",
    "export_diagnostics",
}

REQUEST_SOURCES = {"operator_ui", "automation", "api", "unknown"}


def _utc_today_start() -> datetime:
    now = datetime.now(timezone.utc)
    return datetime(now.year, now.month, now.day, tzinfo=timezone.utc)


def _parse_day_start_utc(raw: str) -> datetime:
    txt = str(raw or "").strip()
    if not txt:
        return _utc_today_start()
    try:
        parsed = datetime.strptime(txt, "%Y-%m-%d")
    except ValueError:
        return _utc_today_start()
    return datetime(parsed.year, parsed.month, parsed.day, tzinfo=timezone.utc)


def _iso_utc(dt: datetime) -> str:
    return dt.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _parse_iso_utc(raw: str, fallback: datetime) -> datetime:
    txt = str(raw or "").strip()
    if not txt:
        return fallback
    if txt.endswith("Z"):
        txt = txt[:-1] + "+00:00"
    try:
        dt = datetime.fromisoformat(txt)
    except ValueError:
        return fallback
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.astimezone(timezone.utc)


def _active_link_predicate(alias: str = "") -> str:
    _ = alias
    return "1=1"


def _visible_node_predicate(alias: str = "") -> str:
    pref = f"{alias}." if alias else ""
    return f"coalesce({pref}status, 'unknown') in ('active', 'unknown')"


def _summary_sql() -> str:
    active_pred = _active_link_predicate("c")
    node_pred = _visible_node_predicate("mn")
    return """
        with active_links as (
            select c.*
            from monitor_v_link_current c
            where {active_pred}
              and exists (
                select 1
                from monitor_nodes mn
                where mn.node_id = c.node_id
                  and {node_pred}
              )
        ),
        active_nodes as (
            select distinct node_id
            from active_links
        ),
        incident_scope as (
            select distinct i.incident_id, i.severity, i.status
            from monitor_incidents i
            left join monitor_incident_links il on il.incident_id = i.incident_id
            left join active_links al on al.link_id = il.link_id
            where i.status in ('open', 'acknowledged')
              and (il.link_id is null or al.link_id is not null)
        )
        select row_to_json(t)
        from (
            select
                (select count(*) from active_links) as links_total,
                (select count(*) from active_links where health_status = 'healthy') as healthy_links,
                (select count(*) from active_links where health_status = 'degraded') as degraded_links,
                (select count(*) from active_links where health_status = 'failed') as failed_links,
                (select count(*) from active_links where is_stale) as stale_links,
                (select count(*) from active_links where observed_state = 'established') as established_links,
                (select count(*) from active_links where open_incident_id is not null) as links_with_incidents,
                (select count(*) from monitor_nodes mn where {node_pred}) as nodes_total,
                (select count(*) from active_nodes) as active_nodes,
                (
                    select count(*)
                    from monitor_nodes
                    where status = 'active'
                      and last_seen_at is not null
                      and last_seen_at < now() - interval '2 minutes'
                ) as stale_nodes,
                (select count(*) from incident_scope where status = 'open') as open_incidents,
                (select count(*) from incident_scope where status = 'acknowledged') as acknowledged_incidents,
                (select count(*) from incident_scope where severity = 'critical') as critical_incidents,
                (
                    select count(*)
                    from monitor_commands
                    where status in ('accepted', 'dispatched', 'acknowledged')
                ) as in_flight_commands,
                (
                    select count(*)
                    from monitor_commands
                    where status = 'failed'
                      and accepted_at > now() - interval '24 hours'
                ) as failed_commands_24h,
                now() as generated_at
        ) t
    """.format(active_pred=active_pred, node_pred=node_pred)


def _nodes_sql() -> str:
    active_pred = _active_link_predicate("l")
    node_pred = _visible_node_predicate("n")
    return """
        select coalesce(json_agg(row_to_json(t) order by t.node_id), '[]'::json)
        from (
            select
                n.node_id,
                n.display_name,
                n.role,
                n.region,
                n.edge_id,
                n.status,
                n.last_seen_at,
                count(l.link_id) as links_total,
                count(l.link_id) filter (where l.health_status = 'healthy') as healthy_links,
                count(l.link_id) filter (where l.health_status = 'degraded') as degraded_links,
                count(l.link_id) filter (where l.health_status = 'failed') as failed_links,
                count(l.link_id) filter (where l.is_stale) as stale_links,
                count(l.link_id) filter (where l.open_incident_id is not null) as links_with_incidents
            from monitor_nodes n
            left join monitor_v_link_current l
              on l.node_id = n.node_id
             and {active_pred}
            where {node_pred}
            group by
                n.node_id,
                n.display_name,
                n.role,
                n.region,
                n.edge_id,
                n.status,
                n.last_seen_at
            having count(l.link_id) > 0
            order by n.node_id
        ) t
    """.format(active_pred=active_pred, node_pred=node_pred)


def _gateways_sql() -> str:
    active_pred = _active_link_predicate("c")
    node_pred = _visible_node_predicate("mn")
    return """
        select coalesce(json_agg(row_to_json(t) order by t.gateway_id), '[]'::json)
        from (
            select
                coalesce(nullif(c.gateway_id_selected, ''), nullif(c.gateway_id, ''), 'unknown') as gateway_id,
                count(*) as links_total,
                count(*) filter (where c.health_status = 'healthy') as healthy_links,
                count(*) filter (where c.health_status = 'degraded') as degraded_links,
                count(*) filter (where c.health_status = 'failed') as failed_links,
                count(*) filter (where c.is_stale) as stale_links,
                count(*) filter (where c.open_incident_id is not null) as links_with_incidents,
                max(c.observed_at) as last_observed_at
            from monitor_v_link_current c
            where {active_pred}
              and exists (
                select 1
                from monitor_nodes mn
                where mn.node_id = c.node_id
                  and {node_pred}
              )
            group by coalesce(nullif(c.gateway_id_selected, ''), nullif(c.gateway_id, ''), 'unknown')
            order by gateway_id
        ) t
    """.format(active_pred=active_pred, node_pred=node_pred)


def _links_sql(params: dict[str, list[str]]) -> str:
    clauses: list[str] = []
    node_pred = _visible_node_predicate("mn")
    clauses.append(f"exists (select 1 from monitor_nodes mn where mn.node_id = c.node_id and {node_pred})")
    include_inactive = (params.get("include_inactive") or [""])[0].strip().lower() in {"1", "true", "yes"}
    if not include_inactive:
        clauses.append(_active_link_predicate("c"))
    filters = (
        ("health", "health_status"),
        ("state", "observed_state"),
        ("node_id", "node_id"),
        ("gateway_id", "gateway_id_selected"),
        ("role", "role"),
        ("account_id", "account_id"),
        ("device_id", "device_id"),
        ("connection_profile_id", "connection_profile_id"),
        ("security_profile", "security_profile"),
        ("profile_revision", "profile_revision"),
        ("profile_index_key", "profile_index_key"),
    )
    for key, column in filters:
        raw = (params.get(key) or [""])[0].strip()
        if raw:
            clauses.append(f"{column} = {sql_str(raw)}")
    source = (params.get("source") or [""])[0].strip().lower()
    if source:
        clauses.append(f"coalesce(nullif(l.metadata->>'monitor_source', ''), 'helper') = {sql_str(source)}")
    stale = (params.get("stale") or [""])[0].strip().lower()
    if stale in {"1", "true", "yes"}:
        clauses.append("is_stale = true")
    elif stale in {"0", "false", "no"}:
        clauses.append("is_stale = false")
    q = (params.get("q") or [""])[0].strip()
    if q:
        qq = sql_str(f"%{q}%")
        clauses.append(
            "("
            f"link_id ilike {qq} or "
            f"node_id ilike {qq} or "
            f"coalesce(node_name, '') ilike {qq} or "
            f"coalesce(gateway_id_selected, gateway_id, '') ilike {qq} or "
            f"coalesce(tun_name, '') ilike {qq} or "
            f"coalesce(connection_profile_id, '') ilike {qq} or "
            f"coalesce(security_profile, '') ilike {qq}"
            ")"
        )
    where = ""
    if clauses:
        where = "where " + " and ".join(clauses)
    try:
        page = max(int((params.get("page") or ["1"])[0]), 1)
    except ValueError:
        page = 1
    try:
        per_page = min(max(int((params.get("per_page") or ["50"])[0]), 1), 200)
    except ValueError:
        per_page = 50
    offset = (page - 1) * per_page
    return f"""
        with filtered as (
            select
                c.*,
                coalesce(nullif(l.metadata->>'monitor_source', ''), 'helper') as monitor_source
            from monitor_v_link_current c
            left join monitor_links l on l.link_id = c.link_id
            {where}
        ),
        paged as (
            select *
            from filtered
            order by
                case health_status
                    when 'failed' then 1
                    when 'degraded' then 2
                    when 'stale' then 3
                    when 'healthy' then 4
                    else 5
                end,
                observed_at desc nulls last,
                link_id
            limit {per_page}
            offset {offset}
        )
        select json_build_object(
            'items', coalesce((select json_agg(row_to_json(paged)) from paged), '[]'::json),
            'page', {page},
            'per_page', {per_page},
            'total', (select count(*) from filtered)
        )
    """


def _link_detail_sql(link_id: str) -> str:
    return f"""
        with cur as (
            select row_to_json(t)
            from (
                select
                    c.*,
                    coalesce(nullif(l.metadata->>'monitor_source', ''), 'helper') as monitor_source
                from monitor_v_link_current c
                left join monitor_links l on l.link_id = c.link_id
                where c.link_id = {sql_str(link_id)}
                limit 1
            ) t
        ),
        ev as (
            select coalesce(json_agg(row_to_json(t) order by t.observed_at desc), '[]'::json)
            from (
                select
                    event_id,
                    session_id,
                    event_type,
                    state,
                    health_status,
                    error_class,
                    cause,
                    payload_json,
                    observed_at,
                    ingested_at
                from monitor_link_events
                where link_id = {sql_str(link_id)}
                order by observed_at desc
                limit 50
            ) t
        ),
        probes as (
            select coalesce(json_agg(row_to_json(t) order by t.observed_at desc), '[]'::json)
            from (
                select
                    probe_id,
                    probe_type,
                    status,
                    latency_ms,
                    loss_pct,
                    details_json,
                    observed_at
                from monitor_probes
                where link_id = {sql_str(link_id)}
                order by observed_at desc
                limit 20
            ) t
        ),
        cmds as (
            select coalesce(json_agg(row_to_json(t) order by t.accepted_at desc), '[]'::json)
            from (
                select
                    command_id,
                    command_type,
                    requested_by,
                    request_source,
                    status,
                    request_json,
                    response_json,
                    accepted_at,
                    dispatched_at,
                    finished_at
                from monitor_commands
                where link_id = {sql_str(link_id)}
                order by accepted_at desc
                limit 20
            ) t
        ),
        inc as (
            select coalesce(json_agg(row_to_json(t) order by t.opened_at desc), '[]'::json)
            from (
                select
                    i.incident_id,
                    i.kind,
                    i.severity,
                    i.status,
                    i.title,
                    i.summary,
                    i.metadata_json,
                    i.opened_at,
                    i.acknowledged_at,
                    i.resolved_at
                from monitor_incidents i
                join monitor_incident_links il on il.incident_id = i.incident_id
                where il.link_id = {sql_str(link_id)}
                order by i.opened_at desc
                limit 20
            ) t
        )
        select json_build_object(
            'current', (select * from cur),
            'recent_events', (select * from ev),
            'recent_probes', (select * from probes),
            'recent_commands', (select * from cmds),
            'incidents', (select * from inc)
        )
    """


def _incidents_sql(params: dict[str, list[str]]) -> str:
    clauses: list[str] = []
    filters = (
        ("status", "status"),
        ("kind", "kind"),
        ("severity", "severity"),
    )
    for key, column in filters:
        raw = (params.get(key) or [""])[0].strip()
        if raw:
            clauses.append(f"{column} = {sql_str(raw)}")
    raw_link_id = (params.get("link_id") or [""])[0].strip()
    if raw_link_id:
        clauses.append(
            "exists ("
            "select 1 "
            "from json_array_elements_text(link_ids) as x(link_id) "
            f"where x.link_id = {sql_str(raw_link_id)}"
            ")"
        )
    q = (params.get("q") or [""])[0].strip()
    if q:
        qq = sql_str(f"%{q}%")
        clauses.append(
            "("
            f"incident_id::text ilike {qq} or "
            f"title ilike {qq} or "
            f"summary ilike {qq} or "
            f"coalesce(metadata_json::text, '') ilike {qq}"
            ")"
        )
    where = ""
    if clauses:
        where = "where " + " and ".join(clauses)
    return f"""
        select coalesce(json_agg(row_to_json(t) order by t.opened_at desc), '[]'::json)
        from (
            select *
            from monitor_v_incident_current
            {where}
            order by
                case severity
                    when 'critical' then 1
                    when 'warning' then 2
                    else 3
                end,
                opened_at desc
        ) t
    """


def _alerts_sql(params: dict[str, list[str]]) -> str:
    clauses: list[str] = []
    status = (params.get("status") or ["open"])[0].strip()
    if status and status != "all":
        clauses.append(f"status = {sql_str(status)}")
    severity = (params.get("severity") or [""])[0].strip()
    if severity:
        clauses.append(f"severity = {sql_str(severity)}")
    kind = (params.get("kind") or [""])[0].strip()
    if kind:
        clauses.append(f"kind = {sql_str(kind)}")
    q = (params.get("q") or [""])[0].strip()
    if q:
        qq = sql_str(f"%{q}%")
        clauses.append(
            "("
            f"incident_id::text ilike {qq} or "
            f"title ilike {qq} or "
            f"summary ilike {qq} or "
            f"coalesce(metadata_json::text, '') ilike {qq}"
            ")"
        )
    try:
        limit = min(max(int((params.get("limit") or ["100"])[0]), 1), 500)
    except ValueError:
        limit = 100
    where = f"where {' and '.join(clauses)}" if clauses else ""
    return f"""
        select coalesce(json_agg(row_to_json(t) order by t.opened_at desc), '[]'::json)
        from (
            select
                incident_id,
                kind,
                severity,
                status,
                title,
                summary,
                owner,
                metadata_json,
                links_total,
                link_ids,
                opened_at,
                acknowledged_at,
                resolved_at
            from monitor_v_incident_current
            {where}
            order by
                case severity
                    when 'critical' then 1
                    when 'warning' then 2
                    else 3
                end,
                opened_at desc
            limit {limit}
        ) t
    """


def _report_incidents_sql(from_iso: str, to_iso: str) -> str:
    return f"""
        with bucket as (
            select *
            from monitor_incidents
            where opened_at >= {sql_str(from_iso)}::timestamptz
              and opened_at < {sql_str(to_iso)}::timestamptz
        )
        select json_build_object(
            'opened_total', count(*),
            'opened_critical', count(*) filter (where severity = 'critical'),
            'opened_warning', count(*) filter (where severity = 'warning'),
            'opened_info', count(*) filter (where severity = 'info'),
            'opened_node_unreachable', count(*) filter (where kind = 'node_unreachable'),
            'opened_link_failed', count(*) filter (where kind = 'link_failed'),
            'opened_link_flapping', count(*) filter (where kind = 'link_flapping'),
            'opened_high_risk_violation', count(*) filter (where kind = 'high_risk_violation'),
            'opened_profile_drift', count(*) filter (where kind = 'profile_drift'),
            'opened_startup_contract_failure', count(*) filter (where kind = 'startup_contract_failure'),
            'opened_link_without_profile', count(*) filter (where kind = 'link_without_profile'),
            'opened_gateway_flap_risk', count(*) filter (where kind = 'gateway_flap_risk')
        )
        from bucket
    """


def _report_incidents_resolved_sql(from_iso: str, to_iso: str) -> str:
    return f"""
        select json_build_object(
            'resolved_total', count(*),
            'resolved_critical', count(*) filter (where severity = 'critical'),
            'resolved_warning', count(*) filter (where severity = 'warning'),
            'resolved_info', count(*) filter (where severity = 'info')
        )
        from monitor_incidents
        where resolved_at is not null
          and resolved_at >= {sql_str(from_iso)}::timestamptz
          and resolved_at < {sql_str(to_iso)}::timestamptz
    """


def _report_commands_sql(from_iso: str, to_iso: str) -> str:
    return f"""
        with bucket as (
            select *
            from monitor_commands
            where accepted_at >= {sql_str(from_iso)}::timestamptz
              and accepted_at < {sql_str(to_iso)}::timestamptz
        )
        select json_build_object(
            'accepted_total', count(*),
            'succeeded_total', count(*) filter (where status = 'succeeded'),
            'failed_total', count(*) filter (where status = 'failed'),
            'inflight_total', count(*) filter (where status in ('accepted', 'dispatched', 'acknowledged')),
            'reconnect_total', count(*) filter (where command_type = 'reconnect'),
            'drain_total', count(*) filter (where command_type = 'drain'),
            'resume_total', count(*) filter (where command_type = 'resume'),
            'select_gateway_total', count(*) filter (where command_type = 'select_gateway')
        )
        from bucket
    """


def _report_events_sql(from_iso: str, to_iso: str) -> str:
    return f"""
        with bucket as (
            select *
            from monitor_link_events
            where observed_at >= {sql_str(from_iso)}::timestamptz
              and observed_at < {sql_str(to_iso)}::timestamptz
        )
        select json_build_object(
            'events_total', count(*),
            'discovered_total', count(*) filter (where event_type = 'link.discovered'),
            'session_rotations_total', count(*) filter (where event_type = 'link.session_changed'),
            'state_changes_total', count(*) filter (where event_type = 'link.state_changed'),
            'health_changes_total', count(*) filter (where event_type = 'link.health_changed')
        )
        from bucket
    """


def _report_top_nodes_sql(from_iso: str, to_iso: str) -> str:
    return f"""
        select coalesce(json_agg(row_to_json(t) order by t.incidents_total desc, t.node_id), '[]'::json)
        from (
            select
                l.node_id,
                count(distinct il.incident_id) as incidents_total
            from monitor_incident_links il
            join monitor_links l on l.link_id = il.link_id
            join monitor_incidents i on i.incident_id = il.incident_id
            where i.opened_at >= {sql_str(from_iso)}::timestamptz
              and i.opened_at < {sql_str(to_iso)}::timestamptz
            group by l.node_id
            order by incidents_total desc, l.node_id
            limit 10
        ) t
    """


def _report_top_links_sql(from_iso: str, to_iso: str) -> str:
    return f"""
        select coalesce(json_agg(row_to_json(t) order by t.incidents_total desc, t.link_id), '[]'::json)
        from (
            select
                il.link_id,
                count(*) as incidents_total
            from monitor_incident_links il
            join monitor_incidents i on i.incident_id = il.incident_id
            where i.opened_at >= {sql_str(from_iso)}::timestamptz
              and i.opened_at < {sql_str(to_iso)}::timestamptz
            group by il.link_id
            order by incidents_total desc, il.link_id
            limit 10
        ) t
    """


def _sre_problems_sql(params: dict[str, list[str]]) -> str:
    try:
        limit = min(max(int((params.get("limit") or ["100"])[0]), 1), 500)
    except ValueError:
        limit = 100
    active_pred = _active_link_predicate("c")
    return f"""
        with bad_links as (
            select
                c.link_id,
                c.node_id,
                c.node_name,
                c.gateway_id_selected,
                c.observed_state,
                c.health_status,
                c.last_error,
                c.observed_at,
                c.open_incident_id,
                c.open_incident_kind,
                c.open_incident_severity,
                coalesce(nullif(l.metadata->>'monitor_source', ''), 'helper') as monitor_source
            from monitor_v_link_current c
            left join monitor_links l on l.link_id = c.link_id
            where {active_pred}
              and exists (
                select 1
                from monitor_nodes mn
                where mn.node_id = c.node_id
                  and mn.status = 'active'
              )
              and (
                c.health_status in ('degraded', 'failed', 'stale')
                or c.open_incident_id is not null
                or c.observed_state in ('starting', 'failing_over', 'orphaned')
              )
            order by
                case c.health_status
                    when 'failed' then 1
                    when 'degraded' then 2
                    when 'stale' then 3
                    else 4
                end,
                c.observed_at desc nulls last,
                c.link_id
            limit {limit}
        ),
        bad_nodes as (
            select
                n.node_id,
                n.display_name,
                n.region,
                n.edge_id,
                count(bl.link_id) as bad_links_total,
                count(*) filter (where bl.health_status = 'failed') as failed_links,
                count(*) filter (where bl.health_status = 'degraded') as degraded_links,
                max(bl.observed_at) as last_observed_at
            from monitor_nodes n
            join bad_links bl on bl.node_id = n.node_id
            where n.status = 'active'
            group by n.node_id, n.display_name, n.region, n.edge_id
            order by bad_links_total desc, n.node_id
            limit {limit}
        ),
        active_link_ids as (
            select c.link_id
            from monitor_v_link_current c
            where {active_pred}
              and exists (
                select 1
                from monitor_nodes mn
                where mn.node_id = c.node_id
                  and mn.status = 'active'
              )
        ),
        open_alerts as (
            select
                mic.incident_id,
                mic.kind,
                mic.severity,
                mic.status,
                mic.title,
                mic.summary,
                mic.opened_at
            from monitor_v_incident_current mic
            left join monitor_incident_links il on il.incident_id = mic.incident_id
            left join active_link_ids al on al.link_id = il.link_id
            where mic.status in ('open', 'acknowledged')
              and (
                il.link_id is null
                or al.link_id is not null
              )
            group by
                mic.incident_id,
                mic.kind,
                mic.severity,
                mic.status,
                mic.title,
                mic.summary,
                mic.opened_at
            order by
                case mic.severity
                    when 'critical' then 1
                    when 'warning' then 2
                    else 3
                end,
                mic.opened_at desc
            limit {limit}
        )
        select json_build_object(
            'links', coalesce((select json_agg(row_to_json(bad_links)) from bad_links), '[]'::json),
            'nodes', coalesce((select json_agg(row_to_json(bad_nodes)) from bad_nodes), '[]'::json),
            'alerts', coalesce((select json_agg(row_to_json(open_alerts)) from open_alerts), '[]'::json)
        )
    """.format(active_pred=active_pred)


def _build_report_payload(db_cfg, *, from_dt: datetime, to_dt: datetime, report_type: str, label: str) -> dict:
    from_iso = _iso_utc(from_dt)
    to_iso = _iso_utc(to_dt)
    fleet = fetch_json_value(db_cfg, _summary_sql(), default={}) or {}
    incidents_opened = fetch_json_value(db_cfg, _report_incidents_sql(from_iso, to_iso), default={}) or {}
    incidents_resolved = fetch_json_value(db_cfg, _report_incidents_resolved_sql(from_iso, to_iso), default={}) or {}
    commands = fetch_json_value(db_cfg, _report_commands_sql(from_iso, to_iso), default={}) or {}
    events = fetch_json_value(db_cfg, _report_events_sql(from_iso, to_iso), default={}) or {}
    top_nodes = fetch_json_value(db_cfg, _report_top_nodes_sql(from_iso, to_iso), default=[]) or []
    top_links = fetch_json_value(db_cfg, _report_top_links_sql(from_iso, to_iso), default=[]) or []
    return {
        "ok": True,
        "report": {
            "type": report_type,
            "label": label,
            "range": {"from": from_iso, "to": to_iso},
            "fleet_now": fleet,
            "incidents": {**incidents_opened, **incidents_resolved},
            "commands": commands,
            "events": events,
            "top_nodes": top_nodes,
            "top_links": top_links,
        },
    }


def _incident_detail_sql(incident_id: str) -> str:
    return f"""
        with cur as (
            select row_to_json(t)
            from (
                select *
                from monitor_v_incident_current
                where incident_id::text = {sql_str(incident_id)}
                limit 1
            ) t
        ),
        links as (
            select coalesce(json_agg(row_to_json(t) order by t.link_id), '[]'::json)
            from (
                select c.*
                from monitor_v_link_current c
                join monitor_incident_links il on il.link_id = c.link_id
                where il.incident_id::text = {sql_str(incident_id)}
            ) t
        )
        select json_build_object(
            'current', (select * from cur),
            'links', (select * from links)
        )
    """


def _app_summary_sql(params: dict[str, list[str]]) -> str:
    clauses: list[str] = []
    filters = (
        ("account_id", "account_id"),
        ("device_id", "device_id"),
        ("connection_profile_id", "connection_profile_id"),
    )
    for key, column in filters:
        raw = (params.get(key) or [""])[0].strip()
        if raw:
            clauses.append(f"{column} = {sql_str(raw)}")
    where = ""
    if clauses:
        where = "where " + " and ".join(clauses)
    return f"""
        select coalesce(json_agg(row_to_json(t) order by t.device_id, t.connection_profile_id), '[]'::json)
        from (
            select *
            from monitor_v_app_runtime_summary
            {where}
            order by device_id, connection_profile_id
        ) t
    """


def _app_profile_detail_sql(connection_profile_id: str, params: dict[str, list[str]]) -> str:
    clauses = [f"connection_profile_id = {sql_str(connection_profile_id)}"]
    account_id = (params.get("account_id") or [""])[0].strip()
    if account_id:
        clauses.append(f"account_id = {sql_str(account_id)}")
    device_id = (params.get("device_id") or [""])[0].strip()
    if device_id:
        clauses.append(f"device_id = {sql_str(device_id)}")
    where = "where " + " and ".join(clauses)
    return f"""
        with profile as (
            select row_to_json(t)
            from (
                select *
                from monitor_v_app_runtime_summary
                {where}
                limit 1
            ) t
        ),
        links as (
            select coalesce(json_agg(row_to_json(t) order by t.tun_name, t.link_id), '[]'::json)
            from (
                select
                    link_id,
                    device_id,
                    connection_profile_id,
                    tun_name,
                    desired_state,
                    observed_state,
                    health_status,
                    last_error,
                    last_handshake_at,
                    last_rx_at,
                    last_tx_at,
                    rx_bytes,
                    tx_bytes,
                    coalesce(nullif(gateway_id_selected, ''), nullif(gateway_id, '')) as current_gateway_id,
                    case
                        when health_status = 'healthy' then 'protected'
                        when health_status in ('degraded', 'draining') then 'degraded'
                        when health_status in ('failed', 'down') then 'disconnected'
                        when is_stale then 'stale'
                        else 'unknown'
                    end as protection_status
                from monitor_v_link_current
                {where}
            ) t
        )
        select json_build_object(
            'profile', (select * from profile),
            'links', (select * from links)
        )
    """


def _commands_sql(params: dict[str, list[str]]) -> str:
    clauses: list[str] = []
    filters = (
        ("status", "status"),
        ("command_type", "command_type"),
        ("request_source", "request_source"),
        ("node_id", "node_id"),
        ("link_id", "link_id"),
    )
    for key, column in filters:
        raw = (params.get(key) or [""])[0].strip()
        if raw:
            clauses.append(f"{column} = {sql_str(raw)}")
    q = (params.get("q") or [""])[0].strip()
    if q:
        qq = sql_str(f"%{q}%")
        clauses.append(
            "("
            f"command_id::text ilike {qq} or "
            f"coalesce(requested_by, '') ilike {qq} or "
            f"coalesce(request_json::text, '') ilike {qq}"
            ")"
        )
    where = "where " + " and ".join(clauses) if clauses else ""
    try:
        page = max(int((params.get("page") or ["1"])[0]), 1)
    except ValueError:
        page = 1
    try:
        per_page = min(max(int((params.get("per_page") or ["50"])[0]), 1), 200)
    except ValueError:
        per_page = 50
    offset = (page - 1) * per_page
    return f"""
        with filtered as (
            select *
            from monitor_commands
            {where}
        ),
        paged as (
            select *
            from filtered
            order by accepted_at desc
            limit {per_page}
            offset {offset}
        )
        select json_build_object(
            'items', coalesce((select json_agg(row_to_json(paged)) from paged), '[]'::json),
            'page', {page},
            'per_page', {per_page},
            'total', (select count(*) from filtered)
        )
    """


def _commands_audit_sql(params: dict[str, list[str]]) -> str:
    clauses: list[str] = []
    filters = (
        ("status", "status"),
        ("command_type", "command_type"),
        ("request_source", "request_source"),
        ("node_id", "node_id"),
        ("link_id", "link_id"),
    )
    for key, column in filters:
        raw = (params.get(key) or [""])[0].strip()
        if raw:
            clauses.append(f"{column} = {sql_str(raw)}")
    backoff = (params.get("backoff_active") or [""])[0].strip().lower()
    if backoff in {"1", "true", "yes"}:
        clauses.append("backoff_active = true")
    q = (params.get("q") or [""])[0].strip()
    if q:
        qq = sql_str(f"%{q}%")
        clauses.append(
            "("
            f"command_id::text ilike {qq} or "
            f"coalesce(requested_by, '') ilike {qq} or "
            f"coalesce(last_error, '') ilike {qq} or "
            f"coalesce(request_json::text, '') ilike {qq}"
            ")"
        )
    where = "where " + " and ".join(clauses) if clauses else ""
    try:
        page = max(int((params.get("page") or ["1"])[0]), 1)
    except ValueError:
        page = 1
    try:
        per_page = min(max(int((params.get("per_page") or ["50"])[0]), 1), 200)
    except ValueError:
        per_page = 50
    offset = (page - 1) * per_page
    return f"""
        with filtered as (
            select *
            from monitor_v_command_audit
            {where}
        ),
        paged as (
            select *
            from filtered
            order by accepted_at desc
            limit {per_page}
            offset {offset}
        )
        select json_build_object(
            'items', coalesce((select json_agg(row_to_json(paged)) from paged), '[]'::json),
            'page', {page},
            'per_page', {per_page},
            'total', (select count(*) from filtered)
        )
    """


def _command_detail_sql(command_id: str) -> str:
    return f"""
        select row_to_json(t)
        from (
            select *
            from monitor_commands
            where command_id::text = {sql_str(command_id)}
            limit 1
        ) t
    """


def _resolve_command_target_sql(target_type: str, target_id: str) -> str:
    if target_type == "link":
        return f"""
            select row_to_json(t)
            from (
                select
                    l.link_id,
                    l.node_id
                from monitor_links l
                where l.link_id = {sql_str(target_id)}
                limit 1
            ) t
        """
    if target_type == "node":
        return f"""
            select row_to_json(t)
            from (
                select
                    ''::text as link_id,
                    n.node_id
                from monitor_nodes n
                where n.node_id = {sql_str(target_id)}
                limit 1
            ) t
        """
    return "select NULL::json"


def _create_command_sql(
    *,
    command_type: str,
    target_type: str,
    target_id: str,
    node_id: str,
    link_id: str,
    requested_by: str,
    request_source: str,
    idempotency_key: str,
    payload: dict,
) -> str:
    link_id_sql = "NULL" if not link_id else sql_str(link_id)
    return f"""
        with ins as (
            insert into monitor_commands (
                link_id,
                node_id,
                command_type,
                requested_by,
                request_source,
                idempotency_key,
                status,
                request_json,
                response_json,
                accepted_at
            ) values (
                {link_id_sql},
                {sql_str(node_id)},
                {sql_str(command_type)},
                {sql_str(requested_by)},
                {sql_str(request_source)},
                {sql_str(idempotency_key)},
                'accepted',
                {sql_str(json.dumps(payload, ensure_ascii=False, sort_keys=True))}::jsonb,
                '{{}}'::jsonb,
                now()
            )
            on conflict (request_source, idempotency_key)
            where idempotency_key <> ''
            do update set
                request_json = monitor_commands.request_json
            returning *
        )
        select row_to_json(ins) from ins
    """


class Handler(BaseHTTPRequestHandler):
    server_version = "tun-monitoring-api/0.1"

    def do_GET(self) -> None:
        parsed = urlparse(self.path)
        if parsed.path == "/healthz":
            self._write_json(HTTPStatus.OK, {"ok": True, "service": "monitoring-api"})
            return
        try:
            if parsed.path == "/v1/monitor/summary":
                summary = fetch_json_value(self.server.db_cfg, _summary_sql(), default={})
                self._write_json(HTTPStatus.OK, {"ok": True, "summary": summary or {}})
                return
            if parsed.path == "/v1/monitor/nodes":
                nodes = fetch_json_value(self.server.db_cfg, _nodes_sql(), default=[])
                self._write_json(HTTPStatus.OK, {"ok": True, "items": nodes or []})
                return
            if parsed.path == "/v1/monitor/gateways":
                gateways = fetch_json_value(self.server.db_cfg, _gateways_sql(), default=[])
                self._write_json(HTTPStatus.OK, {"ok": True, "items": gateways or []})
                return
            if parsed.path == "/v1/monitor/links":
                payload = fetch_json_value(self.server.db_cfg, _links_sql(parse_qs(parsed.query)), default={})
                self._write_json(HTTPStatus.OK, {"ok": True, **(payload or {"items": [], "page": 1, "per_page": 50, "total": 0})})
                return
            if parsed.path == "/v1/monitor/incidents":
                incidents = fetch_json_value(self.server.db_cfg, _incidents_sql(parse_qs(parsed.query)), default=[])
                self._write_json(HTTPStatus.OK, {"ok": True, "items": incidents or []})
                return
            if parsed.path == "/v1/monitor/alerts":
                alerts = fetch_json_value(self.server.db_cfg, _alerts_sql(parse_qs(parsed.query)), default=[])
                self._write_json(HTTPStatus.OK, {"ok": True, "items": alerts or []})
                return
            if parsed.path == "/v1/monitor/sre/problems":
                payload = fetch_json_value(self.server.db_cfg, _sre_problems_sql(parse_qs(parsed.query)), default={}) or {}
                self._write_json(
                    HTTPStatus.OK,
                    {
                        "ok": True,
                        "links": payload.get("links", []) or [],
                        "nodes": payload.get("nodes", []) or [],
                        "alerts": payload.get("alerts", []) or [],
                    },
                )
                return
            if parsed.path == "/v1/monitor/reports/daily":
                qs = parse_qs(parsed.query)
                day_start = _parse_day_start_utc((qs.get("day") or [""])[0])
                day_end = day_start + timedelta(days=1)
                out = _build_report_payload(
                    self.server.db_cfg,
                    from_dt=day_start,
                    to_dt=day_end,
                    report_type="daily",
                    label=day_start.strftime("%Y-%m-%d"),
                )
                out["report"]["day"] = day_start.strftime("%Y-%m-%d")
                self._write_json(HTTPStatus.OK, out)
                return
            if parsed.path == "/v1/monitor/reports/24h":
                to_dt = datetime.now(timezone.utc)
                from_dt = to_dt - timedelta(hours=24)
                self._write_json(
                    HTTPStatus.OK,
                    _build_report_payload(
                        self.server.db_cfg,
                        from_dt=from_dt,
                        to_dt=to_dt,
                        report_type="rolling",
                        label="24h",
                    ),
                )
                return
            if parsed.path == "/v1/monitor/reports/7d":
                to_dt = datetime.now(timezone.utc)
                from_dt = to_dt - timedelta(days=7)
                self._write_json(
                    HTTPStatus.OK,
                    _build_report_payload(
                        self.server.db_cfg,
                        from_dt=from_dt,
                        to_dt=to_dt,
                        report_type="rolling",
                        label="7d",
                    ),
                )
                return
            if parsed.path == "/v1/monitor/reports/range":
                qs = parse_qs(parsed.query)
                now_dt = datetime.now(timezone.utc)
                to_dt = _parse_iso_utc((qs.get("to") or [""])[0], now_dt)
                from_dt = _parse_iso_utc((qs.get("from") or [""])[0], to_dt - timedelta(hours=24))
                if from_dt >= to_dt:
                    self._write_json(HTTPStatus.BAD_REQUEST, {"ok": False, "error": "invalid_range"})
                    return
                self._write_json(
                    HTTPStatus.OK,
                    _build_report_payload(
                        self.server.db_cfg,
                        from_dt=from_dt,
                        to_dt=to_dt,
                        report_type="custom",
                        label="range",
                    ),
                )
                return
            if parsed.path == "/v1/monitor/commands":
                payload = fetch_json_value(self.server.db_cfg, _commands_sql(parse_qs(parsed.query)), default={})
                self._write_json(HTTPStatus.OK, {"ok": True, **(payload or {"items": [], "page": 1, "per_page": 50, "total": 0})})
                return
            if parsed.path == "/v1/monitor/commands/audit":
                payload = fetch_json_value(self.server.db_cfg, _commands_audit_sql(parse_qs(parsed.query)), default={})
                self._write_json(HTTPStatus.OK, {"ok": True, **(payload or {"items": [], "page": 1, "per_page": 50, "total": 0})})
                return
            if parsed.path in {"/v1/app/runtime/summary", "/v1/app/runtime/profiles"}:
                profiles = fetch_json_value(self.server.db_cfg, _app_summary_sql(parse_qs(parsed.query)), default=[])
                self._write_json(HTTPStatus.OK, {"ok": True, "profiles": profiles or []})
                return
            if parsed.path.startswith("/v1/monitor/links/"):
                link_id = parsed.path.split("/v1/monitor/links/", 1)[1].strip()
                if link_id:
                    payload = fetch_json_value(self.server.db_cfg, _link_detail_sql(link_id), default={})
                    current = (payload or {}).get("current")
                    if not current:
                        self._write_json(HTTPStatus.NOT_FOUND, {"ok": False, "error": "link_not_found"})
                        return
                    self._write_json(HTTPStatus.OK, {"ok": True, "link": payload})
                    return
            if parsed.path.startswith("/v1/monitor/incidents/"):
                incident_id = parsed.path.split("/v1/monitor/incidents/", 1)[1].strip()
                if incident_id:
                    payload = fetch_json_value(self.server.db_cfg, _incident_detail_sql(incident_id), default={})
                    current = (payload or {}).get("current")
                    if not current:
                        self._write_json(HTTPStatus.NOT_FOUND, {"ok": False, "error": "incident_not_found"})
                        return
                    self._write_json(HTTPStatus.OK, {"ok": True, "incident": payload})
                    return
            if parsed.path.startswith("/v1/app/runtime/profiles/"):
                connection_profile_id = parsed.path.split("/v1/app/runtime/profiles/", 1)[1].strip()
                if connection_profile_id:
                    payload = fetch_json_value(
                        self.server.db_cfg,
                        _app_profile_detail_sql(connection_profile_id, parse_qs(parsed.query)),
                        default={},
                    )
                    profile = (payload or {}).get("profile")
                    if not profile:
                        self._write_json(HTTPStatus.NOT_FOUND, {"ok": False, "error": "profile_not_found"})
                        return
                    self._write_json(HTTPStatus.OK, {"ok": True, "runtime": payload})
                    return
            if parsed.path.startswith("/v1/monitor/commands/"):
                command_id = parsed.path.split("/v1/monitor/commands/", 1)[1].strip()
                if command_id:
                    command = fetch_json_value(self.server.db_cfg, _command_detail_sql(command_id), default={})
                    if not command:
                        self._write_json(HTTPStatus.NOT_FOUND, {"ok": False, "error": "command_not_found"})
                        return
                    self._write_json(HTTPStatus.OK, {"ok": True, "command": command})
                    return
        except DBError as exc:
            self._write_json(HTTPStatus.SERVICE_UNAVAILABLE, {"ok": False, "error": "db_unavailable", "detail": str(exc)})
            return
        self._write_json(HTTPStatus.NOT_FOUND, {"ok": False, "error": "not_found"})

    def do_POST(self) -> None:
        parsed = urlparse(self.path)
        if parsed.path != "/v1/monitor/commands":
            self._write_json(HTTPStatus.NOT_FOUND, {"ok": False, "error": "not_found"})
            return
        try:
            req = self._read_json_body()
        except ValueError as exc:
            self._write_json(HTTPStatus.BAD_REQUEST, {"ok": False, "error": "invalid_json", "detail": str(exc)})
            return
        target_type = str(req.get("target_type", "")).strip().lower()
        target_id = str(req.get("target_id", "")).strip()
        command_type = str(req.get("command_type", "")).strip()
        requested_by = str(req.get("requested_by", "monitoring-api")).strip() or "monitoring-api"
        request_source = str(req.get("request_source", "api")).strip() or "api"
        idempotency_key = str(req.get("idempotency_key", "")).strip()
        reason = str(req.get("reason", "")).strip()
        args = req.get("args")
        if not isinstance(args, dict):
            args = {}
        if target_type not in {"link", "node"}:
            self._write_json(HTTPStatus.BAD_REQUEST, {"ok": False, "error": "invalid_target_type"})
            return
        if not target_id:
            self._write_json(HTTPStatus.BAD_REQUEST, {"ok": False, "error": "target_id_required"})
            return
        if command_type not in COMMAND_TYPES:
            self._write_json(HTTPStatus.BAD_REQUEST, {"ok": False, "error": "invalid_command_type"})
            return
        if request_source not in REQUEST_SOURCES:
            self._write_json(HTTPStatus.BAD_REQUEST, {"ok": False, "error": "invalid_request_source"})
            return
        if target_type == "node" and command_type in {"reconnect", "drain", "resume", "select_gateway"}:
            self._write_json(
                HTTPStatus.BAD_REQUEST,
                {"ok": False, "error": "target_mismatch", "detail": "link target required for this command"},
            )
            return
        if command_type == "select_gateway":
            gateway_id = str(args.get("gateway_id", "")).strip()
            if not gateway_id:
                self._write_json(
                    HTTPStatus.BAD_REQUEST,
                    {"ok": False, "error": "gateway_id_required", "detail": "args.gateway_id is required"},
                )
                return
        try:
            resolved = fetch_json_value(
                self.server.db_cfg,
                _resolve_command_target_sql(target_type, target_id),
                default={},
            )
            if not resolved:
                self._write_json(HTTPStatus.NOT_FOUND, {"ok": False, "error": "target_not_found"})
                return
            link_id = str(resolved.get("link_id", "") or "").strip()
            node_id = str(resolved.get("node_id", "") or "").strip()
            request_payload = {
                "target_type": target_type,
                "target_id": target_id,
                "reason": reason,
                "args": args,
            }
            command = fetch_json_value(
                self.server.db_cfg,
                _create_command_sql(
                    command_type=command_type,
                    target_type=target_type,
                    target_id=target_id,
                    node_id=node_id,
                    link_id=link_id,
                    requested_by=requested_by,
                    request_source=request_source,
                    idempotency_key=idempotency_key,
                    payload=request_payload,
                ),
                default={},
            )
            if not command:
                self._write_json(HTTPStatus.INTERNAL_SERVER_ERROR, {"ok": False, "error": "command_create_failed"})
                return
            self._write_json(HTTPStatus.ACCEPTED, {"ok": True, "command": command})
            return
        except DBError as exc:
            self._write_json(HTTPStatus.SERVICE_UNAVAILABLE, {"ok": False, "error": "db_unavailable", "detail": str(exc)})
            return

    def log_message(self, fmt: str, *args) -> None:
        return

    def _read_json_body(self) -> dict:
        length_raw = self.headers.get("Content-Length", "0").strip() or "0"
        try:
            length = int(length_raw)
        except ValueError as exc:
            raise ValueError("invalid Content-Length") from exc
        raw = self.rfile.read(length) if length > 0 else b"{}"
        try:
            payload = json.loads(raw.decode("utf-8", "replace") or "{}")
        except json.JSONDecodeError as exc:
            raise ValueError(str(exc)) from exc
        if not isinstance(payload, dict):
            raise ValueError("request body must be a JSON object")
        return payload

    def _write_json(self, status: HTTPStatus, payload: dict) -> None:
        raw = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


def main() -> None:
    cfg = load_api_config()
    srv = ThreadingHTTPServer((cfg.http.host, cfg.http.port), Handler)
    srv.db_cfg = load_db_config(cfg.postgres)
    print(f"monitoring-api listening on {cfg.http.host}:{cfg.http.port}")
    srv.serve_forever()


if __name__ == "__main__":
    main()
