#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ROOT_DIR}/monitoring/.env"
MAX_ENV_DEFAULT="/Users/just/projects/max/.env"
TG_ENV_DEFAULT="/Users/just/projects/tg/tg-notify/.env"
MAX_ENV_PATH="${1:-${MAX_ENV_DEFAULT}}"
TG_ENV_PATH="${2:-${TG_ENV_DEFAULT}}"

if [[ ! -f "${ENV_FILE}" ]]; then
  echo "env file not found: ${ENV_FILE}" >&2
  exit 1
fi

extract_env_value() {
  local file="$1"
  local key="$2"
  [[ -f "${file}" ]] || return 0
  awk -F= -v k="${key}" '
    $1==k{
      sub(/^[^=]*=/,"",$0);
      gsub(/\r/,"",$0);
      print $0;
      exit
    }
  ' "${file}"
}

tg_token="$(extract_env_value "${TG_ENV_PATH}" "TELEGRAM_BOT_TOKEN" | xargs || true)"
tg_chat_id="$(extract_env_value "${TG_ENV_PATH}" "TELEGRAM_CHAT_ID" | xargs || true)"
if [[ -n "${tg_token}" && -n "${tg_chat_id}" ]]; then
  migrated_chat_id="$(python3 - "${tg_token}" "${tg_chat_id}" <<'PY'
import json, sys
from urllib.parse import urlencode
from urllib.request import Request, urlopen
from urllib.error import HTTPError

token = sys.argv[1].strip()
chat_id = sys.argv[2].strip()
url = f"https://api.telegram.org/bot{token}/getChat"
payload = urlencode({"chat_id": chat_id}).encode("utf-8")
req = Request(url, method="POST", headers={"Content-Type": "application/x-www-form-urlencoded"}, data=payload)
try:
    with urlopen(req, timeout=5):
        print("")
except HTTPError as exc:
    raw = exc.read().decode("utf-8", "ignore")
    try:
        doc = json.loads(raw or "{}")
    except Exception:
        doc = {}
    params = doc.get("parameters")
    if isinstance(params, dict):
        v = str(params.get("migrate_to_chat_id", "")).strip()
        print(v)
    else:
        print("")
except Exception:
    print("")
PY
)"
  if [[ -n "${migrated_chat_id}" ]]; then
    tg_chat_id="${migrated_chat_id}"
  fi
fi

max_token="$(extract_env_value "${MAX_ENV_PATH}" "MAX_TOKEN" | xargs || true)"
max_base_url="$(extract_env_value "${MAX_ENV_PATH}" "MAX_BASE_URL" | xargs || true)"
max_chat_id="$(extract_env_value "${MAX_ENV_PATH}" "MAX_CHAT_ID" | xargs || true)"
if [[ -z "${max_chat_id}" ]]; then
  max_chat_id="$(extract_env_value "${MAX_ENV_PATH}" "MAX_CHAT" | xargs || true)"
fi
if [[ -z "${max_chat_id}" ]]; then
  max_chat_id="$(extract_env_value "${MAX_ENV_PATH}" "ALERT_CHAT_ID" | xargs || true)"
fi
if [[ -z "${max_chat_id}" ]]; then
  max_chat_id="$(extract_env_value "${MAX_ENV_PATH}" "REPORT_CHAT_ID" | xargs || true)"
fi
if [[ -z "${max_chat_id}" ]]; then
  allowed_chat_ids="$(extract_env_value "${MAX_ENV_PATH}" "ALLOWED_CHAT_IDS" | xargs || true)"
  if [[ -n "${allowed_chat_ids}" ]]; then
    max_chat_id="$(echo "${allowed_chat_ids}" | cut -d',' -f1 | xargs)"
  fi
fi

python3 - "${ENV_FILE}" "${tg_token}" "${tg_chat_id}" "${max_token}" "${max_base_url}" "${max_chat_id}" <<'PY'
import pathlib, sys
env_path = pathlib.Path(sys.argv[1])
lines = env_path.read_text(encoding="utf-8").splitlines()
current = {}
for line in lines:
    if "=" not in line or line.strip().startswith("#"):
        continue
    k, v = line.split("=", 1)
    current[k.strip()] = v
updates = {
    "MONITORING_ALERT_TG_BOT_TOKEN": sys.argv[2].strip() or current.get("MONITORING_ALERT_TG_BOT_TOKEN", ""),
    "MONITORING_ALERT_TG_CHAT_ID": current.get("MONITORING_ALERT_TG_CHAT_ID", "") or sys.argv[3].strip(),
    "MONITORING_ALERT_MAX_TOKEN": sys.argv[4].strip() or current.get("MONITORING_ALERT_MAX_TOKEN", ""),
    "MONITORING_ALERT_MAX_BASE_URL": sys.argv[5].strip() or current.get("MONITORING_ALERT_MAX_BASE_URL", "https://platform-api.max.ru"),
    "MONITORING_ALERT_MAX_CHAT_ID": sys.argv[6].strip() or current.get("MONITORING_ALERT_MAX_CHAT_ID", ""),
}
seen = set()
out = []
for line in lines:
    if "=" not in line or line.strip().startswith("#"):
        out.append(line)
        continue
    k, _ = line.split("=", 1)
    key = k.strip()
    if key in updates:
        out.append(f"{key}={updates[key]}")
        seen.add(key)
    else:
        out.append(line)
for k, v in updates.items():
    if k not in seen:
        out.append(f"{k}={v}")
env_path.write_text("\n".join(out) + "\n", encoding="utf-8")
PY

echo "updated notifier config in ${ENV_FILE}"
if [[ -z "${tg_token}" || -z "${tg_chat_id}" ]]; then
  echo "warning: telegram bot token/chat id not fully populated"
fi
if [[ -z "${max_token}" || -z "${max_chat_id}" ]]; then
  echo "warning: max token/chat id not fully populated"
fi
