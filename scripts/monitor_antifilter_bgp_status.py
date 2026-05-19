#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import subprocess
from dataclasses import dataclass, asdict
from datetime import datetime, timezone
from typing import Any


@dataclass
class HostStatus:
    host: str
    ok: bool
    frr_active: bool = False
    neighbor: str = ""
    state: str = ""
    accepted_prefixes: int = 0
    msg_rcvd: int = 0
    msg_sent: int = 0
    error: str = ""


def run_ssh(host: str, command: str, timeout_sec: int = 15) -> str:
    proc = subprocess.run(
        ["ssh", host, command],
        text=True,
        capture_output=True,
        timeout=timeout_sec,
        check=False,
    )
    if proc.returncode != 0:
        raise RuntimeError((proc.stderr or proc.stdout or f"ssh failed rc={proc.returncode}").strip())
    return proc.stdout


def collect_host(host: str, neighbor: str) -> HostStatus:
    status = HostStatus(host=host, ok=False, neighbor=neighbor)
    try:
        active = run_ssh(host, "sudo systemctl is-active frr").strip()
        status.frr_active = active == "active"
        if not status.frr_active:
            status.error = f"frr_not_active:{active}"
            return status
        raw = run_ssh(host, "sudo vtysh -c 'show bgp ipv4 unicast summary json'")
        payload = json.loads(raw)
        peers: dict[str, Any] = payload.get("peers", {})
        peer = peers.get(neighbor)
        if not peer:
            status.error = "neighbor_not_found"
            return status
        status.state = str(peer.get("state", ""))
        status.accepted_prefixes = int(peer.get("pfxRcd", 0) or 0)
        status.msg_rcvd = int(peer.get("msgRcvd", 0) or 0)
        status.msg_sent = int(peer.get("msgSent", 0) or 0)
        status.ok = status.state in ("Established", "NoNeg")
        return status
    except Exception as exc:
        status.error = str(exc)
        return status


def main() -> int:
    ap = argparse.ArgumentParser(description="Collect antifilter BGP status from gateways via SSH.")
    ap.add_argument("--hosts", required=True, help="Comma-separated SSH hosts, e.g. vrn,edg")
    ap.add_argument("--neighbor", default="45.154.73.71")
    ap.add_argument("--output", default="")
    args = ap.parse_args()

    hosts = [h.strip() for h in args.hosts.split(",") if h.strip()]
    items = [collect_host(h, args.neighbor) for h in hosts]
    out = {
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "neighbor": args.neighbor,
        "items": [asdict(x) for x in items],
    }
    raw = json.dumps(out, ensure_ascii=True, indent=2) + "\n"
    if args.output:
        with open(args.output, "w", encoding="utf-8") as f:
            f.write(raw)
    else:
        print(raw, end="")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
