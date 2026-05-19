#!/usr/bin/env python3
from __future__ import annotations

import json
import http.cookiejar
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import urllib.parse
from pathlib import Path


ROOT = Path(__file__).resolve().parent


def _free_port() -> int:
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return int(port)


def _request(url: str, *, method: str = "GET", payload: dict | None = None, headers: dict[str, str] | None = None) -> tuple[int, dict, dict]:
    data = None if payload is None else json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    for key, value in (headers or {}).items():
        req.add_header(key, value)
    try:
        with urllib.request.urlopen(req, timeout=5) as resp:
            body = json.loads(resp.read().decode("utf-8"))
            return resp.status, body, dict(resp.headers.items())
    except urllib.error.HTTPError as exc:
        body = json.loads(exc.read().decode("utf-8"))
        return exc.code, body, dict(exc.headers.items())


def _html_request(opener, url: str, *, payload: dict[str, str] | None = None) -> tuple[str, str]:
    if payload is None:
        req = urllib.request.Request(url, method="GET")
    else:
        data = urllib.parse.urlencode(payload).encode("utf-8")
        req = urllib.request.Request(url, data=data, method="POST")
        req.add_header("Content-Type", "application/x-www-form-urlencoded")
    with opener.open(req, timeout=5) as resp:
        return resp.geturl(), resp.read().decode("utf-8")


def main() -> int:
    with tempfile.TemporaryDirectory(prefix="account-lk-http-") as tmp:
        db_path = Path(tmp) / "account_lk.sqlite3"
        port = _free_port()
        server = subprocess.Popen(
            [sys.executable, str(ROOT / "http_app.py"), "--db", str(db_path), "--port", str(port)],
            cwd=str(ROOT),
        )
        try:
            deadline = time.time() + 10
            while time.time() < deadline:
                try:
                    status, _, _ = _request(f"http://127.0.0.1:{port}/healthz")
                    if status == 200:
                        break
                except Exception:
                    time.sleep(0.2)
            cookie_jar = http.cookiejar.CookieJar()
            opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookie_jar))
            html_login_url, html_login_page = _html_request(opener, f"http://127.0.0.1:{port}/account/login")
            html_account_url, html_account_page = _html_request(
                opener,
                f"http://127.0.0.1:{port}/account/login",
                payload={
                    "provider": "telegram",
                    "subject": "tg:form-1",
                    "email": "form@example.com",
                    "display_name": "Form Demo",
                },
            )
            html_profile_url, html_profile_page = _html_request(opener, f"http://127.0.0.1:{port}/account/profiles/new")
            html_after_create_url, html_after_create_page = _html_request(
                opener,
                f"http://127.0.0.1:{port}/account/profiles/new",
                payload={
                    "display_label": "Form Profile",
                    "system_label": "form-profile",
                },
            )
            html_claim_url, html_claim_page = _html_request(opener, f"http://127.0.0.1:{port}/account/claims/new")
            status, body, headers = _request(
                f"http://127.0.0.1:{port}/auth/identity-login",
                method="POST",
                payload={
                    "provider": "telegram",
                    "subject": "tg:999",
                    "email": "http@example.com",
                    "display_name": "HTTP Demo",
                    "profile_payload": {"username": "http-demo"},
                },
            )
            cookie = headers.get("Set-Cookie", "").split(";", 1)[0]
            status2, body2, _ = _request(
                f"http://127.0.0.1:{port}/lk/profiles",
                method="POST",
                payload={"display_label": "HTTP Profile", "system_label": "http-profile", "legacy_peer_id": "p-http-001", "status": "active"},
                headers={"Cookie": cookie},
            )
            status3, body3, _ = _request(
                f"http://127.0.0.1:{port}/lk/account",
                headers={"Cookie": cookie},
            )
            print(
                json.dumps(
                    {
                        "ok": status == 200 and status2 == 201 and status3 == 200 and "/account/" in html_account_url,
                        "login_ok": status == 200,
                        "create_profile_ok": status2 == 201,
                        "account_payload_ok": status3 == 200,
                        "html_login_ok": "/account/login" in html_login_url and "Вход в аккаунт" in html_login_page,
                        "html_account_ok": "/account/" in html_account_url and "Аккаунт" in html_account_page,
                        "html_profile_form_ok": "/account/profiles/new" in html_profile_url and "Новый профиль подключения" in html_profile_page,
                        "html_profile_create_ok": "/account/" in html_after_create_url and "Профили подключения" in html_after_create_page,
                        "html_claim_form_ok": "/account/claims/new" in html_claim_url and "Подключить существующий профиль" in html_claim_page,
                        "profiles_count": len(body3.get("data", {}).get("connection_profiles", [])),
                        "account_id": body.get("account", {}).get("account_id", ""),
                        "profile_id": body2.get("profile", {}).get("connection_profile_id", ""),
                    },
                    ensure_ascii=False,
                )
            )
            return 0
        finally:
            server.terminate()
            try:
                server.wait(timeout=3)
            except subprocess.TimeoutExpired:
                server.kill()


if __name__ == "__main__":
    sys.exit(main())
