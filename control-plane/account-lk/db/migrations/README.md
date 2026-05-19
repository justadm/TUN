# Account-LK Migrations

These migrations define the first ownership and identity layer for the new account-based LK.

Suggested apply order:

1. `001_accounts_auth.sql`
2. `002_connection_profiles.sql`
3. `003_subscriptions_balance.sql`

These migrations are intentionally separate from the current control-plane runtime schema.
