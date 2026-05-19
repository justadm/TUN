#!/usr/bin/env python3
from __future__ import annotations

import json
import sys
import tempfile
from pathlib import Path

from service import AccountLKService
from sqlite_store import SqliteAccountLKStore


def main() -> int:
    with tempfile.TemporaryDirectory(prefix="account-lk-smoke-") as tmp:
        db_path = Path(tmp) / "account_lk.sqlite3"
        store = SqliteAccountLKStore(db_path)
        service = AccountLKService(store)

        account, session = service.register_or_login_by_identity(
            provider="telegram",
            subject="tg:12345",
            email="demo@example.com",
            display_name="Demo User",
            profile_payload={"username": "demo"},
            ip="127.0.0.1",
            ua="smoke",
        )
        profile = service.create_profile_stub(
            account_id=account.account_id,
            display_label="Demo Profile",
            system_label="demo-profile",
            legacy_peer_id="p-demo-001",
            status="active",
        )
        service.attach_legacy_profile(
            account_id=account.account_id,
            legacy_peer_id="p-demo-001",
            claim_method="operator_attach",
        )
        payload = service.account_home_payload(account.account_id)
        print(
            json.dumps(
                {
                    "ok": True,
                    "account_id": account.account_id,
                    "session_token_len": len(session.session_token),
                    "profiles": len(payload["connection_profiles"]),
                    "identities": len(payload["identities"]),
                    "balance_minor": payload["balance"]["available_minor"],
                    "profile_id": profile.connection_profile_id,
                },
                ensure_ascii=False,
            )
        )
        store.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
