-- Phase 1: account-owned connection profiles and claim bridge
-- Date: 2026-04-05

create table if not exists connection_profiles (
  connection_profile_id uuid primary key default gen_random_uuid(),
  account_id uuid not null references accounts(account_id) on delete cascade,
  legacy_peer_id text,
  display_label text,
  system_label text,
  admin_label text,
  admin_comment text,
  public_key text,
  allowed_ip cidr,
  status text not null default 'pending',
  created_at timestamptz not null default now(),
  expires_at timestamptz,
  connected_at timestamptz,
  last_handshake_at timestamptz,
  last_real_ip inet,
  last_real_geo text,
  current_edge_id text,
  current_uplink_id text,
  routing_policy_mode text,
  preferred_uplink_id text,
  effective_uplink_id text,
  failover_uplink_id text,
  runtime_source text,
  unique (legacy_peer_id),
  unique (public_key),
  check (status in ('pending', 'active', 'blocked', 'expired', 'removed'))
);

create index if not exists connection_profiles_account_idx
  on connection_profiles (account_id);

create index if not exists connection_profiles_status_idx
  on connection_profiles (status);

create index if not exists connection_profiles_legacy_peer_idx
  on connection_profiles (legacy_peer_id);

create table if not exists connection_profile_issuances (
  issuance_id uuid primary key default gen_random_uuid(),
  connection_profile_id uuid not null references connection_profiles(connection_profile_id) on delete cascade,
  issue_kind text not null,
  edge_id text,
  uplink_id text,
  artifact_label text,
  zip_name text,
  conf_name text,
  issued_by_account_id uuid references accounts(account_id) on delete set null,
  issued_by_admin boolean not null default false,
  created_at timestamptz not null default now(),
  expires_at timestamptz,
  activated_at timestamptz,
  check (issue_kind in ('create', 'reissue', 'migration', 'rollback'))
);

create index if not exists connection_profile_issuances_profile_idx
  on connection_profile_issuances (connection_profile_id);

create table if not exists profile_claims (
  profile_claim_id uuid primary key default gen_random_uuid(),
  legacy_peer_id text not null,
  account_id uuid not null references accounts(account_id) on delete cascade,
  claim_method text not null,
  claim_status text not null default 'pending',
  proof_payload jsonb,
  created_at timestamptz not null default now(),
  resolved_at timestamptz,
  resolved_by_account_id uuid references accounts(account_id) on delete set null,
  check (claim_method in ('wg_session', 'recovery_code', 'operator_attach')),
  check (claim_status in ('pending', 'confirmed', 'rejected'))
);

create index if not exists profile_claims_account_idx
  on profile_claims (account_id);

create index if not exists profile_claims_legacy_peer_idx
  on profile_claims (legacy_peer_id);
