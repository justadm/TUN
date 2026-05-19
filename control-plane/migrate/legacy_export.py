#!/usr/bin/env python3
"""Export legacy JsTun control-plane state into one normalized JSON snapshot."""

from __future__ import annotations

import argparse
import json
import sqlite3
import sys
from pathlib import Path
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Export legacy JsTun control-plane state from a state directory."
    )
    parser.add_argument(
        "--state-dir",
        required=True,
        help="Path to legacy state root, for example /var/lib/wg-portal",
    )
    parser.add_argument(
        "--output",
        help="Write normalized JSON snapshot to this file instead of stdout.",
    )
    parser.add_argument(
        "--sample-events",
        type=int,
        default=200,
        help="Number of audit events to include when not using --include-all-events.",
    )
    parser.add_argument(
        "--include-all-events",
        action="store_true",
        help="Include the full audit log instead of a bounded sample.",
    )
    return parser.parse_args()


def read_json_file(path: Path, quarantine: list[dict[str, Any]]) -> Any:
    try:
        return json.loads(path.read_text())
    except FileNotFoundError:
        quarantine.append(
            {"kind": "missing_file", "path": str(path), "reason": "file_not_found"}
        )
        return None
    except json.JSONDecodeError as exc:
        quarantine.append(
            {
                "kind": "invalid_json",
                "path": str(path),
                "reason": str(exc),
            }
        )
        return None


def load_peers(state_dir: Path, quarantine: list[dict[str, Any]]) -> list[dict[str, Any]]:
    peers_dir = state_dir / "peers"
    if not peers_dir.is_dir():
        quarantine.append(
            {
                "kind": "missing_directory",
                "path": str(peers_dir),
                "reason": "peers_directory_not_found",
            }
        )
        return []

    peers: list[dict[str, Any]] = []
    for path in sorted(peers_dir.glob("*.json")):
        raw = read_json_file(path, quarantine)
        if not isinstance(raw, dict):
            quarantine.append(
                {
                    "kind": "invalid_peer_payload",
                    "path": str(path),
                    "reason": "expected_object",
                }
            )
            continue

        peer_id = raw.get("id")
        if not isinstance(peer_id, str) or not peer_id:
            quarantine.append(
                {
                    "kind": "invalid_peer_id",
                    "path": str(path),
                    "reason": "missing_or_non_string_id",
                }
            )
            continue

        peers.append(
            {
                "peer_id": peer_id,
                "file_name": path.name,
                "source": raw,
            }
        )
    return peers


def load_overrides(
    path: Path,
    field_name: str,
    quarantine: list[dict[str, Any]],
) -> dict[str, str]:
    raw = read_json_file(path, quarantine)
    if raw is None:
        return {}
    if not isinstance(raw, dict):
        quarantine.append(
            {
                "kind": "invalid_override_payload",
                "path": str(path),
                "reason": "expected_object",
            }
        )
        return {}

    normalized: dict[str, str] = {}
    for key, value in raw.items():
        if not isinstance(key, str) or not isinstance(value, str):
            quarantine.append(
                {
                    "kind": "invalid_override_entry",
                    "path": str(path),
                    "field": field_name,
                    "peer_id": key,
                    "value": value,
                }
            )
            continue
        normalized[key] = value
    return normalized


def load_audit_events(
    path: Path,
    include_all: bool,
    sample_events: int,
    quarantine: list[dict[str, Any]],
) -> dict[str, Any]:
    if not path.exists():
        quarantine.append(
            {"kind": "missing_file", "path": str(path), "reason": "file_not_found"}
        )
        return {"total_lines": 0, "events": []}

    events: list[dict[str, Any]] = []
    total_lines = 0
    with path.open() as handle:
        for line_number, line in enumerate(handle, start=1):
            total_lines += 1
            if not line.strip():
                continue
            try:
                payload = json.loads(line)
            except json.JSONDecodeError as exc:
                quarantine.append(
                    {
                        "kind": "invalid_audit_line",
                        "path": str(path),
                        "line_number": line_number,
                        "reason": str(exc),
                    }
                )
                continue

            if not isinstance(payload, dict):
                quarantine.append(
                    {
                        "kind": "invalid_audit_payload",
                        "path": str(path),
                        "line_number": line_number,
                        "reason": "expected_object",
                    }
                )
                continue

            if include_all or len(events) < sample_events:
                events.append(payload)

    return {"total_lines": total_lines, "events": events}


def load_billing_records(
    path: Path, quarantine: list[dict[str, Any]]
) -> dict[str, Any]:
    if not path.exists():
        quarantine.append(
            {"kind": "missing_file", "path": str(path), "reason": "file_not_found"}
        )
        return {"schema": {}, "rows": []}

    try:
        conn = sqlite3.connect(path)
    except sqlite3.Error as exc:
        quarantine.append(
            {
                "kind": "billing_db_open_error",
                "path": str(path),
                "reason": str(exc),
            }
        )
        return {"schema": {}, "rows": []}

    conn.row_factory = sqlite3.Row
    try:
        columns = [
            dict(row)
            for row in conn.execute("pragma table_info(billing_orders)").fetchall()
        ]
        rows = [
            dict(row)
            for row in conn.execute(
                """
                select
                  id,
                  peer_id,
                  provider,
                  external_id,
                  amount_rub,
                  currency,
                  status,
                  provider_status,
                  payment_url,
                  purpose,
                  expires_at,
                  paid_at,
                  error_code,
                  error_message,
                  meta_json,
                  created_at,
                  updated_at
                from billing_orders
                order by created_at asc, id asc
                """
            ).fetchall()
        ]
        return {"schema": {"billing_orders": columns}, "rows": rows}
    except sqlite3.Error as exc:
        quarantine.append(
            {
                "kind": "billing_db_query_error",
                "path": str(path),
                "reason": str(exc),
            }
        )
        return {"schema": {}, "rows": []}
    finally:
        conn.close()


def build_snapshot(args: argparse.Namespace) -> dict[str, Any]:
    state_dir = Path(args.state_dir)
    quarantine: list[dict[str, Any]] = []

    peers = load_peers(state_dir, quarantine)
    label_overrides = load_overrides(
        state_dir / "label_overrides.json", "label", quarantine
    )
    type_overrides = load_overrides(
        state_dir / "type_overrides.json", "peer_type", quarantine
    )
    audit = load_audit_events(
        state_dir / "audit.jsonl",
        include_all=args.include_all_events,
        sample_events=args.sample_events,
        quarantine=quarantine,
    )
    billing = load_billing_records(state_dir / "billing.db", quarantine)

    return {
        "format": "jstun-control-plane-legacy-export/v1",
        "state_dir": str(state_dir),
        "summary": {
            "peer_count": len(peers),
            "label_override_count": len(label_overrides),
            "type_override_count": len(type_overrides),
            "audit_events_included": len(audit["events"]),
            "audit_total_lines": audit["total_lines"],
            "billing_record_count": len(billing["rows"]),
            "quarantine_count": len(quarantine),
        },
        "peers": peers,
        "label_overrides": label_overrides,
        "type_overrides": type_overrides,
        "audit": audit,
        "billing": billing,
        "quarantine": quarantine,
    }


def main() -> int:
    args = parse_args()
    snapshot = build_snapshot(args)
    payload = json.dumps(snapshot, ensure_ascii=True, indent=2, sort_keys=True)
    if args.output:
        Path(args.output).write_text(payload + "\n")
    else:
        sys.stdout.write(payload + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
