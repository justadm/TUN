from __future__ import annotations

import json
import sqlite3
import threading
import uuid
from contextlib import closing
from dataclasses import asdict
from datetime import datetime, timezone, timedelta
from pathlib import Path
from typing import Any

from store import (
    Account,
    AccountBalance,
    AccountIdentity,
    AccountLKStore,
    AccountSession,
    BalanceLedgerEntry,
    ConnectionProfile,
    ProfileClaim,
    Subscription,
)


def _now() -> datetime:
    return datetime.now(timezone.utc).replace(microsecond=0)


def _dt(value: str | None) -> datetime | None:
    raw = str(value or "").strip()
    if not raw:
        return None
    if raw.endswith("Z"):
        raw = raw[:-1] + "+00:00"
    return datetime.fromisoformat(raw)


def _ts(value: datetime | None) -> str | None:
    if value is None:
        return None
    if value.tzinfo is None:
        value = value.replace(tzinfo=timezone.utc)
    return value.astimezone(timezone.utc).replace(microsecond=0).isoformat()


def _blank_to_none(value: str | None) -> str | None:
    raw = str(value or "").strip()
    return raw or None


class SqliteAccountLKStore(AccountLKStore):
    def __init__(self, db_path: str | Path):
        self.db_path = str(db_path)
        self.conn = sqlite3.connect(self.db_path, check_same_thread=False)
        self.conn.row_factory = sqlite3.Row
        self.lock = threading.RLock()
        self._init_schema()

    def close(self) -> None:
        with self.lock:
            self.conn.close()

    def _init_schema(self) -> None:
        with self.lock:
            self.conn.executescript(
                """
            create table if not exists accounts (
              account_id text primary key,
              email text,
              phone text,
              display_name text,
              status text not null,
              locale text,
              timezone text,
              created_at text not null,
              updated_at text not null,
              last_login_at text
            );
            create unique index if not exists accounts_email_idx on accounts(email) where email is not null and email <> '';
            create unique index if not exists accounts_phone_idx on accounts(phone) where phone is not null and phone <> '';

            create table if not exists account_identities (
              identity_id text primary key,
              account_id text not null references accounts(account_id) on delete cascade,
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
              email_verified integer,
              profile_json text,
              created_at text not null,
              updated_at text not null,
              unique(provider, subject)
            );

            create table if not exists account_sessions (
              session_id text primary key,
              account_id text not null references accounts(account_id) on delete cascade,
              session_token text not null unique,
              status text not null,
              ip text,
              ua text,
              created_at text not null,
              expires_at text not null,
              last_seen_at text
            );

            create table if not exists connection_profiles (
              connection_profile_id text primary key,
              account_id text not null references accounts(account_id) on delete cascade,
              legacy_peer_id text unique,
              display_label text,
              system_label text,
              admin_label text,
              admin_comment text,
              public_key text unique,
              allowed_ip text,
              status text not null,
              created_at text not null,
              expires_at text,
              connected_at text,
              last_handshake_at text,
              last_real_ip text,
              last_real_geo text,
              current_edge_id text,
              current_uplink_id text,
              routing_policy_mode text,
              preferred_uplink_id text,
              effective_uplink_id text,
              failover_uplink_id text,
              runtime_source text
            );

            create table if not exists profile_claims (
              profile_claim_id text primary key,
              legacy_peer_id text not null,
              account_id text not null references accounts(account_id) on delete cascade,
              claim_method text not null,
              claim_status text not null,
              proof_payload text,
              created_at text not null,
              resolved_at text,
              resolved_by_account_id text
            );

            create table if not exists subscriptions (
              subscription_id text primary key,
              account_id text not null references accounts(account_id) on delete cascade,
              plan_code text not null,
              status text not null,
              started_at text,
              expires_at text,
              auto_renew integer not null default 0,
              profile_limit integer,
              metadata text
            );

            create table if not exists account_balances (
              account_id text primary key references accounts(account_id) on delete cascade,
              currency text not null default 'RUB',
              available_minor integer not null default 0,
              reserved_minor integer not null default 0,
              updated_at text not null
            );

            create table if not exists account_balance_ledger (
              ledger_entry_id integer primary key autoincrement,
              account_id text not null references accounts(account_id) on delete cascade,
              entry_type text not null,
              direction text not null,
              amount_minor integer not null,
              currency text not null default 'RUB',
              reference_type text,
              reference_id text,
              connection_profile_id text,
              note text,
              payload text,
              created_at text not null,
              created_by_account_id text
            );
                """
            )
            self.conn.commit()

    def _fetchone(self, query: str, params: tuple[Any, ...]) -> sqlite3.Row | None:
        with self.lock:
            with closing(self.conn.execute(query, params)) as cur:
                return cur.fetchone()

    def _fetchall(self, query: str, params: tuple[Any, ...]) -> list[sqlite3.Row]:
        with self.lock:
            with closing(self.conn.execute(query, params)) as cur:
                return cur.fetchall()

    def _row_to_account(self, row: sqlite3.Row | None) -> Account | None:
        if row is None:
            return None
        return Account(
            account_id=row["account_id"],
            email=row["email"] or "",
            phone=row["phone"] or "",
            display_name=row["display_name"] or "",
            status=row["status"],
            locale=row["locale"] or "",
            timezone=row["timezone"] or "",
            created_at=_dt(row["created_at"]),
            updated_at=_dt(row["updated_at"]),
            last_login_at=_dt(row["last_login_at"]),
        )

    def _row_to_identity(self, row: sqlite3.Row) -> AccountIdentity:
        return AccountIdentity(
            identity_id=row["identity_id"],
            account_id=row["account_id"],
            provider=row["provider"],
            subject=row["subject"],
            email_snapshot=row["email_snapshot"] or "",
            phone_snapshot=row["phone_snapshot"] or "",
            login_snapshot=row["login_snapshot"] or "",
            name_snapshot=row["name_snapshot"] or "",
            given_name=row["given_name"] or "",
            family_name=row["family_name"] or "",
            middle_name=row["middle_name"] or "",
            avatar_url=row["avatar_url"] or "",
            locale=row["locale"] or "",
            email_verified=None if row["email_verified"] is None else bool(row["email_verified"]),
            profile_json=json.loads(row["profile_json"] or "{}"),
            created_at=_dt(row["created_at"]),
            updated_at=_dt(row["updated_at"]),
        )

    def _row_to_session(self, row: sqlite3.Row) -> AccountSession:
        return AccountSession(
            session_id=row["session_id"],
            account_id=row["account_id"],
            session_token=row["session_token"],
            status=row["status"],
            ip=row["ip"] or "",
            ua=row["ua"] or "",
            created_at=_dt(row["created_at"]),
            expires_at=_dt(row["expires_at"]),
            last_seen_at=_dt(row["last_seen_at"]),
        )

    def _row_to_profile(self, row: sqlite3.Row | None) -> ConnectionProfile | None:
        if row is None:
            return None
        return ConnectionProfile(
            connection_profile_id=row["connection_profile_id"],
            account_id=row["account_id"],
            legacy_peer_id=row["legacy_peer_id"] or "",
            display_label=row["display_label"] or "",
            system_label=row["system_label"] or "",
            admin_label=row["admin_label"] or "",
            admin_comment=row["admin_comment"] or "",
            public_key=row["public_key"] or "",
            allowed_ip=row["allowed_ip"] or "",
            status=row["status"],
            created_at=_dt(row["created_at"]),
            expires_at=_dt(row["expires_at"]),
            connected_at=_dt(row["connected_at"]),
            last_handshake_at=_dt(row["last_handshake_at"]),
            last_real_ip=row["last_real_ip"] or "",
            last_real_geo=row["last_real_geo"] or "",
            current_edge_id=row["current_edge_id"] or "",
            current_uplink_id=row["current_uplink_id"] or "",
            routing_policy_mode=row["routing_policy_mode"] or "",
            preferred_uplink_id=row["preferred_uplink_id"] or "",
            effective_uplink_id=row["effective_uplink_id"] or "",
            failover_uplink_id=row["failover_uplink_id"] or "",
            runtime_source=row["runtime_source"] or "",
        )

    def _row_to_claim(self, row: sqlite3.Row | None) -> ProfileClaim | None:
        if row is None:
            return None
        return ProfileClaim(
            profile_claim_id=row["profile_claim_id"],
            legacy_peer_id=row["legacy_peer_id"],
            account_id=row["account_id"],
            claim_method=row["claim_method"],
            claim_status=row["claim_status"],
            proof_payload=json.loads(row["proof_payload"] or "{}"),
            created_at=_dt(row["created_at"]),
            resolved_at=_dt(row["resolved_at"]),
            resolved_by_account_id=row["resolved_by_account_id"] or "",
        )

    def _row_to_subscription(self, row: sqlite3.Row | None) -> Subscription | None:
        if row is None:
            return None
        return Subscription(
            subscription_id=row["subscription_id"],
            account_id=row["account_id"],
            plan_code=row["plan_code"],
            status=row["status"],
            started_at=_dt(row["started_at"]),
            expires_at=_dt(row["expires_at"]),
            auto_renew=bool(row["auto_renew"]),
            profile_limit=row["profile_limit"],
            metadata=json.loads(row["metadata"] or "{}"),
        )

    def _row_to_balance(self, row: sqlite3.Row | None, account_id: str) -> AccountBalance:
        if row is None:
            return AccountBalance(account_id=account_id, updated_at=_now())
        return AccountBalance(
            account_id=row["account_id"],
            currency=row["currency"],
            available_minor=int(row["available_minor"] or 0),
            reserved_minor=int(row["reserved_minor"] or 0),
            updated_at=_dt(row["updated_at"]),
        )

    def _row_to_ledger(self, row: sqlite3.Row) -> BalanceLedgerEntry:
        return BalanceLedgerEntry(
            ledger_entry_id=int(row["ledger_entry_id"]),
            account_id=row["account_id"],
            entry_type=row["entry_type"],
            direction=row["direction"],
            amount_minor=int(row["amount_minor"]),
            currency=row["currency"],
            reference_type=row["reference_type"] or "",
            reference_id=row["reference_id"] or "",
            connection_profile_id=row["connection_profile_id"] or "",
            note=row["note"] or "",
            payload=json.loads(row["payload"] or "{}"),
            created_at=_dt(row["created_at"]),
            created_by_account_id=row["created_by_account_id"] or "",
        )

    # Accounts
    def create_account(self, *, email: str = "", phone: str = "", display_name: str = "") -> Account:
        account = Account(account_id=str(uuid.uuid4()), email=email, phone=phone, display_name=display_name, created_at=_now(), updated_at=_now())
        self.conn.execute(
            "insert into accounts(account_id,email,phone,display_name,status,locale,timezone,created_at,updated_at,last_login_at) values(?,?,?,?,?,?,?,?,?,?)",
            (account.account_id, account.email, account.phone, account.display_name, account.status, account.locale, account.timezone, _ts(account.created_at), _ts(account.updated_at), _ts(account.last_login_at)),
        )
        self.conn.execute(
            "insert or ignore into account_balances(account_id,currency,available_minor,reserved_minor,updated_at) values(?,?,?,?,?)",
            (account.account_id, "RUB", 0, 0, _ts(_now())),
        )
        self.conn.commit()
        return account

    def get_account(self, account_id: str) -> Account | None:
        return self._row_to_account(self._fetchone("select * from accounts where account_id = ?", (account_id,)))

    def get_account_by_email(self, email: str) -> Account | None:
        return self._row_to_account(self._fetchone("select * from accounts where lower(email) = lower(?)", (email,)))

    def get_account_by_phone(self, phone: str) -> Account | None:
        return self._row_to_account(self._fetchone("select * from accounts where phone = ?", (phone,)))

    def update_account_profile(self, account_id: str, *, display_name: str | None = None, locale: str | None = None, timezone: str | None = None, status: str | None = None) -> Account:
        row = self.get_account(account_id)
        if row is None:
            raise KeyError(account_id)
        self.conn.execute(
            "update accounts set display_name=?, locale=?, timezone=?, status=?, updated_at=? where account_id=?",
            (
                row.display_name if display_name is None else display_name,
                row.locale if locale is None else locale,
                row.timezone if timezone is None else timezone,
                row.status if status is None else status,
                _ts(_now()),
                account_id,
            ),
        )
        self.conn.commit()
        return self.get_account(account_id)  # type: ignore[return-value]

    # Identities
    def get_account_by_identity(self, provider: str, subject: str) -> Account | None:
        row = self._fetchone(
            "select a.* from accounts a join account_identities i on i.account_id = a.account_id where i.provider = ? and i.subject = ?",
            (provider, subject),
        )
        return self._row_to_account(row)

    def upsert_account_identity(self, identity: AccountIdentity) -> AccountIdentity:
        identity_id = identity.identity_id or str(uuid.uuid4())
        now = _now()
        self.conn.execute(
            """
            insert into account_identities(identity_id,account_id,provider,subject,email_snapshot,phone_snapshot,login_snapshot,name_snapshot,given_name,family_name,middle_name,avatar_url,locale,email_verified,profile_json,created_at,updated_at)
            values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
            on conflict(provider, subject) do update set
              account_id=excluded.account_id,
              email_snapshot=excluded.email_snapshot,
              phone_snapshot=excluded.phone_snapshot,
              login_snapshot=excluded.login_snapshot,
              name_snapshot=excluded.name_snapshot,
              given_name=excluded.given_name,
              family_name=excluded.family_name,
              middle_name=excluded.middle_name,
              avatar_url=excluded.avatar_url,
              locale=excluded.locale,
              email_verified=excluded.email_verified,
              profile_json=excluded.profile_json,
              updated_at=excluded.updated_at
            """,
            (
                identity_id, identity.account_id, identity.provider, identity.subject, identity.email_snapshot, identity.phone_snapshot,
                identity.login_snapshot, identity.name_snapshot, identity.given_name, identity.family_name, identity.middle_name,
                identity.avatar_url, identity.locale, None if identity.email_verified is None else int(identity.email_verified),
                json.dumps(identity.profile_json or {}, ensure_ascii=False), _ts(now), _ts(now),
            ),
        )
        self.conn.commit()
        row = self._fetchone("select * from account_identities where provider=? and subject=?", (identity.provider, identity.subject))
        return self._row_to_identity(row)  # type: ignore[arg-type]

    def list_account_identities(self, account_id: str) -> list[AccountIdentity]:
        return [self._row_to_identity(r) for r in self._fetchall("select * from account_identities where account_id=? order by provider, subject", (account_id,))]

    def delete_account_identity(self, account_id: str, provider: str, subject: str) -> None:
        self.conn.execute("delete from account_identities where account_id=? and provider=? and subject=?", (account_id, provider, subject))
        self.conn.commit()

    # Sessions
    def create_account_session(self, account_id: str, *, ttl_seconds: int, ip: str = "", ua: str = "") -> AccountSession:
        now = _now()
        sess = AccountSession(
            session_id=str(uuid.uuid4()),
            account_id=account_id,
            session_token=uuid.uuid4().hex + uuid.uuid4().hex,
            status="active",
            ip=ip,
            ua=ua,
            created_at=now,
            expires_at=now + timedelta(seconds=ttl_seconds),
            last_seen_at=now,
        )
        self.conn.execute(
            "insert into account_sessions(session_id,account_id,session_token,status,ip,ua,created_at,expires_at,last_seen_at) values(?,?,?,?,?,?,?,?,?)",
            (sess.session_id, sess.account_id, sess.session_token, sess.status, sess.ip, sess.ua, _ts(sess.created_at), _ts(sess.expires_at), _ts(sess.last_seen_at)),
        )
        self.conn.execute("update accounts set last_login_at=?, updated_at=? where account_id=?", (_ts(now), _ts(now), account_id))
        self.conn.commit()
        return sess

    def get_account_by_session_token(self, session_token: str) -> Account | None:
        row = self._fetchone(
            "select a.* from accounts a join account_sessions s on s.account_id = a.account_id where s.session_token=? and s.status='active'",
            (session_token,),
        )
        return self._row_to_account(row)

    def revoke_account_session(self, session_id: str) -> None:
        self.conn.execute("update account_sessions set status='revoked' where session_id=?", (session_id,))
        self.conn.commit()

    def touch_account_session(self, session_id: str) -> None:
        self.conn.execute("update account_sessions set last_seen_at=? where session_id=?", (_ts(_now()), session_id))
        self.conn.commit()

    # Connection profiles
    def create_connection_profile(self, profile: ConnectionProfile) -> ConnectionProfile:
        pid = profile.connection_profile_id or str(uuid.uuid4())
        now = _now()
        self.conn.execute(
            """
            insert into connection_profiles(connection_profile_id,account_id,legacy_peer_id,display_label,system_label,admin_label,admin_comment,public_key,allowed_ip,status,created_at,expires_at,connected_at,last_handshake_at,last_real_ip,last_real_geo,current_edge_id,current_uplink_id,routing_policy_mode,preferred_uplink_id,effective_uplink_id,failover_uplink_id,runtime_source)
            values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
            """,
            (
                pid, profile.account_id, _blank_to_none(profile.legacy_peer_id), profile.display_label, profile.system_label, profile.admin_label, profile.admin_comment,
                _blank_to_none(profile.public_key), profile.allowed_ip, profile.status, _ts(profile.created_at or now), _ts(profile.expires_at), _ts(profile.connected_at),
                _ts(profile.last_handshake_at), profile.last_real_ip, profile.last_real_geo, profile.current_edge_id, profile.current_uplink_id,
                profile.routing_policy_mode, profile.preferred_uplink_id, profile.effective_uplink_id, profile.failover_uplink_id, profile.runtime_source,
            ),
        )
        self.conn.commit()
        return self.get_connection_profile(pid)  # type: ignore[return-value]

    def get_connection_profile(self, connection_profile_id: str) -> ConnectionProfile | None:
        return self._row_to_profile(self._fetchone("select * from connection_profiles where connection_profile_id=?", (connection_profile_id,)))

    def get_connection_profile_by_legacy_peer_id(self, legacy_peer_id: str) -> ConnectionProfile | None:
        return self._row_to_profile(self._fetchone("select * from connection_profiles where legacy_peer_id=?", (legacy_peer_id,)))

    def list_connection_profiles_by_account(self, account_id: str, *, limit: int = 100) -> list[ConnectionProfile]:
        return [self._row_to_profile(r) for r in self._fetchall("select * from connection_profiles where account_id=? order by created_at desc limit ?", (account_id, limit)) if self._row_to_profile(r)]

    def update_connection_profile_metadata(self, connection_profile_id: str, *, display_label: str | None = None, admin_label: str | None = None, admin_comment: str | None = None, status: str | None = None) -> ConnectionProfile:
        row = self.get_connection_profile(connection_profile_id)
        if row is None:
            raise KeyError(connection_profile_id)
        self.conn.execute(
            "update connection_profiles set display_label=?, admin_label=?, admin_comment=?, status=? where connection_profile_id=?",
            (
                row.display_label if display_label is None else display_label,
                row.admin_label if admin_label is None else admin_label,
                row.admin_comment if admin_comment is None else admin_comment,
                row.status if status is None else status,
                connection_profile_id,
            ),
        )
        self.conn.commit()
        return self.get_connection_profile(connection_profile_id)  # type: ignore[return-value]

    def bind_legacy_peer_to_profile(self, connection_profile_id: str, legacy_peer_id: str) -> ConnectionProfile:
        self.conn.execute("update connection_profiles set legacy_peer_id=? where connection_profile_id=?", (legacy_peer_id, connection_profile_id))
        self.conn.commit()
        return self.get_connection_profile(connection_profile_id)  # type: ignore[return-value]

    def sync_profile_runtime_projection(self, connection_profile_id: str, *, connected_at: datetime | None = None, last_handshake_at: datetime | None = None, last_real_ip: str | None = None, last_real_geo: str | None = None, current_edge_id: str | None = None, current_uplink_id: str | None = None, preferred_uplink_id: str | None = None, effective_uplink_id: str | None = None, failover_uplink_id: str | None = None) -> ConnectionProfile:
        row = self.get_connection_profile(connection_profile_id)
        if row is None:
            raise KeyError(connection_profile_id)
        self.conn.execute(
            """
            update connection_profiles set
              connected_at=?,
              last_handshake_at=?,
              last_real_ip=?,
              last_real_geo=?,
              current_edge_id=?,
              current_uplink_id=?,
              preferred_uplink_id=?,
              effective_uplink_id=?,
              failover_uplink_id=?
            where connection_profile_id=?
            """,
            (
                _ts(row.connected_at if connected_at is None else connected_at),
                _ts(row.last_handshake_at if last_handshake_at is None else last_handshake_at),
                row.last_real_ip if last_real_ip is None else last_real_ip,
                row.last_real_geo if last_real_geo is None else last_real_geo,
                row.current_edge_id if current_edge_id is None else current_edge_id,
                row.current_uplink_id if current_uplink_id is None else current_uplink_id,
                row.preferred_uplink_id if preferred_uplink_id is None else preferred_uplink_id,
                row.effective_uplink_id if effective_uplink_id is None else effective_uplink_id,
                row.failover_uplink_id if failover_uplink_id is None else failover_uplink_id,
                connection_profile_id,
            ),
        )
        self.conn.commit()
        return self.get_connection_profile(connection_profile_id)  # type: ignore[return-value]

    # Claim bridge
    def create_profile_claim(self, claim: ProfileClaim) -> ProfileClaim:
        cid = claim.profile_claim_id or str(uuid.uuid4())
        now = _now()
        self.conn.execute(
            "insert into profile_claims(profile_claim_id,legacy_peer_id,account_id,claim_method,claim_status,proof_payload,created_at,resolved_at,resolved_by_account_id) values(?,?,?,?,?,?,?,?,?)",
            (cid, claim.legacy_peer_id, claim.account_id, claim.claim_method, claim.claim_status, json.dumps(claim.proof_payload or {}, ensure_ascii=False), _ts(claim.created_at or now), _ts(claim.resolved_at), claim.resolved_by_account_id),
        )
        self.conn.commit()
        return self._row_to_claim(self._fetchone("select * from profile_claims where profile_claim_id=?", (cid,)))  # type: ignore[return-value]

    def get_pending_profile_claim(self, legacy_peer_id: str, account_id: str) -> ProfileClaim | None:
        return self._row_to_claim(self._fetchone("select * from profile_claims where legacy_peer_id=? and account_id=? and claim_status='pending' order by created_at desc limit 1", (legacy_peer_id, account_id)))

    def resolve_profile_claim(self, profile_claim_id: str, *, claim_status: str, resolved_by_account_id: str = "") -> ProfileClaim:
        self.conn.execute("update profile_claims set claim_status=?, resolved_at=?, resolved_by_account_id=? where profile_claim_id=?", (claim_status, _ts(_now()), resolved_by_account_id, profile_claim_id))
        self.conn.commit()
        return self._row_to_claim(self._fetchone("select * from profile_claims where profile_claim_id=?", (profile_claim_id,)))  # type: ignore[return-value]

    # Subscription and balance
    def get_active_subscription(self, account_id: str) -> Subscription | None:
        return self._row_to_subscription(self._fetchone("select * from subscriptions where account_id=? and status in ('active','trial','past_due') order by started_at desc limit 1", (account_id,)))

    def create_subscription(self, subscription: Subscription) -> Subscription:
        sid = subscription.subscription_id or str(uuid.uuid4())
        self.conn.execute(
            "insert into subscriptions(subscription_id,account_id,plan_code,status,started_at,expires_at,auto_renew,profile_limit,metadata) values(?,?,?,?,?,?,?,?,?)",
            (sid, subscription.account_id, subscription.plan_code, subscription.status, _ts(subscription.started_at or _now()), _ts(subscription.expires_at), int(subscription.auto_renew), subscription.profile_limit, json.dumps(subscription.metadata or {}, ensure_ascii=False)),
        )
        self.conn.commit()
        return self._row_to_subscription(self._fetchone("select * from subscriptions where subscription_id=?", (sid,)))  # type: ignore[return-value]

    def get_account_balance(self, account_id: str) -> AccountBalance:
        row = self._fetchone("select * from account_balances where account_id=?", (account_id,))
        if row is None:
            self.conn.execute("insert or ignore into account_balances(account_id,currency,available_minor,reserved_minor,updated_at) values(?,?,?,?,?)", (account_id, "RUB", 0, 0, _ts(_now())))
            self.conn.commit()
            row = self._fetchone("select * from account_balances where account_id=?", (account_id,))
        return self._row_to_balance(row, account_id)

    def append_balance_ledger_entry(self, entry: BalanceLedgerEntry) -> BalanceLedgerEntry:
        self.conn.execute(
            "insert into account_balance_ledger(account_id,entry_type,direction,amount_minor,currency,reference_type,reference_id,connection_profile_id,note,payload,created_at,created_by_account_id) values(?,?,?,?,?,?,?,?,?,?,?,?)",
            (
                entry.account_id, entry.entry_type, entry.direction, entry.amount_minor, entry.currency, entry.reference_type, entry.reference_id,
                entry.connection_profile_id, entry.note, json.dumps(entry.payload or {}, ensure_ascii=False), _ts(entry.created_at or _now()), entry.created_by_account_id,
            ),
        )
        self.conn.commit()
        self.recalculate_account_balance(entry.account_id)
        row = self._fetchone("select * from account_balance_ledger order by ledger_entry_id desc limit 1", ())
        return self._row_to_ledger(row)  # type: ignore[arg-type]

    def recalculate_account_balance(self, account_id: str) -> AccountBalance:
        rows = self._fetchall("select direction, amount_minor from account_balance_ledger where account_id=?", (account_id,))
        total = 0
        for row in rows:
            amount = int(row["amount_minor"] or 0)
            total += amount if row["direction"] == "credit" else -amount
        self.conn.execute(
            "insert into account_balances(account_id,currency,available_minor,reserved_minor,updated_at) values(?,?,?,?,?) on conflict(account_id) do update set available_minor=excluded.available_minor,reserved_minor=excluded.reserved_minor,updated_at=excluded.updated_at",
            (account_id, "RUB", total, 0, _ts(_now())),
        )
        self.conn.commit()
        return self.get_account_balance(account_id)

    # Read models
    def count_connection_profiles_by_account(self, account_id: str, *, statuses: list[str] | None = None) -> int:
        if statuses:
            placeholders = ",".join("?" for _ in statuses)
            row = self._fetchone(f"select count(*) as c from connection_profiles where account_id=? and status in ({placeholders})", tuple([account_id, *statuses]))
        else:
            row = self._fetchone("select count(*) as c from connection_profiles where account_id=?", (account_id,))
        return int((row["c"] if row else 0) or 0)

    def list_recent_profile_claims(self, account_id: str, *, limit: int = 50) -> list[ProfileClaim]:
        return [self._row_to_claim(r) for r in self._fetchall("select * from profile_claims where account_id=? order by created_at desc limit ?", (account_id, limit)) if self._row_to_claim(r)]

    def list_balance_ledger(self, account_id: str, *, limit: int = 100) -> list[BalanceLedgerEntry]:
        return [self._row_to_ledger(r) for r in self._fetchall("select * from account_balance_ledger where account_id=? order by created_at desc, ledger_entry_id desc limit ?", (account_id, limit))]
