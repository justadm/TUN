from __future__ import annotations

import json
import os
from dataclasses import dataclass
from pathlib import Path
from typing import Any


def _env(name: str, default: str = "") -> str:
    return str(os.getenv(name, default)).strip()


def _env_int(name: str, default: int) -> int:
    raw = _env(name, str(default))
    try:
        return int(raw)
    except ValueError:
        return default


def _env_bool(name: str, default: bool) -> bool:
    raw = _env(name, "true" if default else "false").lower()
    return raw in {"1", "true", "yes", "on"}


@dataclass(frozen=True)
class PostgresConfig:
    host: str
    port: int
    db: str
    user: str
    password: str


@dataclass(frozen=True)
class HTTPConfig:
    host: str
    port: int


@dataclass(frozen=True)
class IngestorConfig:
    http: HTTPConfig
    postgres: PostgresConfig
    poll_interval_sec: int
    node_sources_file: Path
    helper_timeout_sec: int
    command_dispatch_enabled: bool
    command_dispatch_batch_size: int
    command_dispatch_timeout_sec: int
    command_retry_max_attempts: int
    command_retry_backoff_base_sec: int
    command_retry_backoff_max_sec: int


@dataclass(frozen=True)
class APIConfig:
    http: HTTPConfig
    postgres: PostgresConfig


def load_postgres_config() -> PostgresConfig:
    return PostgresConfig(
        host=_env("MONITORING_PG_HOST", "127.0.0.1"),
        port=_env_int("MONITORING_PG_PORT", 5432),
        db=_env("MONITORING_PG_DB", "monitoring"),
        user=_env("MONITORING_PG_USER", "monitoring"),
        password=_env("MONITORING_PG_PASSWORD", "monitoring"),
    )


def load_api_config() -> APIConfig:
    return APIConfig(
        http=HTTPConfig(
            host=_env("MONITORING_API_HOST", "0.0.0.0"),
            port=_env_int("MONITORING_API_PORT", 18070),
        ),
        postgres=load_postgres_config(),
    )


def load_ingestor_config() -> IngestorConfig:
    return IngestorConfig(
        http=HTTPConfig(
            host=_env("MONITORING_INGESTOR_HOST", "0.0.0.0"),
            port=_env_int("MONITORING_INGESTOR_PORT", 18071),
        ),
        postgres=load_postgres_config(),
        poll_interval_sec=_env_int("MONITORING_INGESTOR_POLL_INTERVAL_SEC", 30),
        node_sources_file=Path(_env("MONITORING_NODE_SOURCES_FILE", "/app/config/nodes.json")),
        helper_timeout_sec=_env_int("MONITORING_HELPER_TIMEOUT_SEC", 5),
        command_dispatch_enabled=_env_bool("MONITORING_COMMAND_DISPATCH_ENABLED", False),
        command_dispatch_batch_size=_env_int("MONITORING_COMMAND_DISPATCH_BATCH_SIZE", 20),
        command_dispatch_timeout_sec=_env_int("MONITORING_COMMAND_DISPATCH_TIMEOUT_SEC", 5),
        command_retry_max_attempts=_env_int("MONITORING_COMMAND_RETRY_MAX_ATTEMPTS", 5),
        command_retry_backoff_base_sec=_env_int("MONITORING_COMMAND_RETRY_BACKOFF_BASE_SEC", 2),
        command_retry_backoff_max_sec=_env_int("MONITORING_COMMAND_RETRY_BACKOFF_MAX_SEC", 60),
    )


def load_node_sources(path: Path) -> dict[str, Any]:
    if not path.exists():
        return {"nodes": []}
    raw = json.loads(path.read_text(encoding="utf-8") or "{}")
    if not isinstance(raw, dict):
        return {"nodes": []}
    nodes = raw.get("nodes")
    if not isinstance(nodes, list):
        return {"nodes": []}
    return {"nodes": nodes}
