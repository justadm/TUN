#!/usr/bin/env python3
import re
import sys
from http.cookiejar import CookieJar
from urllib.request import HTTPCookieProcessor, Request, build_opener


def fetch(opener, url):
    req = Request(url, headers={"User-Agent": "portal-admin-smoke/1.0"})
    with opener.open(req, timeout=10) as resp:
        return resp.status, resp.read().decode("utf-8", "replace")


def main():
    token = sys.argv[1]
    base = sys.argv[2]
    opener = build_opener(HTTPCookieProcessor(CookieJar()))

    status, _ = fetch(opener, f"{base}/admin/?token={token}")
    print(f"ADMIN_STATUS={status}")

    status, body = fetch(opener, f"{base}/admin/peers/?token={token}")
    print(f"PEERS_STATUS={status}")
    for pat in [
        "Активные + ожидают",
        "IP (реальный)",
        "Uplink",
        "Последний HS",
        "value='uplink_ams'",
        "value='uplink_nyc'",
        "value='uplink_fra'",
    ]:
        print(f"PEERS_HAS[{pat}]={pat in body}")

    m = re.search(r"/admin/peers/([^/]+)/", body)
    peer = m.group(1) if m else ""
    print(f"PEER={peer}")
    if not peer:
        return 1

    status, body = fetch(opener, f"{base}/admin/peers/{peer}/?token={token}")
    print(f"DETAIL_STATUS={status}")
    for pat in [
        "IP (реальный)",
        "Uplink:",
        "value='uplink_ams'",
        "value='uplink_nyc'",
        "value='uplink_fra'",
        "Label:",
        "Last handshake:",
    ]:
        print(f"DETAIL_HAS[{pat}]={pat in body}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
