#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
import sys
from pathlib import Path

SET_NAMES = ["ru4", "ru6", "msk_custom4", "msk_custom6", "openai_v4", "openai_v6"]

DNSMASQ_REMOTE = {
    "20-openai.conf": "/etc/dnsmasq.d/openai.conf",
    "30-ru-streaming.conf": "/etc/dnsmasq.d/ru-streaming.conf",
    "40-wg-portal-telegram.conf": "/etc/dnsmasq.d/wg-portal-telegram.conf",
}

DNSMASQ_DROP_PREFIXES = (
    "no-resolv",
    "server=",
    "interface=",
    "listen-address=",
    "bind-interfaces",
    "cache-size=",
)


def run(cmd: list[str]) -> str:
    return subprocess.check_output(cmd, text=True)


def run_ssh(host: str, remote_cmd: str) -> str:
    return run(["ssh", host, remote_cmd])


def run_ssh_json(host: str, family: str, table: str, set_name: str) -> dict:
    out = run(["ssh", host, f"sudo nft -j list set {family} {table} {set_name}"])
    return json.loads(out)


def normalize_elem(elem: object) -> str:
    if isinstance(elem, str):
        return elem
    if isinstance(elem, dict):
        if "prefix" in elem:
            prefix = elem["prefix"]
            return f"{prefix['addr']}/{prefix['len']}"
        if "elem" in elem:
            return normalize_elem(elem["elem"])
    raise ValueError(f"Unsupported nft element format: {elem!r}")


def extract_elements(payload: dict) -> list[str]:
    for item in payload.get("nftables", []):
        set_obj = item.get("set")
        if set_obj and "elem" in set_obj:
            return [normalize_elem(elem) for elem in set_obj["elem"]]
    return []


def sanitize_dnsmasq_body(body: str) -> str:
    out: list[str] = []
    for line in body.splitlines():
        stripped = line.strip()
        if any(stripped.startswith(prefix) for prefix in DNSMASQ_DROP_PREFIXES):
            continue
        out.append(line)
    sanitized = "\n".join(out).strip()
    return sanitized + ("\n" if sanitized else "")


def sha256_text(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def verify(snapshot_dir: Path, host: str) -> dict[str, object]:
    manifest = json.loads((snapshot_dir / "manifest.json").read_text(encoding="utf-8"))
    result: dict[str, object] = {
        "host": host,
        "version": manifest["version"],
        "tree_sha256": manifest.get("tree_sha256", ""),
        "sets": {},
        "dnsmasq": {},
        "ok": True,
    }

    for set_name in SET_NAMES:
        spec = manifest["sets"][set_name]
        payload = run_ssh_json(host, spec["family"], spec["table"], set_name)
        live = sorted(dict.fromkeys(extract_elements(payload)))
        live_digest = sha256_text("\n".join(live) + ("\n" if live else ""))
        match = live_digest == spec["sha256"]
        result["sets"][set_name] = {
            "expected_sha256": spec["sha256"],
            "live_sha256": live_digest,
            "expected_count": spec["count"],
            "live_count": len(live),
            "match": match,
        }
        if not match:
            result["ok"] = False

    for local_name, remote_path in DNSMASQ_REMOTE.items():
        expected_path = snapshot_dir / "dnsmasq" / local_name
        expected_digest = sha256_text(expected_path.read_text(encoding="utf-8"))
        live_body = sanitize_dnsmasq_body(run_ssh(host, f"sudo cat {remote_path}"))
        live_digest = sha256_text(live_body)
        match = live_digest == expected_digest
        result["dnsmasq"][local_name] = {
            "expected_sha256": expected_digest,
            "live_sha256": live_digest,
            "match": match,
        }
        if not match:
            result["ok"] = False

    return result


def main() -> int:
    parser = argparse.ArgumentParser(description="Shadow verify live geo state against snapshot.")
    parser.add_argument("--snapshot-dir", required=True)
    parser.add_argument("--host", default="edg")
    parser.add_argument("--output", default="")
    args = parser.parse_args()

    result = verify(Path(args.snapshot_dir), args.host)
    body = json.dumps(result, ensure_ascii=True, indent=2) + "\n"
    if args.output:
        Path(args.output).write_text(body, encoding="utf-8")
    sys.stdout.write(body)
    return 0 if result["ok"] else 2


if __name__ == "__main__":
    raise SystemExit(main())
