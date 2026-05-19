-- Monitoring subsystem initial schema
-- Date: 2026-04-10

create extension if not exists pgcrypto;

create table if not exists monitor_nodes (
    node_id text primary key,
    display_name text not null default '',
    role text not null default 'runtime'
        check (role in ('runtime', 'gateway', 'edge', 'unknown')),
    region text not null default '',
    edge_id text not null default '',
    host text not null default '',
    helper_base_url text not null default '',
    helper_auth_ref text not null default '',
    status text not null default 'active'
        check (status in ('active', 'disabled', 'draining', 'unknown')),
    last_seen_at timestamptz,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create index if not exists monitor_nodes_edge_idx
    on monitor_nodes(edge_id);

create index if not exists monitor_nodes_status_idx
    on monitor_nodes(status);

create table if not exists monitor_links (
    link_id text primary key,
    node_id text not null references monitor_nodes(node_id) on delete cascade,
    peer_node_id text not null default '',
    gateway_id text not null default '',
    role text not null
        check (role in ('client', 'server', 'unknown')),
    transport_type text not null default '',
    transport_addr text not null default '',
    server_name text not null default '',
    tun_name text not null default '',
    desired_state text not null default 'up'
        check (desired_state in ('up', 'down', 'unknown')),
    is_managed boolean not null default true,
    metadata jsonb not null default '{}'::jsonb,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create index if not exists monitor_links_node_idx
    on monitor_links(node_id);

create index if not exists monitor_links_gateway_idx
    on monitor_links(gateway_id);

create index if not exists monitor_links_role_idx
    on monitor_links(role);

create table if not exists monitor_link_subjects (
    link_id text primary key references monitor_links(link_id) on delete cascade,
    account_id text not null default '',
    device_id text not null default '',
    connection_profile_id text not null default '',
    security_profile text not null default '',
    profile_revision text not null default '',
    profile_index_key text not null default '',
    profile_bundle_version text not null default '',
    helper_profile_id text not null default '',
    helper_security_profile text not null default '',
    helper_profile_revision text not null default '',
    helper_profile_source text not null default '',
    source text not null default 'unknown'
        check (source in ('jstun', 'runtime', 'manual', 'unknown')),
    observed_at timestamptz,
    metadata jsonb not null default '{}'::jsonb,
    updated_at timestamptz not null default now()
);

create index if not exists monitor_link_subjects_account_idx
    on monitor_link_subjects(account_id);

create index if not exists monitor_link_subjects_device_idx
    on monitor_link_subjects(device_id);

create index if not exists monitor_link_subjects_profile_idx
    on monitor_link_subjects(connection_profile_id);

create index if not exists monitor_link_subjects_profile_revision_idx
    on monitor_link_subjects(connection_profile_id, profile_revision);

create index if not exists monitor_link_subjects_profile_index_key_idx
    on monitor_link_subjects(profile_index_key);

create table if not exists monitor_link_snapshots (
    link_id text primary key references monitor_links(link_id) on delete cascade,
    observed_state text not null default 'unknown'
        check (observed_state in (
            'planned',
            'starting',
            'dialing',
            'handshaking',
            'established',
            'degraded',
            'failing_over',
            'draining',
            'stopping',
            'stopped',
            'failed',
            'orphaned',
            'unknown'
        )),
    health_status text not null default 'unknown'
        check (health_status in ('healthy', 'degraded', 'failed', 'draining', 'stale', 'down', 'unknown')),
    session_id text not null default '',
    error_class text not null default 'none',
    last_error text not null default '',
    last_transition_at timestamptz,
    last_handshake_at timestamptz,
    last_rx_at timestamptz,
    last_tx_at timestamptz,
    rx_bytes bigint not null default 0,
    tx_bytes bigint not null default 0,
    reconnects integer not null default 0,
    handshake_failures integer not null default 0,
    gateway_id_selected text not null default '',
    gateway_addr_selected text not null default '',
    stale_after timestamptz,
    snapshot_source text not null default 'helper'
        check (snapshot_source in ('helper', 'poll', 'sse', 'derived', 'unknown')),
    snapshot_version text not null default 'v1',
    observed_at timestamptz not null default now(),
    snapshot_json jsonb not null default '{}'::jsonb,
    updated_at timestamptz not null default now()
);

create index if not exists monitor_link_snapshots_health_idx
    on monitor_link_snapshots(health_status);

create index if not exists monitor_link_snapshots_state_idx
    on monitor_link_snapshots(observed_state);

create index if not exists monitor_link_snapshots_gateway_idx
    on monitor_link_snapshots(gateway_id_selected);

create index if not exists monitor_link_snapshots_stale_idx
    on monitor_link_snapshots(stale_after);

create table if not exists monitor_link_events (
    event_id uuid primary key default gen_random_uuid(),
    link_id text not null references monitor_links(link_id) on delete cascade,
    node_id text not null default '',
    session_id text not null default '',
    event_type text not null,
    state text not null default 'unknown',
    health_status text not null default 'unknown',
    error_class text not null default 'none',
    cause text not null default '',
    payload_json jsonb not null default '{}'::jsonb,
    observed_at timestamptz not null,
    ingested_at timestamptz not null default now()
);

create index if not exists monitor_link_events_link_time_idx
    on monitor_link_events(link_id, observed_at desc);

create index if not exists monitor_link_events_node_time_idx
    on monitor_link_events(node_id, observed_at desc);

create index if not exists monitor_link_events_type_time_idx
    on monitor_link_events(event_type, observed_at desc);

create table if not exists monitor_probes (
    probe_id uuid primary key default gen_random_uuid(),
    link_id text not null references monitor_links(link_id) on delete cascade,
    probe_type text not null
        check (probe_type in ('transport', 'control_keepalive', 'in_tunnel_echo', 'synthetic_route', 'passive')),
    status text not null
        check (status in ('ok', 'degraded', 'failed', 'timeout', 'unknown')),
    latency_ms integer,
    loss_pct numeric(5,2),
    details_json jsonb not null default '{}'::jsonb,
    observed_at timestamptz not null default now()
);

create index if not exists monitor_probes_link_time_idx
    on monitor_probes(link_id, observed_at desc);

create index if not exists monitor_probes_type_time_idx
    on monitor_probes(probe_type, observed_at desc);

create table if not exists monitor_commands (
    command_id uuid primary key default gen_random_uuid(),
    link_id text references monitor_links(link_id) on delete set null,
    node_id text not null default '',
    command_type text not null
        check (command_type in (
            'reconnect',
            'drain',
            'resume',
            'freeze_autoselect',
            'unfreeze_autoselect',
            'select_gateway',
            'export_diagnostics'
        )),
    requested_by text not null default '',
    request_source text not null default 'unknown'
        check (request_source in ('operator_ui', 'automation', 'api', 'unknown')),
    idempotency_key text not null default '',
    status text not null default 'accepted'
        check (status in ('accepted', 'dispatched', 'acknowledged', 'succeeded', 'failed', 'timed_out', 'canceled')),
    request_json jsonb not null default '{}'::jsonb,
    response_json jsonb not null default '{}'::jsonb,
    accepted_at timestamptz not null default now(),
    dispatched_at timestamptz,
    finished_at timestamptz
);

create index if not exists monitor_commands_link_idx
    on monitor_commands(link_id, accepted_at desc);

create index if not exists monitor_commands_status_idx
    on monitor_commands(status, accepted_at desc);

create unique index if not exists monitor_commands_idempotency_idx
    on monitor_commands(request_source, idempotency_key)
    where idempotency_key <> '';

create table if not exists monitor_incidents (
    incident_id uuid primary key default gen_random_uuid(),
    kind text not null
        check (kind in (
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
        )),
    severity text not null
        check (severity in ('info', 'warning', 'critical')),
    status text not null default 'open'
        check (status in ('open', 'acknowledged', 'resolved', 'suppressed')),
    title text not null,
    summary text not null default '',
    owner text not null default '',
    metadata_json jsonb not null default '{}'::jsonb,
    opened_at timestamptz not null default now(),
    acknowledged_at timestamptz,
    resolved_at timestamptz
);

create index if not exists monitor_incidents_status_idx
    on monitor_incidents(status, opened_at desc);

create index if not exists monitor_incidents_kind_idx
    on monitor_incidents(kind, opened_at desc);

create table if not exists monitor_incident_links (
    incident_id uuid not null references monitor_incidents(incident_id) on delete cascade,
    link_id text not null references monitor_links(link_id) on delete cascade,
    added_at timestamptz not null default now(),
    primary key (incident_id, link_id)
);

create index if not exists monitor_incident_links_link_idx
    on monitor_incident_links(link_id, added_at desc);

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
);

create index if not exists monitor_profile_inventory_profile_idx
    on monitor_profile_inventory(profile_id);
