#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
from datetime import datetime, timezone
from pathlib import Path


SET_SPECS = [
    ("inet", "msk_geo", "ru4"),
    ("inet", "msk_geo", "ru6"),
    ("inet", "msk_geo", "msk_custom4"),
    ("inet", "msk_geo", "msk_custom6"),
    ("inet", "msk_geo", "openai_v4"),
    ("inet", "msk_geo", "openai_v6"),
]


def run_ssh_json(host: str, family: str, table: str, set_name: str) -> dict:
    cmd = [
        "ssh",
        host,
        f"sudo nft -j list set {family} {table} {set_name}",
    ]
    out = subprocess.check_output(cmd, text=True)
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


def sha256_lines(lines: list[str]) -> str:
    body = "\n".join(lines).encode()
    return hashlib.sha256(body).hexdigest()


def main() -> int:
    parser = argparse.ArgumentParser(description="Snapshot EDG geo nft sets for VRN sync.")
    parser.add_argument("--host", default="edg")
    parser.add_argument(
        "--output-dir",
        default="/Users/just/projects/TUN/.docs/vrn/geo_snapshot/edg/latest",
    )
    args = parser.parse_args()

    outdir = Path(args.output_dir)
    outdir.mkdir(parents=True, exist_ok=True)

    manifest: dict[str, object] = {
        "source_host": args.host,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "sets": {},
    }

    for family, table, set_name in SET_SPECS:
        payload = run_ssh_json(args.host, family, table, set_name)
        elems = extract_elements(payload)
        elems = sorted(dict.fromkeys(elems))
        target = outdir / f"{set_name}.txt"
        target.write_text("\n".join(elems) + ("\n" if elems else ""), encoding="utf-8")
        manifest["sets"][set_name] = {
            "family": family,
            "table": table,
            "count": len(elems),
            "sha256": sha256_lines(elems),
            "file": target.name,
        }

    (outdir / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=True, indent=2) + "\n", encoding="utf-8"
    )
    print(outdir)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
