from __future__ import annotations

import json
import os
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


class HelperClientError(RuntimeError):
    pass


def _resolve_auth_header(auth_ref: str) -> dict[str, str]:
    auth_ref = str(auth_ref or "").strip()
    if not auth_ref:
        return {}
    if auth_ref.startswith("env:"):
        env_name = auth_ref.split(":", 1)[1].strip()
        token = str(os.getenv(env_name, "")).strip()
        if not token:
            return {}
        return {
            "Authorization": f"Bearer {token}",
            "X-Helper-Token": token,
        }
    return {}


def _request_json(
    url: str,
    *,
    method: str,
    payload: dict[str, Any] | None = None,
    auth_ref: str = "",
    timeout_sec: float = 5.0,
) -> Any:
    headers = {"Accept": "application/json"}
    headers.update(_resolve_auth_header(auth_ref))
    data = None
    if payload is not None:
        headers["Content-Type"] = "application/json"
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = Request(url, headers=headers, data=data, method=method)
    try:
        with urlopen(req, timeout=timeout_sec) as resp:
            raw = resp.read().decode("utf-8", "replace")
    except HTTPError as exc:
        body = exc.read().decode("utf-8", "replace")
        raise HelperClientError(f"http {exc.code}: {body.strip()}") from exc
    except URLError as exc:
        raise HelperClientError(str(exc)) from exc
    try:
        return json.loads(raw or "{}")
    except json.JSONDecodeError as exc:
        raise HelperClientError(f"invalid json: {exc}") from exc


def get_json(url: str, *, auth_ref: str = "", timeout_sec: float = 5.0) -> Any:
    return _request_json(url, method="GET", auth_ref=auth_ref, timeout_sec=timeout_sec)


def post_json(url: str, payload: dict[str, Any], *, auth_ref: str = "", timeout_sec: float = 5.0) -> Any:
    return _request_json(url, method="POST", payload=payload, auth_ref=auth_ref, timeout_sec=timeout_sec)


def fetch_helper_links(base_url: str, *, auth_ref: str = "", timeout_sec: float = 5.0) -> list[dict[str, Any]]:
    base = str(base_url or "").rstrip("/")
    if not base:
        raise HelperClientError("empty helper base url")
    payload = get_json(f"{base}/v1/helper/links", auth_ref=auth_ref, timeout_sec=timeout_sec)
    if isinstance(payload, dict):
        links = payload.get("links")
        if isinstance(links, list):
            return [item for item in links if isinstance(item, dict)]
    raise HelperClientError("helper response missing links[]")


def fetch_helper_schema(base_url: str, *, auth_ref: str = "", timeout_sec: float = 5.0) -> dict[str, Any]:
    base = str(base_url or "").rstrip("/")
    if not base:
        raise HelperClientError("empty helper base url")
    payload = get_json(f"{base}/v1/helper/schema", auth_ref=auth_ref, timeout_sec=timeout_sec)
    if isinstance(payload, dict):
        return payload
    raise HelperClientError("helper schema response is not a JSON object")


def fetch_helper_status(base_url: str, *, auth_ref: str = "", timeout_sec: float = 5.0) -> dict[str, Any]:
    base = str(base_url or "").rstrip("/")
    if not base:
        raise HelperClientError("empty helper base url")
    payload = get_json(f"{base}/v1/helper/status", auth_ref=auth_ref, timeout_sec=timeout_sec)
    if isinstance(payload, dict):
        return payload
    raise HelperClientError("helper status response is not a JSON object")


def fetch_helper_profile_current(base_url: str, *, auth_ref: str = "", timeout_sec: float = 5.0) -> dict[str, Any]:
    base = str(base_url or "").rstrip("/")
    if not base:
        raise HelperClientError("empty helper base url")
    payload = get_json(f"{base}/v1/helper/profile.current", auth_ref=auth_ref, timeout_sec=timeout_sec)
    if isinstance(payload, dict):
        return payload
    raise HelperClientError("helper profile.current response is not a JSON object")


def post_helper_link_action(
    base_url: str,
    link_id: str,
    action: str,
    *,
    payload: dict[str, Any] | None = None,
    auth_ref: str = "",
    timeout_sec: float = 5.0,
) -> dict[str, Any]:
    base = str(base_url or "").rstrip("/")
    link_id = str(link_id or "").strip()
    action = str(action or "").strip()
    if not base:
        raise HelperClientError("empty helper base url")
    if not link_id:
        raise HelperClientError("empty link id")
    if action not in {"reconnect", "drain", "resume", "gateway.select"}:
        raise HelperClientError(f"unsupported action: {action}")
    body = payload if isinstance(payload, dict) else {}
    out = post_json(
        f"{base}/v1/helper/links/{link_id}/{action}",
        body,
        auth_ref=auth_ref,
        timeout_sec=timeout_sec,
    )
    if isinstance(out, dict):
        return out
    raise HelperClientError("helper action response is not a JSON object")
