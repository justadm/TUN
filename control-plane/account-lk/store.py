from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from decimal import Decimal
from typing import Any, Protocol


@dataclass(slots=True)
class Account:
    account_id: str
    email: str = ""
    phone: str = ""
    display_name: str = ""
    status: str = "active"
    locale: str = ""
    timezone: str = ""
    created_at: datetime | None = None
    updated_at: datetime | None = None
    last_login_at: datetime | None = None


@dataclass(slots=True)
class AccountIdentity:
    identity_id: str
    account_id: str
    provider: str
    subject: str
    email_snapshot: str = ""
    phone_snapshot: str = ""
    login_snapshot: str = ""
    name_snapshot: str = ""
    given_name: str = ""
    family_name: str = ""
    middle_name: str = ""
    avatar_url: str = ""
    locale: str = ""
    email_verified: bool | None = None
    profile_json: dict[str, Any] = field(default_factory=dict)
    created_at: datetime | None = None
    updated_at: datetime | None = None


@dataclass(slots=True)
class AccountSession:
    session_id: str
    account_id: str
    session_token: str
    status: str = "active"
    ip: str = ""
    ua: str = ""
    created_at: datetime | None = None
    expires_at: datetime | None = None
    last_seen_at: datetime | None = None


@dataclass(slots=True)
class ConnectionProfile:
    connection_profile_id: str
    account_id: str
    legacy_peer_id: str = ""
    display_label: str = ""
    system_label: str = ""
    admin_label: str = ""
    admin_comment: str = ""
    public_key: str = ""
    allowed_ip: str = ""
    status: str = "pending"
    created_at: datetime | None = None
    expires_at: datetime | None = None
    connected_at: datetime | None = None
    last_handshake_at: datetime | None = None
    last_real_ip: str = ""
    last_real_geo: str = ""
    current_edge_id: str = ""
    current_uplink_id: str = ""
    routing_policy_mode: str = ""
    preferred_uplink_id: str = ""
    effective_uplink_id: str = ""
    failover_uplink_id: str = ""
    runtime_source: str = ""


@dataclass(slots=True)
class ProfileClaim:
    profile_claim_id: str
    legacy_peer_id: str
    account_id: str
    claim_method: str
    claim_status: str = "pending"
    proof_payload: dict[str, Any] = field(default_factory=dict)
    created_at: datetime | None = None
    resolved_at: datetime | None = None
    resolved_by_account_id: str = ""


@dataclass(slots=True)
class Subscription:
    subscription_id: str
    account_id: str
    plan_code: str
    status: str = "trial"
    started_at: datetime | None = None
    expires_at: datetime | None = None
    auto_renew: bool = False
    profile_limit: int | None = None
    metadata: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class AccountBalance:
    account_id: str
    currency: str = "RUB"
    available_minor: int = 0
    reserved_minor: int = 0
    updated_at: datetime | None = None


@dataclass(slots=True)
class BalanceLedgerEntry:
    ledger_entry_id: int | None
    account_id: str
    entry_type: str
    direction: str
    amount_minor: int
    currency: str = "RUB"
    reference_type: str = ""
    reference_id: str = ""
    connection_profile_id: str = ""
    note: str = ""
    payload: dict[str, Any] = field(default_factory=dict)
    created_at: datetime | None = None
    created_by_account_id: str = ""


class AccountLKStore(Protocol):
    # Accounts
    def create_account(self, *, email: str = "", phone: str = "", display_name: str = "") -> Account: ...
    def get_account(self, account_id: str) -> Account | None: ...
    def get_account_by_email(self, email: str) -> Account | None: ...
    def get_account_by_phone(self, phone: str) -> Account | None: ...
    def update_account_profile(
        self,
        account_id: str,
        *,
        display_name: str | None = None,
        locale: str | None = None,
        timezone: str | None = None,
        status: str | None = None,
    ) -> Account: ...

    # Identities
    def get_account_by_identity(self, provider: str, subject: str) -> Account | None: ...
    def upsert_account_identity(self, identity: AccountIdentity) -> AccountIdentity: ...
    def list_account_identities(self, account_id: str) -> list[AccountIdentity]: ...
    def delete_account_identity(self, account_id: str, provider: str, subject: str) -> None: ...

    # Sessions
    def create_account_session(self, account_id: str, *, ttl_seconds: int, ip: str = "", ua: str = "") -> AccountSession: ...
    def get_account_by_session_token(self, session_token: str) -> Account | None: ...
    def revoke_account_session(self, session_id: str) -> None: ...
    def touch_account_session(self, session_id: str) -> None: ...

    # Connection profiles
    def create_connection_profile(self, profile: ConnectionProfile) -> ConnectionProfile: ...
    def get_connection_profile(self, connection_profile_id: str) -> ConnectionProfile | None: ...
    def get_connection_profile_by_legacy_peer_id(self, legacy_peer_id: str) -> ConnectionProfile | None: ...
    def list_connection_profiles_by_account(self, account_id: str, *, limit: int = 100) -> list[ConnectionProfile]: ...
    def update_connection_profile_metadata(
        self,
        connection_profile_id: str,
        *,
        display_label: str | None = None,
        admin_label: str | None = None,
        admin_comment: str | None = None,
        status: str | None = None,
    ) -> ConnectionProfile: ...
    def bind_legacy_peer_to_profile(self, connection_profile_id: str, legacy_peer_id: str) -> ConnectionProfile: ...
    def sync_profile_runtime_projection(
        self,
        connection_profile_id: str,
        *,
        connected_at: datetime | None = None,
        last_handshake_at: datetime | None = None,
        last_real_ip: str | None = None,
        last_real_geo: str | None = None,
        current_edge_id: str | None = None,
        current_uplink_id: str | None = None,
        preferred_uplink_id: str | None = None,
        effective_uplink_id: str | None = None,
        failover_uplink_id: str | None = None,
    ) -> ConnectionProfile: ...

    # Claim bridge
    def create_profile_claim(self, claim: ProfileClaim) -> ProfileClaim: ...
    def get_pending_profile_claim(self, legacy_peer_id: str, account_id: str) -> ProfileClaim | None: ...
    def resolve_profile_claim(self, profile_claim_id: str, *, claim_status: str, resolved_by_account_id: str = "") -> ProfileClaim: ...

    # Subscription and balance
    def get_active_subscription(self, account_id: str) -> Subscription | None: ...
    def create_subscription(self, subscription: Subscription) -> Subscription: ...
    def get_account_balance(self, account_id: str) -> AccountBalance: ...
    def append_balance_ledger_entry(self, entry: BalanceLedgerEntry) -> BalanceLedgerEntry: ...
    def recalculate_account_balance(self, account_id: str) -> AccountBalance: ...

    # Read models
    def count_connection_profiles_by_account(self, account_id: str, *, statuses: list[str] | None = None) -> int: ...
    def list_recent_profile_claims(self, account_id: str, *, limit: int = 50) -> list[ProfileClaim]: ...
    def list_balance_ledger(self, account_id: str, *, limit: int = 100) -> list[BalanceLedgerEntry]: ...
