from __future__ import annotations

import json
import os
import subprocess
from dataclasses import dataclass
from typing import Any

from services.common.config import PostgresConfig


@dataclass(frozen=True)
class DBConfig:
    postgres: PostgresConfig
    psql_bin: str


class DBError(RuntimeError):
    pass


def load_db_config(postgres: PostgresConfig) -> DBConfig:
    return DBConfig(
        postgres=postgres,
        psql_bin=str(os.getenv("MONITORING_PSQL_BIN", "psql")).strip() or "psql",
    )


def _run_psql(cfg: DBConfig, sql: str) -> str:
    env = os.environ.copy()
    if cfg.postgres.password:
        env["PGPASSWORD"] = cfg.postgres.password
    args = [
        cfg.psql_bin,
        "-h",
        cfg.postgres.host,
        "-p",
        str(cfg.postgres.port),
        "-U",
        cfg.postgres.user,
        "-d",
        cfg.postgres.db,
        "-At",
        "-F",
        "\t",
        "-c",
        sql,
    ]
    proc = subprocess.run(args, capture_output=True, text=True, env=env)
    if proc.returncode != 0:
        raise DBError((proc.stderr or proc.stdout or "psql failed").strip())
    return (proc.stdout or "").strip()


def fetch_json_value(cfg: DBConfig, sql: str, *, default: Any) -> Any:
    raw = _run_psql(cfg, sql)
    if raw == "":
        return default
    try:
        return json.loads(raw)
    except json.JSONDecodeError as exc:
        raise DBError(f"invalid json from postgres: {exc}") from exc


def exec_sql(cfg: DBConfig, sql: str) -> None:
    _run_psql(cfg, sql)


def sql_str(value: str) -> str:
    return "'" + str(value).replace("'", "''") + "'"

