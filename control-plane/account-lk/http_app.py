#!/usr/bin/env python3
from __future__ import annotations

import json
from dataclasses import asdict
from http import HTTPStatus
from http.cookies import SimpleCookie
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, quote, urlparse

from service import AccountLKService
from sqlite_store import SqliteAccountLKStore


def _json_default(value):
    if hasattr(value, "isoformat"):
        return value.isoformat()
    raise TypeError(f"unsupported json type: {type(value)!r}")


class AccountLKHTTPHandler(BaseHTTPRequestHandler):
    server: "AccountLKHTTPServer"

    def _send_json(self, payload: dict, status: int = 200, *, set_cookie: str | None = None) -> None:
        body = json.dumps(payload, ensure_ascii=False, default=_json_default).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        if set_cookie:
            self.send_header("Set-Cookie", set_cookie)
        self.end_headers()
        self.wfile.write(body)

    def _send_html(self, html: str, status: int = 200, *, set_cookie: str | None = None, location: str | None = None) -> None:
        body = html.encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        if set_cookie:
            self.send_header("Set-Cookie", set_cookie)
        if location:
            self.send_header("Location", location)
        self.end_headers()
        self.wfile.write(body)

    def _redirect(self, location: str, *, set_cookie: str | None = None) -> None:
        self._send_html("", status=303, set_cookie=set_cookie, location=location)

    def _read_json(self) -> dict:
        size = int(self.headers.get("Content-Length", "0") or "0")
        raw = self.rfile.read(size) if size > 0 else b"{}"
        return json.loads(raw.decode("utf-8") or "{}")

    def _read_form(self) -> dict[str, str]:
        size = int(self.headers.get("Content-Length", "0") or "0")
        raw = self.rfile.read(size) if size > 0 else b""
        payload = parse_qs(raw.decode("utf-8"), keep_blank_values=True)
        return {key: values[-1] if values else "" for key, values in payload.items()}

    def _session_token(self) -> str:
        auth = self.headers.get("Authorization", "")
        if auth.startswith("Bearer "):
            return auth[7:].strip()
        cookie = SimpleCookie(self.headers.get("Cookie", ""))
        morsel = cookie.get("account_lk_session")
        return morsel.value if morsel else ""

    def _account(self):
        token = self._session_token()
        if not token:
            return None
        return self.server.service.resolve_account_by_session(token)

    def _html_page(self, title: str, body: str) -> str:
        return f"""<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{title}</title>
  <style>
    body {{ font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background:#eef4ff; color:#1d2738; margin:0; }}
    .wrap {{ max-width: 960px; margin: 0 auto; padding: 32px 20px 64px; }}
    .card {{ background:#fff; border:1px solid #d4e1f5; border-radius:24px; padding:24px; margin: 0 0 20px; }}
    h1,h2 {{ color:#4caf50; margin:0 0 16px; }}
    h3 {{ margin:16px 0 8px; }}
    .row {{ display:flex; gap:16px; flex-wrap:wrap; }}
    .col {{ flex:1 1 320px; }}
    .meta {{ color:#60708d; }}
    .pill {{ display:inline-block; background:#eef3fb; border-radius:8px; padding:2px 8px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }}
    .btn {{ display:inline-block; border:2px solid #1f2c42; color:#1f2c42; background:#fff; border-radius:14px; padding:10px 16px; text-decoration:none; font-weight:600; }}
    .btn + .btn {{ margin-left:10px; }}
    .btn.green {{ border-color:#4caf50; color:#4caf50; }}
    input {{ width:100%; box-sizing:border-box; border:2px solid #1f2c42; border-radius:14px; padding:12px 14px; font-size:16px; }}
    label {{ display:block; margin:0 0 10px; font-weight:600; }}
    form > * + * {{ margin-top:14px; }}
    ul {{ margin: 8px 0 0 18px; }}
    .nav {{ margin-bottom:20px; }}
  </style>
</head>
<body>
  <div class="wrap">
    <div class="nav">
      <a class="btn green" href="/account/">Аккаунт</a>
      <a class="btn" href="/account/profiles/new">Новый профиль</a>
      <a class="btn" href="/account/claims/new">Подключить существующий профиль</a>
    </div>
    {body}
  </div>
</body>
</html>"""

    def _render_login_page(self) -> str:
        return self._html_page(
            "Вход в аккаунт",
            """
            <div class="card">
              <h1>Вход в аккаунт</h1>
              <p class="meta">Это первый bridge для нового account-based LK. Вход пока технический: через provider + subject.</p>
              <form method="post" action="/account/login">
                <label>Provider
                  <input name="provider" value="telegram" />
                </label>
                <label>Subject
                  <input name="subject" placeholder="tg:12345" />
                </label>
                <label>Email
                  <input name="email" placeholder="user@example.com" />
                </label>
                <label>Display name
                  <input name="display_name" placeholder="Demo User" />
                </label>
                <button class="btn green" type="submit">Войти</button>
              </form>
            </div>
            """,
        )

    def _render_account_page(self, payload: dict) -> str:
        account = payload["account"]
        profiles = payload["connection_profiles"]
        identities = payload["identities"]
        balance = payload["balance"]
        profile_items = "".join(
            f"<li><strong>{p['admin_label'] or p['display_label'] or p['legacy_peer_id'] or p['connection_profile_id']}</strong> "
            f"<span class='pill'>{p['status']}</span> "
            f"<span class='meta'>{p['legacy_peer_id'] or 'new profile'}</span></li>"
            for p in profiles
        ) or "<li class='meta'>Пока нет профилей подключения.</li>"
        identity_items = "".join(
            f"<li><strong>{i['provider']}</strong> <span class='pill'>{i['subject']}</span></li>"
            for i in identities
        ) or "<li class='meta'>Пока нет привязанных identity.</li>"
        body = f"""
        <div class="card">
          <h1>Аккаунт</h1>
          <div class="row">
            <div class="col">
              <h3>{account['display_name'] or 'Без имени'}</h3>
              <p class="meta">account_id: <span class="pill">{account['account_id']}</span></p>
              <p>Email: <span class="pill">{account['email'] or '—'}</span></p>
              <p>Статус: <span class="pill">{account['status']}</span></p>
            </div>
            <div class="col">
              <h3>Внутренний счёт</h3>
              <p>Баланс: <span class="pill">{balance['available_minor']}</span> {balance['currency']}</p>
            </div>
          </div>
        </div>
        <div class="card">
          <h2>Identity</h2>
          <ul>{identity_items}</ul>
        </div>
        <div class="card">
          <h2>Профили подключения</h2>
          <ul>{profile_items}</ul>
        </div>
        """
        return self._html_page("Аккаунт", body)

    def _render_profile_new_page(self) -> str:
        return self._html_page(
            "Новый профиль",
            """
            <div class="card">
              <h1>Новый профиль подключения</h1>
              <form method="post" action="/account/profiles/new">
                <label>Название профиля
                  <input name="display_label" placeholder="MacBook / iPhone / Work laptop" />
                </label>
                <label>Технический label
                  <input name="system_label" placeholder="macbook-main" />
                </label>
                <button class="btn green" type="submit">Создать профиль</button>
              </form>
            </div>
            """,
        )

    def _render_claim_new_page(self) -> str:
        return self._html_page(
            "Подключить существующий профиль",
            """
            <div class="card">
              <h1>Подключить существующий профиль</h1>
              <p class="meta">Bridge для старого WG-first LK. Здесь можно привязать legacy peer к account.</p>
              <form method="post" action="/account/claims/new">
                <label>Legacy peer ID
                  <input name="legacy_peer_id" placeholder="p123..." />
                </label>
                <label>Отображаемое название
                  <input name="display_label" placeholder="Старый iPhone профиль" />
                </label>
                <button class="btn green" type="submit">Привязать профиль</button>
              </form>
            </div>
            """,
        )

    def do_GET(self) -> None:
        parsed = urlparse(self.path)
        if parsed.path == "/healthz":
            self._send_json({"ok": True})
            return
        if parsed.path == "/account/login":
            self._send_html(self._render_login_page())
            return
        if parsed.path == "/account/":
            account = self._account()
            if account is None:
                self._redirect("/account/login")
                return
            self._send_html(self._render_account_page(self.server.service.account_home_payload(account.account_id)))
            return
        if parsed.path == "/account/profiles/new":
            if self._account() is None:
                self._redirect("/account/login")
                return
            self._send_html(self._render_profile_new_page())
            return
        if parsed.path == "/account/claims/new":
            if self._account() is None:
                self._redirect("/account/login")
                return
            self._send_html(self._render_claim_new_page())
            return
        if parsed.path == "/lk/account":
            account = self._account()
            if account is None:
                self._send_json({"ok": False, "error": "unauthorized"}, HTTPStatus.UNAUTHORIZED)
                return
            self._send_json({"ok": True, "data": self.server.service.account_home_payload(account.account_id)})
            return
        if parsed.path == "/lk/profiles":
            account = self._account()
            if account is None:
                self._send_json({"ok": False, "error": "unauthorized"}, HTTPStatus.UNAUTHORIZED)
                return
            payload = self.server.service.account_home_payload(account.account_id)
            self._send_json({"ok": True, "profiles": payload["connection_profiles"]})
            return
        if parsed.path == "/auth/session":
            account = self._account()
            if account is None:
                self._send_json({"ok": False, "authenticated": False}, HTTPStatus.UNAUTHORIZED)
                return
            self._send_json({"ok": True, "authenticated": True, "account": asdict(account)})
            return
        self._send_json({"ok": False, "error": "not_found"}, HTTPStatus.NOT_FOUND)

    def do_POST(self) -> None:
        parsed = urlparse(self.path)
        content_type = self.headers.get("Content-Type", "")
        is_form = content_type.startswith("application/x-www-form-urlencoded")
        body = self._read_form() if is_form else self._read_json()
        if parsed.path == "/account/login":
            account, session = self.server.service.register_or_login_by_identity(
                provider=str(body.get("provider") or ""),
                subject=str(body.get("subject") or ""),
                email=str(body.get("email") or ""),
                phone=str(body.get("phone") or ""),
                display_name=str(body.get("display_name") or ""),
                profile_payload={},
                ip=self.client_address[0],
                ua=self.headers.get("User-Agent", ""),
            )
            cookie = f"account_lk_session={session.session_token}; Path=/; HttpOnly; SameSite=Lax"
            self._redirect("/account/", set_cookie=cookie)
            return
        if parsed.path == "/account/profiles/new":
            account = self._account()
            if account is None:
                self._redirect("/account/login")
                return
            profile = self.server.service.create_profile_stub(
                account_id=account.account_id,
                display_label=str(body.get("display_label") or "").strip(),
                system_label=str(body.get("system_label") or "").strip(),
                status="pending",
            )
            self._redirect(f"/account/?created_profile={quote(profile.connection_profile_id, safe='')}")
            return
        if parsed.path == "/account/claims/new":
            account = self._account()
            if account is None:
                self._redirect("/account/login")
                return
            self.server.service.attach_legacy_profile(
                account_id=account.account_id,
                legacy_peer_id=str(body.get("legacy_peer_id") or "").strip(),
                display_label=str(body.get("display_label") or "").strip(),
                claim_method="operator_attach",
                proof_payload={"source": "account_bridge_form"},
            )
            self._redirect(f"/account/?attached_legacy_peer={quote(str(body.get('legacy_peer_id') or '').strip(), safe='')}")
            return
        if parsed.path == "/auth/identity-login":
            account, session = self.server.service.register_or_login_by_identity(
                provider=str(body.get("provider") or ""),
                subject=str(body.get("subject") or ""),
                email=str(body.get("email") or ""),
                phone=str(body.get("phone") or ""),
                display_name=str(body.get("display_name") or ""),
                profile_payload=body.get("profile_payload") or {},
                ip=self.client_address[0],
                ua=self.headers.get("User-Agent", ""),
            )
            cookie = f"account_lk_session={session.session_token}; Path=/; HttpOnly; SameSite=Lax"
            self._send_json(
                {"ok": True, "account": asdict(account), "session_id": session.session_id},
                set_cookie=cookie,
            )
            return
        if parsed.path == "/lk/profiles":
            account = self._account()
            if account is None:
                self._send_json({"ok": False, "error": "unauthorized"}, HTTPStatus.UNAUTHORIZED)
                return
            profile = self.server.service.create_profile_stub(
                account_id=account.account_id,
                display_label=str(body.get("display_label") or ""),
                system_label=str(body.get("system_label") or ""),
                admin_label=str(body.get("admin_label") or ""),
                legacy_peer_id=str(body.get("legacy_peer_id") or ""),
                status=str(body.get("status") or "pending"),
            )
            self._send_json({"ok": True, "profile": asdict(profile)}, HTTPStatus.CREATED)
            return
        if parsed.path == "/lk/claims":
            account = self._account()
            if account is None:
                self._send_json({"ok": False, "error": "unauthorized"}, HTTPStatus.UNAUTHORIZED)
                return
            claim = self.server.service.attach_legacy_profile(
                account_id=account.account_id,
                legacy_peer_id=str(body.get("legacy_peer_id") or ""),
                claim_method=str(body.get("claim_method") or "operator_attach"),
                proof_payload=body.get("proof_payload") or {},
            )
            self._send_json({"ok": True, "claim": asdict(claim)})
            return
        self._send_json({"ok": False, "error": "not_found"}, HTTPStatus.NOT_FOUND)

    def log_message(self, format: str, *args) -> None:
        return


class AccountLKHTTPServer(ThreadingHTTPServer):
    def __init__(self, server_address: tuple[str, int], db_path: str):
        self.store = SqliteAccountLKStore(db_path)
        self.service = AccountLKService(self.store)
        super().__init__(server_address, AccountLKHTTPHandler)

    def close(self) -> None:
        self.store.close()
        self.server_close()


def serve(db_path: str, host: str = "127.0.0.1", port: int = 18081) -> None:
    server = AccountLKHTTPServer((host, port), db_path)
    try:
        server.serve_forever()
    finally:
        server.close()


if __name__ == "__main__":
    import argparse

    parser = argparse.ArgumentParser(description="Run minimal account-LK HTTP slice.")
    parser.add_argument("--db", required=True)
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=18081)
    args = parser.parse_args()
    serve(args.db, host=args.host, port=args.port)
