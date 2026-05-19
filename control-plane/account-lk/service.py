from __future__ import annotations

from dataclasses import asdict
from datetime import datetime
from typing import Any

from store import (
    Account,
    AccountIdentity,
    AccountLKStore,
    AccountSession,
    ConnectionProfile,
    ProfileClaim,
)


class AccountLKService:
    def __init__(self, store: AccountLKStore, session_ttl_seconds: int = 60 * 60 * 24 * 30):
        self.store = store
        self.session_ttl_seconds = session_ttl_seconds

    def register_or_login_by_identity(
        self,
        *,
        provider: str,
        subject: str,
        email: str = "",
        phone: str = "",
        display_name: str = "",
        profile_payload: dict[str, Any] | None = None,
        ip: str = "",
        ua: str = "",
    ) -> tuple[Account, AccountSession]:
        account = self.store.get_account_by_identity(provider, subject)
        if account is None:
            if email:
                account = self.store.get_account_by_email(email)
            if account is None and phone:
                account = self.store.get_account_by_phone(phone)
            if account is None:
                account = self.store.create_account(email=email, phone=phone, display_name=display_name)
            else:
                account = self.store.update_account_profile(
                    account.account_id,
                    display_name=display_name or None,
                )

        identity = AccountIdentity(
            identity_id="",
            account_id=account.account_id,
            provider=provider,
            subject=subject,
            email_snapshot=email,
            phone_snapshot=phone,
            login_snapshot=display_name,
            name_snapshot=display_name,
            profile_json=profile_payload or {},
        )
        self.store.upsert_account_identity(identity)
        session = self.store.create_account_session(account.account_id, ttl_seconds=self.session_ttl_seconds, ip=ip, ua=ua)
        return account, session

    def resolve_account_by_session(self, session_token: str) -> Account | None:
        return self.store.get_account_by_session_token(session_token)

    def account_home_payload(self, account_id: str) -> dict[str, Any]:
        account = self.store.get_account(account_id)
        if account is None:
            raise KeyError(account_id)
        return {
            "account": asdict(account),
            "identities": [asdict(x) for x in self.store.list_account_identities(account_id)],
            "connection_profiles": [asdict(x) for x in self.store.list_connection_profiles_by_account(account_id)],
            "subscription": asdict(sub) if (sub := self.store.get_active_subscription(account_id)) else None,
            "balance": asdict(self.store.get_account_balance(account_id)),
            "recent_claims": [asdict(x) for x in self.store.list_recent_profile_claims(account_id)],
        }

    def attach_legacy_profile(
        self,
        *,
        account_id: str,
        legacy_peer_id: str,
        display_label: str = "",
        claim_method: str = "operator_attach",
        proof_payload: dict[str, Any] | None = None,
        resolved_by_account_id: str = "",
    ) -> ProfileClaim:
        claim = self.store.create_profile_claim(
            ProfileClaim(
                profile_claim_id="",
                legacy_peer_id=legacy_peer_id,
                account_id=account_id,
                claim_method=claim_method,
                claim_status="pending",
                proof_payload=proof_payload or {},
            )
        )
        profile = self.store.get_connection_profile_by_legacy_peer_id(legacy_peer_id)
        if profile is None:
            self.store.create_connection_profile(
                ConnectionProfile(
                    connection_profile_id="",
                    account_id=account_id,
                    legacy_peer_id=legacy_peer_id,
                    display_label=display_label or legacy_peer_id,
                    system_label=display_label or legacy_peer_id,
                    admin_label=display_label or legacy_peer_id,
                    status="active",
                )
            )
        elif profile.account_id != account_id:
            self.store.update_connection_profile_metadata(
                profile.connection_profile_id,
                display_label=display_label or profile.display_label or legacy_peer_id,
                admin_label=display_label or profile.admin_label or profile.display_label or legacy_peer_id,
                status=profile.status,
            )
        return self.store.resolve_profile_claim(
            claim.profile_claim_id,
            claim_status="confirmed",
            resolved_by_account_id=resolved_by_account_id or account_id,
        )

    def create_profile_stub(
        self,
        *,
        account_id: str,
        display_label: str,
        system_label: str = "",
        admin_label: str = "",
        legacy_peer_id: str = "",
        status: str = "pending",
    ) -> ConnectionProfile:
        profile = ConnectionProfile(
            connection_profile_id="",
            account_id=account_id,
            legacy_peer_id=legacy_peer_id,
            display_label=display_label,
            system_label=system_label or display_label,
            admin_label=admin_label or display_label,
            status=status,
        )
        return self.store.create_connection_profile(profile)

    def register_issued_profile(
        self,
        *,
        account_id: str,
        legacy_peer_id: str,
        display_label: str,
        system_label: str = "",
        admin_label: str = "",
        allowed_ip: str = "",
        status: str = "pending",
        expires_at: datetime | None = None,
        current_edge_id: str = "",
        current_uplink_id: str = "",
        preferred_uplink_id: str = "",
        effective_uplink_id: str = "",
        failover_uplink_id: str = "",
        routing_policy_mode: str = "",
        runtime_source: str = "portal_issue",
    ) -> ConnectionProfile:
        profile = self.store.get_connection_profile_by_legacy_peer_id(legacy_peer_id)
        if profile is None:
            profile = self.store.create_connection_profile(
                ConnectionProfile(
                    connection_profile_id="",
                    account_id=account_id,
                    legacy_peer_id=legacy_peer_id,
                    display_label=display_label,
                    system_label=system_label or display_label,
                    admin_label=admin_label or display_label,
                    allowed_ip=allowed_ip,
                    status=status,
                    expires_at=expires_at,
                    current_edge_id=current_edge_id,
                    current_uplink_id=current_uplink_id,
                    routing_policy_mode=routing_policy_mode,
                    preferred_uplink_id=preferred_uplink_id,
                    effective_uplink_id=effective_uplink_id,
                    failover_uplink_id=failover_uplink_id,
                    runtime_source=runtime_source,
                )
            )
            return profile
        profile = self.store.update_connection_profile_metadata(
            profile.connection_profile_id,
            display_label=display_label or profile.display_label,
            admin_label=admin_label or profile.admin_label or display_label,
            status=status or profile.status,
        )
        return self.store.sync_profile_runtime_projection(
            profile.connection_profile_id,
            current_edge_id=current_edge_id or None,
            current_uplink_id=current_uplink_id or None,
            preferred_uplink_id=preferred_uplink_id or None,
            effective_uplink_id=effective_uplink_id or None,
            failover_uplink_id=failover_uplink_id or None,
        )

    def sync_profile_from_runtime(
        self,
        *,
        connection_profile_id: str,
        connected_at: datetime | None = None,
        last_handshake_at: datetime | None = None,
        last_real_ip: str | None = None,
        last_real_geo: str | None = None,
        current_edge_id: str | None = None,
        current_uplink_id: str | None = None,
        preferred_uplink_id: str | None = None,
        effective_uplink_id: str | None = None,
        failover_uplink_id: str | None = None,
    ) -> ConnectionProfile:
        return self.store.sync_profile_runtime_projection(
            connection_profile_id,
            connected_at=connected_at,
            last_handshake_at=last_handshake_at,
            last_real_ip=last_real_ip,
            last_real_geo=last_real_geo,
            current_edge_id=current_edge_id,
            current_uplink_id=current_uplink_id,
            preferred_uplink_id=preferred_uplink_id,
            effective_uplink_id=effective_uplink_id,
            failover_uplink_id=failover_uplink_id,
        )
