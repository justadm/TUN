-- Phase 1: subscriptions, internal balance, orders
-- Date: 2026-04-05

create table if not exists subscriptions (
  subscription_id uuid primary key default gen_random_uuid(),
  account_id uuid not null references accounts(account_id) on delete cascade,
  plan_code text not null,
  status text not null default 'trial',
  started_at timestamptz not null default now(),
  expires_at timestamptz,
  auto_renew boolean not null default false,
  profile_limit integer,
  metadata jsonb not null default '{}'::jsonb,
  check (status in ('active', 'trial', 'past_due', 'expired', 'cancelled'))
);

create index if not exists subscriptions_account_idx
  on subscriptions (account_id);

create index if not exists subscriptions_status_idx
  on subscriptions (status);

create table if not exists subscription_events (
  subscription_event_id bigserial primary key,
  subscription_id uuid not null references subscriptions(subscription_id) on delete cascade,
  account_id uuid not null references accounts(account_id) on delete cascade,
  event_type text not null,
  payload jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);

create index if not exists subscription_events_subscription_idx
  on subscription_events (subscription_id);

create table if not exists account_balances (
  account_id uuid primary key references accounts(account_id) on delete cascade,
  currency text not null default 'RUB',
  available_minor bigint not null default 0,
  reserved_minor bigint not null default 0,
  updated_at timestamptz not null default now()
);

create table if not exists account_balance_ledger (
  ledger_entry_id bigserial primary key,
  account_id uuid not null references accounts(account_id) on delete cascade,
  entry_type text not null,
  direction text not null,
  amount_minor bigint not null,
  currency text not null default 'RUB',
  reference_type text,
  reference_id text,
  connection_profile_id uuid references connection_profiles(connection_profile_id) on delete set null,
  note text,
  payload jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now(),
  created_by_account_id uuid references accounts(account_id) on delete set null,
  check (entry_type in ('top_up', 'service_charge', 'refund', 'bonus', 'manual_adjustment', 'correction')),
  check (direction in ('credit', 'debit'))
);

create index if not exists account_balance_ledger_account_idx
  on account_balance_ledger (account_id, created_at desc);

create index if not exists account_balance_ledger_profile_idx
  on account_balance_ledger (connection_profile_id);

create table if not exists orders (
  order_id uuid primary key default gen_random_uuid(),
  account_id uuid not null references accounts(account_id) on delete cascade,
  order_type text not null,
  status text not null default 'new',
  amount_minor bigint not null,
  currency text not null default 'RUB',
  provider text,
  provider_payment_id text,
  payment_url text,
  created_at timestamptz not null default now(),
  paid_at timestamptz,
  payload jsonb not null default '{}'::jsonb
);

create index if not exists orders_account_idx
  on orders (account_id);

create index if not exists orders_status_idx
  on orders (status);

create table if not exists payment_attempts (
  payment_attempt_id uuid primary key default gen_random_uuid(),
  order_id uuid not null references orders(order_id) on delete cascade,
  provider text not null,
  status text not null,
  payload jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);

create index if not exists payment_attempts_order_idx
  on payment_attempts (order_id);
