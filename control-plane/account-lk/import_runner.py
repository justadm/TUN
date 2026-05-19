#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from import_legacy_profiles import build_plan
from sqlite_store import SqliteAccountLKStore
from store import ConnectionProfile, ProfileClaim


def _read_json(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def load_plan(args: argparse.Namespace) -> dict:
    if args.plan:
        return _read_json(Path(args.plan))
    snapshot = _read_json(Path(args.snapshot))
    mapping = _read_json(Path(args.account_mapping)) if args.account_mapping else {}
    if not isinstance(mapping, dict):
        raise SystemExit("account mapping must be a JSON object")
    return build_plan(
        snapshot,
        {str(k): str(v) for k, v in mapping.items()},
        bool(args.bootstrap_per_peer),
    )


def import_plan(store: SqliteAccountLKStore, plan: dict) -> dict[str, int]:
    counts = {
        "accounts": 0,
        "profiles": 0,
        "claims": 0,
        "quarantine": len(plan.get("quarantine") or []),
    }

    for row in plan.get("accounts") or []:
        if store.get_account(row["account_id"]):
            continue
        store.conn.execute(
            """
            insert into accounts(account_id,email,phone,display_name,status,locale,timezone,created_at,updated_at,last_login_at)
            values(?,?,?,?,?,?,?,?,?,?)
            """,
            (
                row["account_id"],
                row.get("email", ""),
                row.get("phone", ""),
                row.get("display_name", ""),
                row.get("status", "pending"),
                row.get("locale", ""),
                row.get("timezone", ""),
                row.get("created_at"),
                row.get("updated_at"),
                row.get("last_login_at") or None,
            ),
        )
        store.conn.execute(
            """
            insert or ignore into account_balances(account_id,currency,available_minor,reserved_minor,updated_at)
            values(?,?,?,?,?)
            """,
            (row["account_id"], "RUB", 0, 0, row.get("updated_at") or row.get("created_at")),
        )
        counts["accounts"] += 1

    for row in plan.get("connection_profiles") or []:
        if row.get("legacy_peer_id") and store.get_connection_profile_by_legacy_peer_id(row["legacy_peer_id"]):
            continue
        profile = ConnectionProfile(
            connection_profile_id=row.get("connection_profile_id", ""),
            account_id=row["account_id"],
            legacy_peer_id=row.get("legacy_peer_id", ""),
            display_label=row.get("display_label", ""),
            system_label=row.get("system_label", ""),
            admin_label=row.get("admin_label", ""),
            admin_comment=row.get("admin_comment", ""),
            public_key=row.get("public_key", ""),
            allowed_ip=row.get("allowed_ip", ""),
            status=row.get("status", "pending"),
        )
        store.create_connection_profile(profile)
        counts["profiles"] += 1

    for row in plan.get("profile_claims") or []:
        pending = store.get_pending_profile_claim(row["legacy_peer_id"], row["account_id"])
        if pending:
            continue
        claim = ProfileClaim(
            profile_claim_id=row.get("profile_claim_id", ""),
            legacy_peer_id=row["legacy_peer_id"],
            account_id=row["account_id"],
            claim_method=row.get("claim_method", "operator_attach"),
            claim_status=row.get("claim_status", "pending"),
            proof_payload=row.get("proof_payload") or {},
        )
        store.create_profile_claim(claim)
        counts["claims"] += 1

    store.conn.commit()
    return counts


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Import staged account/profile entities into sqlite account-LK store."
    )
    parser.add_argument("--db", required=True, help="SQLite database path.")
    parser.add_argument("--plan", help="Prebuilt import plan JSON.")
    parser.add_argument("--snapshot", help="Legacy snapshot JSON.")
    parser.add_argument("--account-mapping", help="Optional legacy peer_id -> account_id mapping JSON.")
    parser.add_argument("--bootstrap-per-peer", action="store_true", help="Create one synthetic account per peer.")
    args = parser.parse_args()

    if not args.plan and not args.snapshot:
        raise SystemExit("either --plan or --snapshot is required")

    plan = load_plan(args)
    store = SqliteAccountLKStore(args.db)
    try:
        counts = import_plan(store, plan)
    finally:
        store.close()

    print(json.dumps({"ok": True, **counts}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    sys.exit(main())
