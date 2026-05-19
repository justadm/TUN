#!/usr/bin/env python3
from __future__ import annotations

import argparse
import hashlib
import json
import shutil
import subprocess
from datetime import datetime, timezone
from pathlib import Path

SET_SPECS = [
    {"family": "inet", "table": "msk_geo", "name": "ru4"},
    {"family": "inet", "table": "msk_geo", "name": "ru6"},
    {"family": "inet", "table": "msk_geo", "name": "msk_custom4"},
    {"family": "inet", "table": "msk_geo", "name": "msk_custom6"},
    {"family": "inet", "table": "msk_geo", "name": "openai_v4"},
    {"family": "inet", "table": "msk_geo", "name": "openai_v6"},
]

DNSMASQ_SPECS = [
    {
        "remote_path": "/etc/dnsmasq.d/openai.conf",
        "target_name": "20-openai.conf",
    },
    {
        "remote_path": "/etc/dnsmasq.d/ru-streaming.conf",
        "target_name": "30-ru-streaming.conf",
    },
    {
        "remote_path": "/etc/dnsmasq.d/wg-portal-telegram.conf",
        "target_name": "40-wg-portal-telegram.conf",
    },
]

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


def sha256_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def read_set_lines(path: Path) -> list[str]:
    if not path.exists():
        return []
    out: list[str] = []
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        out.append(line)
    return out


def merge_unique_sorted(*chunks: list[str]) -> list[str]:
    merged: dict[str, None] = {}
    for chunk in chunks:
        for item in chunk:
            merged[item] = None
    return sorted(merged)


def sha256_tree(entries: list[tuple[str, str]]) -> str:
    payload = "\n".join(f"{name}:{digest}" for name, digest in sorted(entries))
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


def sanitize_dnsmasq_body(body: str) -> str:
    out: list[str] = []
    for line in body.splitlines():
        stripped = line.strip()
        if any(stripped.startswith(prefix) for prefix in DNSMASQ_DROP_PREFIXES):
            continue
        out.append(line)
    sanitized = "\n".join(out).strip()
    return sanitized + ("\n" if sanitized else "")


def snapshot_now() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H-%M-%SZ")


def build_snapshot(source_host: str, out_root: Path, manual_root: Path | None = None) -> Path:
    version = snapshot_now()
    version_dir = out_root / "snapshots" / version
    latest_dir = out_root / "snapshots" / "latest"
    manual_root = manual_root or (out_root / "manual")
    dnsmasq_dir = version_dir / "dnsmasq"
    dnsmasq_dir.mkdir(parents=True, exist_ok=True)

    manifest: dict[str, object] = {
        "version": version,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "source_host": source_host,
        "source_mode": "hybrid" if manual_root.exists() else "edg-import",
        "manual_root": str(manual_root),
        "sets": {},
        "dnsmasq": {},
    }
    tree_entries: list[tuple[str, str]] = []

    for spec in SET_SPECS:
        family = spec["family"]
        table = spec["table"]
        set_name = spec["name"]
        payload = run_ssh_json(source_host, family, table, set_name)
        imported = sorted(dict.fromkeys(extract_elements(payload)))
        manual_file = manual_root / "sets" / f"{set_name}.txt"
        manual = read_set_lines(manual_file)
        elems = merge_unique_sorted(imported, manual)
        target = version_dir / f"{set_name}.txt"
        target.write_text("\n".join(elems) + ("\n" if elems else ""), encoding="utf-8")
        digest = sha256_file(target)
        manifest["sets"][set_name] = {
            "family": family,
            "table": table,
            "import_count": len(imported),
            "manual_count": len(manual),
            "count": len(elems),
            "sha256": digest,
            "file": target.name,
        }
        tree_entries.append((target.name, digest))

    managed_dnsmasq: dict[str, dict[str, str]] = {}
    for spec in DNSMASQ_SPECS:
        remote_path = spec["remote_path"]
        target_name = spec["target_name"]
        body = run_ssh(source_host, f"sudo cat {remote_path}")
        target = dnsmasq_dir / target_name
        target.write_text(sanitize_dnsmasq_body(body), encoding="utf-8")
        managed_dnsmasq[target.name] = {
            "remote_path": remote_path,
            "source_mode": "edg-import",
            "sha256": sha256_file(target),
            "file": f"dnsmasq/{target.name}",
        }
        tree_entries.append((f"dnsmasq/{target.name}", managed_dnsmasq[target.name]["sha256"]))

    manual_dnsmasq_dir = manual_root / "dnsmasq"
    if manual_dnsmasq_dir.exists():
        for manual_file in sorted(manual_dnsmasq_dir.glob("*.conf")):
            target = dnsmasq_dir / manual_file.name
            shutil.copy2(manual_file, target)
            managed_dnsmasq[target.name] = {
                "remote_path": None,
                "source_mode": "manual",
                "sha256": sha256_file(target),
                "file": f"dnsmasq/{target.name}",
            }
            tree_entries = [entry for entry in tree_entries if entry[0] != f"dnsmasq/{target.name}"]
            tree_entries.append((f"dnsmasq/{target.name}", managed_dnsmasq[target.name]["sha256"]))

    manifest["dnsmasq"] = dict(sorted(managed_dnsmasq.items()))
    manifest["tree_sha256"] = sha256_tree(tree_entries)

    (version_dir / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=True, indent=2) + "\n", encoding="utf-8"
    )

    if latest_dir.exists() or latest_dir.is_symlink():
        if latest_dir.is_symlink() or latest_dir.is_file():
            latest_dir.unlink()
        else:
            shutil.rmtree(latest_dir)
    shutil.copytree(version_dir, latest_dir)
    return version_dir


def main() -> int:
    parser = argparse.ArgumentParser(description="Build geo snapshot for EDG/VRN sync.")
    parser.add_argument("--source-host", default="edg")
    parser.add_argument(
        "--output-root",
        default="/Users/just/projects/TUN/.docs/vrn/geo_sync",
    )
    parser.add_argument("--manual-root", default=None)
    args = parser.parse_args()

    version_dir = build_snapshot(
        args.source_host,
        Path(args.output_root),
        Path(args.manual_root) if args.manual_root else None,
    )
    print(version_dir)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
