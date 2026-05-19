#!/usr/bin/env python3
import base64
import hashlib
import json
import os
import secrets
import sys
from pathlib import Path

STATE_PATH = Path(os.getenv("WG_STUB_STATE", "/var/lib/jstun-shadow/runtime/wgstub-state.json"))
SERVER_PUBLIC_KEY = os.getenv("WG_STUB_SERVER_PUBLIC_KEY", "wgstub-server-public-key")


def load_state():
    if not STATE_PATH.exists():
        return {"server_public_key": SERVER_PUBLIC_KEY, "peers": {}}
    try:
        return json.loads(STATE_PATH.read_text(encoding="utf-8"))
    except Exception:
        return {"server_public_key": SERVER_PUBLIC_KEY, "peers": {}}


def save_state(state):
    STATE_PATH.parent.mkdir(parents=True, exist_ok=True)
    STATE_PATH.write_text(json.dumps(state, ensure_ascii=False, indent=2, sort_keys=True), encoding="utf-8")


def genkey():
    raw = secrets.token_bytes(32)
    print(base64.b64encode(raw).decode("ascii"))


def pubkey():
    priv = sys.stdin.read().strip()
    digest = hashlib.sha256(priv.encode("utf-8")).digest()
    print(base64.b64encode(digest).decode("ascii"))


def genpsk():
    raw = secrets.token_bytes(32)
    print(base64.b64encode(raw).decode("ascii"))


def show_allowed_ips(state):
    for pub, data in sorted(state.get("peers", {}).items()):
        allowed_ip = str(data.get("allowed_ip") or "").strip()
        if allowed_ip:
            print(f"{pub}\t{allowed_ip}")


def show_public_key(state):
    print(state.get("server_public_key") or SERVER_PUBLIC_KEY)


def show_latest_handshakes(state):
    for pub, data in sorted(state.get("peers", {}).items()):
        print(f"{pub}\t{int(data.get('latest_handshake', 0) or 0)}")


def show_transfer(state):
    for pub, data in sorted(state.get("peers", {}).items()):
        print(f"{pub}\t{int(data.get('rx_bytes', 0) or 0)}\t{int(data.get('tx_bytes', 0) or 0)}")


def set_peer(state, argv):
    if len(argv) < 5 or argv[2] != "peer":
        raise SystemExit("unsupported wg set syntax")
    pub = argv[3]
    rest = argv[4:]
    peers = state.setdefault("peers", {})
    if rest == ["remove"]:
        peers.pop(pub, None)
        save_state(state)
        return
    allowed_ip = None
    for idx, token in enumerate(rest):
        if token == "allowed-ips" and idx + 1 < len(rest):
            allowed_ip = rest[idx + 1]
            break
    if not allowed_ip:
        raise SystemExit("wgstub: allowed-ips missing")
    peers[pub] = {
        "allowed_ip": allowed_ip,
        "latest_handshake": int(peers.get(pub, {}).get("latest_handshake", 0) or 0),
        "rx_bytes": int(peers.get(pub, {}).get("rx_bytes", 0) or 0),
        "tx_bytes": int(peers.get(pub, {}).get("tx_bytes", 0) or 0),
    }
    save_state(state)


def main():
    argv = sys.argv[1:]
    if not argv:
        raise SystemExit("usage: wgstub <command>")
    cmd = argv[0]
    if cmd == "genkey":
        genkey()
        return
    if cmd == "pubkey":
        pubkey()
        return
    if cmd == "genpsk":
        genpsk()
        return
    state = load_state()
    if cmd == "show":
        if len(argv) < 3:
            raise SystemExit("unsupported wg show syntax")
        sub = argv[2]
        if sub == "allowed-ips":
            show_allowed_ips(state)
            return
        if sub == "public-key":
            show_public_key(state)
            return
        if sub == "latest-handshakes":
            show_latest_handshakes(state)
            return
        if sub == "transfer":
            show_transfer(state)
            return
        raise SystemExit("unsupported wg show subcommand")
    if cmd == "set":
        set_peer(state, argv)
        return
    raise SystemExit("unsupported command")


if __name__ == "__main__":
    main()
