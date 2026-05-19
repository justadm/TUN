-- Monitoring subsystem read models
-- Date: 2026-04-10

create or replace view monitor_v_link_current as
select
    l.link_id,
    l.node_id,
    n.display_name as node_name,
    n.region as node_region,
    n.edge_id as node_edge_id,
    l.peer_node_id,
    l.gateway_id,
    l.role,
    l.transport_type,
    l.transport_addr,
    l.server_name,
    l.tun_name,
    l.desired_state,
    l.is_managed,
    s.observed_state,
    s.health_status,
    s.session_id,
    s.error_class,
    s.last_error,
    s.last_transition_at,
    s.last_handshake_at,
    s.last_rx_at,
    s.last_tx_at,
    s.rx_bytes,
    s.tx_bytes,
    s.reconnects,
    s.handshake_failures,
    s.gateway_id_selected,
    s.gateway_addr_selected,
    s.stale_after,
    s.observed_at,
    case
        when s.stale_after is not null and s.stale_after < now() then true
        else false
    end as is_stale,
    subj.account_id,
    subj.device_id,
    subj.connection_profile_id,
    subj.security_profile,
    subj.profile_revision,
    subj.profile_index_key,
    subj.profile_bundle_version,
    subj.helper_profile_id,
    subj.helper_security_profile,
    subj.helper_profile_revision,
    subj.helper_profile_source,
    subj.source as subject_source,
    c.incident_id as open_incident_id,
    i.kind as open_incident_kind,
    i.severity as open_incident_severity,
    i.status as open_incident_status
from monitor_links l
join monitor_nodes n on n.node_id = l.node_id
left join monitor_link_snapshots s on s.link_id = l.link_id
left join monitor_link_subjects subj on subj.link_id = l.link_id
left join lateral (
    select il.incident_id
    from monitor_incident_links il
    join monitor_incidents ii on ii.incident_id = il.incident_id
    where il.link_id = l.link_id
      and ii.status in ('open', 'acknowledged')
    order by ii.opened_at desc
    limit 1
) c on true
left join monitor_incidents i on i.incident_id = c.incident_id;

create or replace view monitor_v_fleet_summary as
with link_counts as (
    select
        count(*) as links_total,
        count(*) filter (where health_status = 'healthy') as healthy_links,
        count(*) filter (where health_status = 'degraded') as degraded_links,
        count(*) filter (where health_status = 'failed') as failed_links,
        count(*) filter (where health_status = 'stale') as stale_links,
        count(*) filter (where observed_state = 'established') as established_links,
        count(*) filter (where open_incident_id is not null) as links_with_incidents
    from monitor_v_link_current
),
node_counts as (
    select
        count(*) as nodes_total,
        count(*) filter (where status = 'active') as active_nodes,
        count(*) filter (where last_seen_at is not null and last_seen_at < now() - interval '2 minutes') as stale_nodes
    from monitor_nodes
),
incident_counts as (
    select
        count(*) filter (where status = 'open') as open_incidents,
        count(*) filter (where status = 'acknowledged') as acknowledged_incidents,
        count(*) filter (where severity = 'critical' and status in ('open', 'acknowledged')) as critical_incidents
    from monitor_incidents
),
command_counts as (
    select
        count(*) filter (where status in ('accepted', 'dispatched', 'acknowledged')) as in_flight_commands,
        count(*) filter (where status = 'failed' and accepted_at > now() - interval '24 hours') as failed_commands_24h
    from monitor_commands
)
select
    lc.links_total,
    lc.healthy_links,
    lc.degraded_links,
    lc.failed_links,
    lc.stale_links,
    lc.established_links,
    lc.links_with_incidents,
    nc.nodes_total,
    nc.active_nodes,
    nc.stale_nodes,
    ic.open_incidents,
    ic.acknowledged_incidents,
    ic.critical_incidents,
    cc.in_flight_commands,
    cc.failed_commands_24h,
    now() as generated_at
from link_counts lc
cross join node_counts nc
cross join incident_counts ic
cross join command_counts cc;

create or replace view monitor_v_gateway_summary as
select
    coalesce(nullif(gateway_id_selected, ''), nullif(gateway_id, ''), 'unknown') as gateway_id,
    count(*) as links_total,
    count(*) filter (where health_status = 'healthy') as healthy_links,
    count(*) filter (where health_status = 'degraded') as degraded_links,
    count(*) filter (where health_status = 'failed') as failed_links,
    count(*) filter (where is_stale) as stale_links,
    count(*) filter (where open_incident_id is not null) as links_with_incidents,
    max(observed_at) as last_observed_at
from monitor_v_link_current
group by coalesce(nullif(gateway_id_selected, ''), nullif(gateway_id, ''), 'unknown');

create or replace view monitor_v_node_summary as
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
left join monitor_v_link_current l on l.node_id = n.node_id
group by
    n.node_id,
    n.display_name,
    n.role,
    n.region,
    n.edge_id,
    n.status,
    n.last_seen_at;

create or replace view monitor_v_app_runtime_summary as
select
    subj.account_id,
    subj.device_id,
    subj.connection_profile_id,
    count(*) as links_total,
    count(*) filter (where cur.health_status = 'healthy') as healthy_links,
    count(*) filter (where cur.health_status = 'degraded') as degraded_links,
    count(*) filter (where cur.health_status = 'failed') as failed_links,
    count(*) filter (where cur.is_stale) as stale_links,
    max(cur.last_handshake_at) as last_handshake_at,
    max(cur.last_rx_at) as last_rx_at,
    max(cur.last_tx_at) as last_tx_at,
    sum(cur.rx_bytes) as total_rx_bytes,
    sum(cur.tx_bytes) as total_tx_bytes,
    max(coalesce(nullif(cur.gateway_id_selected, ''), nullif(cur.gateway_id, ''))) as current_gateway_id,
    bool_or(cur.health_status = 'healthy') as protection_healthy,
    bool_or(cur.observed_state = 'established') as protection_established
from monitor_link_subjects subj
join monitor_v_link_current cur on cur.link_id = subj.link_id
group by
    subj.account_id,
    subj.device_id,
    subj.connection_profile_id;

create or replace view monitor_v_incident_current as
select
    i.incident_id,
    i.kind,
    i.severity,
    i.status,
    i.title,
    i.summary,
    i.owner,
    i.metadata_json,
    i.opened_at,
    i.acknowledged_at,
    i.resolved_at,
    count(il.link_id) as links_total,
    coalesce(
        json_agg(il.link_id order by il.link_id) filter (where il.link_id is not null),
        '[]'::json
    ) as link_ids
from monitor_incidents i
left join monitor_incident_links il on il.incident_id = i.incident_id
group by
    i.incident_id,
    i.kind,
    i.severity,
    i.status,
    i.title,
    i.summary,
    i.owner,
    i.metadata_json,
    i.opened_at,
    i.acknowledged_at,
    i.resolved_at;

create or replace view monitor_v_command_audit as
select
    c.command_id,
    c.link_id,
    c.node_id,
    c.command_type,
    c.requested_by,
    c.request_source,
    c.idempotency_key,
    c.status,
    c.request_json,
    c.response_json,
    c.accepted_at,
    c.dispatched_at,
    c.finished_at,
    case
        when nullif(coalesce(c.response_json->'dispatch'->>'attempt', ''), '') is null then 0
        else (c.response_json->'dispatch'->>'attempt')::int
    end as dispatch_attempt,
    nullif(coalesce(c.response_json->'dispatch'->>'last_error', ''), '') as last_error,
    case
        when nullif(coalesce(c.response_json->'dispatch'->>'next_retry_at', ''), '') is null then null
        else (c.response_json->'dispatch'->>'next_retry_at')::timestamptz
    end as next_retry_at,
    case
        when c.status = 'accepted'
         and nullif(coalesce(c.response_json->'dispatch'->>'next_retry_at', ''), '') is not null
         and (c.response_json->'dispatch'->>'next_retry_at')::timestamptz > now()
        then true
        else false
    end as backoff_active
from monitor_commands c;
