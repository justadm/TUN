#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import sys
import uuid
from datetime import datetime, timezone
from pathlib import Path


def _iso_now() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def _read_json(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def _normalize_status(value: str) -> str:
    raw = str(value or "").strip().lower()
    if raw in {"pending", "active", "blocked", "expired", "removed"}:
        return raw
    return "pending"


def _profile_row(peer: dict, account_id: str) -> dict:
    peer_id = str(peer.get("peer_id") or peer.get("id") or "").strip()
    label = str(peer.get("label") or peer_id).strip()
    admin_label = str(peer.get("admin_label") or label).strip()
    return {
        "connection_profile_id": str(uuid.uuid4()),
        "account_id": account_id,
        "legacy_peer_id": peer_id,
        "display_label": label,
        "system_label": label,
        "admin_label": admin_label,
        "admin_comment": str(peer.get("admin_comment") or "").strip(),
        "public_key": str(peer.get("public_key") or "").strip(),
        "allowed_ip": str(peer.get("allowed_ip") or "").strip(),
        "status": _normalize_status(str(peer.get("status") or "")),
        "created_at": str(peer.get("created_at") or _iso_now()),
        "expires_at": str(peer.get("absolute_expires_at") or peer.get("expires_at") or ""),
        "connected_at": str(peer.get("connected_at") or ""),
        "last_handshake_at": str(peer.get("last_handshake_at") or ""),
        "last_real_ip": str(peer.get("real_ip") or ""),
        "current_edge_id": str(peer.get("ingress_edge_id") or ""),
        "routing_policy_mode": str(peer.get("policy_mode") or ""),
        "preferred_uplink_id": str(peer.get("preferred_uplink_id") or ""),
        "effective_uplink_id": str(peer.get("active_uplink_id") or peer.get("effective_uplink_id") or ""),
        "failover_uplink_id": str(peer.get("failover_uplink_id") or ""),
        "runtime_source": "legacy_import",
    }


def _bootstrap_account(label: str) -> dict:
    return {
        "account_id": str(uuid.uuid4()),
        "email": "",
        "phone": "",
        "display_name": label,
        "status": "pending",
        "locale": "ru",
        "timezone": "Europe/Moscow",
        "created_at": _iso_now(),
        "updated_at": _iso_now(),
        "last_login_at": "",
    }


def build_plan(snapshot: dict, mapping: dict[str, str], bootstrap_per_peer: bool) -> dict:
    peers = list(snapshot.get("peers") or snapshot.get("tables", {}).get("peers") or [])
    result = {
        "ok": True,
        "generated_at": _iso_now(),
        "accounts": [],
        "connection_profiles": [],
        "profile_claims": [],
        "quarantine": [],
    }
    created_accounts: dict[str, dict] = {}

    for peer in peers:
        peer_id = str(peer.get("peer_id") or peer.get("id") or "").strip()
        if not peer_id:
            result["quarantine"].append({"kind": "peer_without_id", "peer": peer})
            continue

        account_id = str(mapping.get(peer_id) or "").strip()
        if not account_id and bootstrap_per_peer:
            label = str(peer.get("admin_label") or peer.get("label") or peer_id).strip()
            acc = _bootstrap_account(label or peer_id)
            account_id = acc["account_id"]
            created_accounts[account_id] = acc
        if not account_id:
            result["quarantine"].append({"kind": "unmapped_peer", "peer_id": peer_id})
            continue

        result["connection_profiles"].append(_profile_row(peer, account_id))
        result["profile_claims"].append(
            {
                "profile_claim_id": str(uuid.uuid4()),
                "legacy_peer_id": peer_id,
                "account_id": account_id,
                "claim_method": "operator_attach" if bootstrap_per_peer else "wg_session",
                "claim_status": "pending",
                "proof_payload": {},
                "created_at": _iso_now(),
                "resolved_at": "",
                "resolved_by_account_id": "",
            }
        )

    result["accounts"] = list(created_accounts.values())
    return result


def main() -> int:
    parser = argparse.ArgumentParser(description="Build staged import data from legacy peers into account-based LK entities.")
    parser.add_argument("--snapshot", required=True, help="Path to legacy export snapshot JSON.")
    parser.add_argument("--account-mapping", help="JSON file mapping legacy peer_id -> account_id.")
    parser.add_argument("--bootstrap-per-peer", action="store_true", help="Create one synthetic pending account per peer when mapping is absent.")
    parser.add_argument("--output", required=True, help="Output JSON path.")
    args = parser.parse_args()

    snapshot_path = Path(args.snapshot)
    output_path = Path(args.output)
    mapping_path = Path(args.account_mapping) if args.account_mapping else None

    snapshot = _read_json(snapshot_path)
    mapping = _read_json(mapping_path) if mapping_path else {}
    if not isinstance(mapping, dict):
        raise SystemExit("account mapping must be a JSON object: {\"legacy_peer_id\":\"account_id\"}")

    plan = build_plan(snapshot, {str(k): str(v) for k, v in mapping.items()}, bool(args.bootstrap_per_peer))
    output_path.write_text(json.dumps(plan, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(
        json.dumps(
            {
                "ok": True,
                "accounts": len(plan["accounts"]),
                "connection_profiles": len(plan["connection_profiles"]),
                "profile_claims": len(plan["profile_claims"]),
                "quarantine": len(plan["quarantine"]),
            },
            ensure_ascii=False,
        )
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
