#!/usr/bin/env python3
import argparse
import base64
import datetime as dt
import fcntl
import ipaddress
import json
import os
import random
import re
import secrets
import subprocess
import sys
from pathlib import Path
import hashlib
import base64 as b64

WG_IFACE = os.getenv("WG_PORTAL_IFACE", "wg0")
WG_CONF = Path(os.getenv("WG_PORTAL_CONF", f"/etc/wireguard/{WG_IFACE}.conf"))
ENV_FILE = Path(os.getenv("WG_PORTAL_ENV", "/etc/wireguard/wg-portal.env"))
STATE_DIR = Path(os.getenv("WG_PORTAL_STATE", "/var/lib/wg-portal"))
WG_BIN = os.getenv("WG_PORTAL_WG_BIN", "wg").strip() or "wg"
QRENCODE_BIN = os.getenv("WG_PORTAL_QRENCODE_BIN", "qrencode").strip() or "qrencode"
EDGE_ID = os.getenv("WG_PORTAL_EDGE_ID", "edg").strip().lower() or "edg"
PEERS_DIR = STATE_DIR / "peers"
LOCK_FILE = STATE_DIR / ".lock"
TTL_SEC = int(os.getenv("WG_PORTAL_TTL_SEC", "3600"))
IP_POOL_START = int(os.getenv("WG_PORTAL_IP_START", "50"))
IP_POOL_END = int(os.getenv("WG_PORTAL_IP_END", "250"))
IP_NET = ipaddress.ip_network(os.getenv("WG_PORTAL_NET", "10.8.0.0/24"))


def configured_ip_net():
    env = load_env()
    raw = env.get("WG_PORTAL_NET", os.getenv("WG_PORTAL_NET", str(IP_NET)))
    try:
        return ipaddress.ip_network(raw)
    except Exception:
        return IP_NET


def configured_pool_bounds():
    env = load_env()
    try:
        start = int(env.get("WG_PORTAL_IP_START", os.getenv("WG_PORTAL_IP_START", str(IP_POOL_START))))
    except Exception:
        start = IP_POOL_START
    try:
        end = int(env.get("WG_PORTAL_IP_END", os.getenv("WG_PORTAL_IP_END", str(IP_POOL_END))))
    except Exception:
        end = IP_POOL_END
    return start, end


def pool_ip_cidr(host_index, ip_net=None):
    net = ip_net or configured_ip_net()
    return f"{net.network_address + host_index}/32"


def run(cmd, input_text=None):
    cmd = [str(x) for x in cmd]
    p = subprocess.run(
        cmd,
        input=input_text,
        text=True,
        capture_output=True,
        check=False,
    )
    if p.returncode != 0:
        raise RuntimeError(f"cmd failed: {' '.join(cmd)}: {p.stderr.strip()}")
    return p.stdout


def utc_now():
    return dt.datetime.now(dt.timezone.utc)


def iso(ts):
    return ts.astimezone(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def load_env():
    out = {}
    if ENV_FILE.exists():
        for line in ENV_FILE.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            v = v.strip()
            if len(v) >= 2 and v[:1] == v[-1:] and v[:1] in ("'", '"'):
                v = v[1:-1]
            out[k.strip()] = v
    return out


def ensure_dirs():
    PEERS_DIR.mkdir(parents=True, exist_ok=True)


def lock_fd():
    ensure_dirs()
    fd = os.open(LOCK_FILE, os.O_CREAT | os.O_RDWR, 0o600)
    fcntl.flock(fd, fcntl.LOCK_EX)
    return fd


def unlock_fd(fd):
    os.close(fd)


def parse_used_ips():
    ip_net = configured_ip_net()
    used = set()
    out = run([WG_BIN, "show", WG_IFACE, "allowed-ips"])
    for line in out.splitlines():
        parts = line.strip().split()
        if len(parts) < 2:
            continue
        cidrs = " ".join(parts[1:]).split(",")
        for c in cidrs:
            c = c.strip()
            try:
                net = ipaddress.ip_network(c, strict=False)
            except Exception:
                continue
            if net.subnet_of(ip_net):
                used.add(f"{net.network_address}/{net.prefixlen}")
    if WG_CONF.exists():
        text = WG_CONF.read_text(encoding="utf-8")
        for m in re.finditer(r"AllowedIPs\s*=\s*([0-9./]+)", text):
            raw = m.group(1)
            try:
                net = ipaddress.ip_network(raw, strict=False)
            except Exception:
                continue
            if net.subnet_of(ip_net):
                used.add(f"{net.network_address}/{net.prefixlen}")
    return used


def alloc_ip():
    ip_net = configured_ip_net()
    pool_start, pool_end = configured_pool_bounds()
    used = parse_used_ips()
    for i in range(pool_start, pool_end + 1):
        ip = pool_ip_cidr(i, ip_net=ip_net)
        if ip not in used:
            return ip
    raise RuntimeError("no free IP in pool")


def gen_keypair_and_psk():
    priv = run([WG_BIN, "genkey"]).strip()
    pub = run([WG_BIN, "pubkey"], input_text=priv).strip()
    psk = run([WG_BIN, "genpsk"]).strip()
    return priv, pub, psk


def server_pubkey():
    return run([WG_BIN, "show", WG_IFACE, "public-key"]).strip()


def read_handshakes():
    out = run([WG_BIN, "show", WG_IFACE, "latest-handshakes"])
    mp = {}
    for line in out.splitlines():
        parts = line.strip().split()
        if len(parts) != 2:
            continue
        k, ts = parts
        try:
            mp[k] = int(ts)
        except ValueError:
            mp[k] = 0
    return mp


def read_transfers():
    out = run([WG_BIN, "show", WG_IFACE, "transfer"])
    mp = {}
    for line in out.splitlines():
        parts = line.strip().split()
        if len(parts) != 3:
            continue
        k, rx, tx = parts
        try:
            mp[k] = (int(rx), int(tx))
        except ValueError:
            mp[k] = (0, 0)
    return mp


def _placeholder_qr_png_b64(conf_text):
    # Valid 1x1 transparent PNG so UI never renders a broken image when qrencode is unavailable.
    return "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aW6QAAAAASUVORK5CYII="


def peer_path(peer_id):
    return PEERS_DIR / f"{peer_id}.json"


def save_peer(meta):
    peer_path(meta["id"]).write_text(json.dumps(meta, ensure_ascii=False, indent=2), encoding="utf-8")


def load_peer(peer_id):
    p = peer_path(peer_id)
    if not p.exists():
        raise RuntimeError(f"peer not found: {peer_id}")
    return json.loads(p.read_text(encoding="utf-8"))


def ensure_lk_token(meta):
    # Токен для ЛК: второй фактор к ID. Нужен для защиты от подбора предсказуемых ID.
    if not meta.get("lk_token"):
        meta["lk_token"] = secrets.token_hex(16)
        meta["lk_token_created_at"] = iso(utc_now())
        save_peer(meta)
    return meta["lk_token"]


def list_peers():
    items = []
    hs = read_handshakes()
    transfers = read_transfers()
    now = utc_now()
    for p in sorted(PEERS_DIR.glob("*.json")):
        try:
            d = json.loads(p.read_text(encoding="utf-8"))
        except Exception:
            continue
        ensure_lk_token(d)
        pub = d.get("public_key", "")
        ts = int(hs.get(pub, 0) or 0)
        if ts > 0 and not d.get("connected_at"):
            d["connected_at"] = iso(dt.datetime.fromtimestamp(ts, tz=dt.timezone.utc))
            if d.get("status") == "pending":
                d["status"] = "active"
            save_peer(d)
        d["last_handshake_unix"] = ts
        d["last_handshake_at"] = iso(dt.datetime.fromtimestamp(ts, tz=dt.timezone.utc)) if ts > 0 else None
        rx, tx = transfers.get(pub, (0, 0))
        d["rx_bytes"] = rx
        d["tx_bytes"] = tx
        if d.get("status") == "pending":
            try:
                created = dt.datetime.fromisoformat(d["created_at"].replace("Z", "+00:00"))
                ttl = int(d.get("ttl_sec") or TTL_SEC)
                d["expires_at"] = iso(created + dt.timedelta(seconds=ttl))
                d["expired"] = now > (created + dt.timedelta(seconds=ttl))
            except Exception:
                d["expires_at"] = None
                d["expired"] = None
        items.append(d)
    return items


def append_peer_block(peer_id, pub, psk, allowed_ip, label):
    marker_b = f"# wg-portal begin {peer_id}"
    marker_e = f"# wg-portal end {peer_id}"
    block = (
        f"\n{marker_b}\n"
        f"# label: {label}\n"
        f"[Peer]\n"
        f"PublicKey = {pub}\n"
        f"PresharedKey = {psk}\n"
        f"AllowedIPs = {allowed_ip}\n"
        f"PersistentKeepalive = 25\n"
        f"{marker_e}\n"
    )
    text = WG_CONF.read_text(encoding="utf-8") if WG_CONF.exists() else ""
    if marker_b in text:
        raise RuntimeError(f"peer marker already exists: {peer_id}")
    WG_CONF.write_text(text.rstrip() + block + "\n", encoding="utf-8")


def remove_peer_block(peer_id):
    if not WG_CONF.exists():
        return
    marker_b = f"# wg-portal begin {peer_id}"
    marker_e = f"# wg-portal end {peer_id}"
    out = []
    inside = False
    changed = False
    for line in WG_CONF.read_text(encoding="utf-8").splitlines(True):
        if line.strip() == marker_b:
            inside = True
            changed = True
            continue
        if inside and line.strip() == marker_e:
            inside = False
            continue
        if not inside:
            out.append(line)
    if changed:
        WG_CONF.write_text("".join(out).rstrip() + "\n", encoding="utf-8")


def make_peer_id():
    # Старый формат pYYYYMMDDHHMMSS#### был предсказуемым.
    # Новый ID должен быть непредсказуемым (секретная ссылка).
    return "p" + secrets.token_hex(8)


def build_client_conf(priv, addr, psk, srv_pub, endpoint, dns):
    return (
        "[Interface]\n"
        f"PrivateKey = {priv}\n"
        f"Address = {addr}\n"
        f"DNS = {dns}\n"
        "MTU = 1280\n\n"
        "[Peer]\n"
        f"PublicKey = {srv_pub}\n"
        f"PresharedKey = {psk}\n"
        f"Endpoint = {endpoint}\n"
        "AllowedIPs = 0.0.0.0/0\n"
        "PersistentKeepalive = 25\n"
    )


def create_peer(label, ttl_sec=None, expire_sec=None):
    env = load_env()
    endpoint = env.get("WG_ENDPOINT", "85.239.44.100:51820")
    dns = env.get("WG_DNS", "1.1.1.1, 8.8.8.8")
    peer_id = make_peer_id()
    allowed_ip = alloc_ip()
    priv, pub, psk = gen_keypair_and_psk()
    psk_tmp = STATE_DIR / f".{peer_id}.psk"
    psk_tmp.write_text(psk + "\n", encoding="utf-8")
    os.chmod(psk_tmp, 0o600)
    try:
        run([WG_BIN, "set", WG_IFACE, "peer", pub, "preshared-key", str(psk_tmp), "allowed-ips", allowed_ip, "persistent-keepalive", "25"])
    finally:
        psk_tmp.unlink(missing_ok=True)

    append_peer_block(peer_id, pub, psk, allowed_ip, label)

    now = utc_now()
    ttl = int(ttl_sec) if ttl_sec is not None else TTL_SEC
    abs_ttl = int(expire_sec) if expire_sec is not None else None
    srv_pub = server_pubkey()
    conf = build_client_conf(priv, allowed_ip, psk, srv_pub, endpoint, dns)
    meta = {
        "id": peer_id,
        "label": label,
        "edge_id": EDGE_ID,
        "status": "pending",
        "created_at": iso(now),
        # expires_at: дедлайн первого handshake (pending timeout)
        "expires_at": iso(now + dt.timedelta(seconds=ttl)),
        "public_key": pub,
        "private_key": priv,
        "preshared_key": psk,
        "allowed_ip": allowed_ip,
        "endpoint": endpoint,
        "server_public_key": srv_pub,
        "config": conf,
        "ttl_sec": ttl,
    }
    if abs_ttl is not None and abs_ttl > 0:
        # absolute_expires_at: абсолютный срок действия профиля (удаление независимо от handshake)
        meta["absolute_ttl_sec"] = abs_ttl
        meta["absolute_expires_at"] = iso(now + dt.timedelta(seconds=abs_ttl))
    save_peer(meta)
    ensure_lk_token(meta)

    png_b64 = None
    qr_is_placeholder = False
    try:
        png = subprocess.run([QRENCODE_BIN, "-t", "PNG", "-o", "-"], input=conf.encode("utf-8"), capture_output=True, check=False)
        if png.returncode == 0 and png.stdout:
            png_b64 = b64.b64encode(png.stdout).decode("ascii")
        else:
            png_b64 = _placeholder_qr_png_b64(conf)
            qr_is_placeholder = True
    except FileNotFoundError:
        png_b64 = _placeholder_qr_png_b64(conf)
        qr_is_placeholder = True

    return {
        "ok": True,
        "id": peer_id,
        "edge_id": EDGE_ID,
        "status": meta["status"],
        "created_at": meta["created_at"],
        "expires_at": meta["expires_at"],
        "absolute_expires_at": meta.get("absolute_expires_at"),
        "absolute_ttl_sec": meta.get("absolute_ttl_sec"),
        "allowed_ip": allowed_ip,
        "endpoint": endpoint,
        "server_public_key": srv_pub,
        "lk_token": meta.get("lk_token"),
        "config": conf,
        "qr_png_b64": png_b64,
        "qr_is_placeholder": qr_is_placeholder,
    }


def remove_peer(peer_id, reason="manual"):
    meta = load_peer(peer_id)
    pub = meta.get("public_key")
    if pub and meta.get("status") not in ("removed", "expired", "blocked"):
        run([WG_BIN, "set", WG_IFACE, "peer", pub, "remove"])
    remove_peer_block(peer_id)
    meta["status"] = "removed" if reason == "manual" else "expired"
    meta["removed_at"] = iso(utc_now())
    meta["remove_reason"] = reason
    save_peer(meta)
    return {"ok": True, "id": peer_id, "status": meta["status"], "remove_reason": reason}


def block_peer(peer_id):
    meta = load_peer(peer_id)
    pub = meta.get("public_key")
    if pub and meta.get("status") not in ("removed", "expired", "blocked"):
        run([WG_BIN, "set", WG_IFACE, "peer", pub, "remove"])
    remove_peer_block(peer_id)
    meta["status"] = "blocked"
    meta["blocked_at"] = iso(utc_now())
    meta["block_reason"] = "manual_admin"
    save_peer(meta)
    return {"ok": True, "id": peer_id, "status": meta["status"], "block_reason": meta["block_reason"]}


def cleanup_peers():
    hs = read_handshakes()
    now = utc_now()
    removed = []
    for p in sorted(PEERS_DIR.glob("*.json")):
        try:
            d = json.loads(p.read_text(encoding="utf-8"))
        except Exception:
            continue
        if d.get("status") in ("removed", "expired", "blocked"):
            continue

        # Абсолютный TTL: удаляем профиль независимо от handshake (если включено).
        try:
            created = dt.datetime.fromisoformat(d["created_at"].replace("Z", "+00:00"))
        except Exception:
            continue
        abs_ttl = int(d.get("absolute_ttl_sec") or 0)
        if abs_ttl > 0:
            if (now - created).total_seconds() >= abs_ttl:
                rid = d["id"]
                remove_peer(rid, reason="ttl_expired")
                removed.append(rid)
                continue

        if d.get("status") != "pending":
            continue
        pub = d.get("public_key", "")
        ts = int(hs.get(pub, 0) or 0)
        if ts > 0:
            d["status"] = "active"
            d["connected_at"] = iso(dt.datetime.fromtimestamp(ts, tz=dt.timezone.utc))
            save_peer(d)
            continue
        ttl = int(d.get("ttl_sec") or TTL_SEC)
        if (now - created).total_seconds() >= ttl:
            rid = d["id"]
            remove_peer(rid, reason="ttl_no_handshake")
            removed.append(rid)
    return {"ok": True, "removed": removed, "removed_count": len(removed)}


def cmd_create(args):
    fd = lock_fd()
    try:
        out = create_peer(args.label, ttl_sec=args.ttl_sec, expire_sec=args.expire_sec)
        print(json.dumps(out, ensure_ascii=False))
    finally:
        unlock_fd(fd)


def cmd_list(args):
    fd = lock_fd()
    try:
        print(json.dumps({"ok": True, "items": list_peers()}, ensure_ascii=False))
    finally:
        unlock_fd(fd)


def cmd_cleanup(args):
    fd = lock_fd()
    try:
        print(json.dumps(cleanup_peers(), ensure_ascii=False))
    finally:
        unlock_fd(fd)


def cmd_remove(args):
    fd = lock_fd()
    try:
        print(json.dumps(remove_peer(args.id, reason="manual"), ensure_ascii=False))
    finally:
        unlock_fd(fd)


def cmd_block(args):
    fd = lock_fd()
    try:
        print(json.dumps(block_peer(args.id), ensure_ascii=False))
    finally:
        unlock_fd(fd)


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)

    p_create = sub.add_parser("create")
    p_create.add_argument("--label", default="web")
    p_create.add_argument("--ttl-sec", type=int, default=None, help="pending timeout (handshake deadline) in seconds")
    p_create.add_argument("--expire-sec", type=int, default=None, help="absolute TTL in seconds (remove peer regardless of handshake)")
    p_create.set_defaults(func=cmd_create)

    p_list = sub.add_parser("list")
    p_list.set_defaults(func=cmd_list)

    p_cleanup = sub.add_parser("cleanup")
    p_cleanup.set_defaults(func=cmd_cleanup)

    p_remove = sub.add_parser("remove")
    p_remove.add_argument("--id", required=True)
    p_remove.set_defaults(func=cmd_remove)

    p_block = sub.add_parser("block")
    p_block.add_argument("--id", required=True)
    p_block.set_defaults(func=cmd_block)

    args = ap.parse_args()
    try:
        args.func(args)
    except Exception as e:
        print(json.dumps({"ok": False, "error": str(e)}, ensure_ascii=False))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
