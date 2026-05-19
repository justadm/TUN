-- Phase 1: accounts, identities, sessions
-- Date: 2026-04-05

create extension if not exists pgcrypto;

create table if not exists accounts (
  account_id uuid primary key default gen_random_uuid(),
  email text,
  phone text,
  display_name text,
  status text not null default 'active',
  locale text,
  timezone text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  last_login_at timestamptz,
  check (status in ('active', 'pending', 'blocked', 'deleted'))
);

create unique index if not exists accounts_email_idx
  on accounts (lower(email))
  where email is not null;

create unique index if not exists accounts_phone_idx
  on accounts (phone)
  where phone is not null;

create table if not exists account_identities (
  identity_id uuid primary key default gen_random_uuid(),
  account_id uuid not null references accounts(account_id) on delete cascade,
  provider text not null,
  subject text not null,
  email_snapshot text,
  phone_snapshot text,
  login_snapshot text,
  name_snapshot text,
  given_name text,
  family_name text,
  middle_name text,
  avatar_url text,
  locale text,
  email_verified boolean,
  profile_json jsonb,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (provider, subject)
);

create index if not exists account_identities_account_idx
  on account_identities (account_id);

create table if not exists account_sessions (
  session_id uuid primary key default gen_random_uuid(),
  account_id uuid not null references accounts(account_id) on delete cascade,
  session_token text not null,
  status text not null default 'active',
  ip inet,
  ua text,
  created_at timestamptz not null default now(),
  expires_at timestamptz not null,
  last_seen_at timestamptz,
  unique (session_token),
  check (status in ('active', 'revoked', 'expired'))
);

create index if not exists account_sessions_account_idx
  on account_sessions (account_id);

create index if not exists account_sessions_expires_idx
  on account_sessions (expires_at);
