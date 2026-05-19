#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ROOT_DIR}/monitoring/.env"

if [[ ! -f "${ENV_FILE}" ]]; then
  echo "env file not found: ${ENV_FILE}" >&2
  exit 1
fi

aliases=("$@")
if [[ ${#aliases[@]} -eq 0 ]]; then
  if [[ -f "${HOME}/.ssh/config" ]]; then
    while IFS= read -r line; do
      host="${line#Host }"
      host="$(echo "${host}" | xargs)"
      [[ -z "${host}" ]] && continue
      [[ "${host}" == *"*"* ]] && continue
      [[ "${host}" == *"?"* ]] && continue
      aliases+=("${host}")
    done < <(grep -E '^[[:space:]]*Host[[:space:]]+' "${HOME}/.ssh/config" || true)
  fi
fi
if [[ ${#aliases[@]} -eq 0 ]]; then
  aliases=(nyc fra ams msk spb vrn edg exe)
fi

# Keep order but remove duplicates.
uniq_aliases=()
seen_aliases=","
for host in "${aliases[@]}"; do
  key="$(echo "${host}" | xargs)"
  [[ -z "${key}" ]] && continue
  if [[ "${seen_aliases}" == *",${key},"* ]]; then
    continue
  fi
  seen_aliases="${seen_aliases}${key},"
  uniq_aliases+=("${key}")
done
aliases=("${uniq_aliases[@]}")

base_port="${MONITORING_AUTO_BASE_PORT:-18111}"
next_port="${base_port}"
docker_host="${MONITORING_DOCKER_HOST:-172.17.0.1}"

disc_urls=()
disc_tokens=()
peer_urls=()
peer_tokens=()

get_token() {
  local host="$1"
  ssh -o ConnectTimeout=5 "${host}" "sudo python3 -" 2>/dev/null <<'PY'
import re, subprocess
try:
    out = subprocess.check_output(["ss", "-ltnp"], stderr=subprocess.DEVNULL).decode("utf-8", "ignore")
except Exception:
    out = ""
ports = {18110, 18190}
keys = (
    "WG_CONTROL_API_TOKEN",
    "CONTROL_API_TOKEN",
    "API_TOKEN",
    "JSTUN_ADMIN_TOKEN",
    "JSTUN_SPB_EDGE_API_TOKEN",
)
pids = []
for line in out.splitlines():
    if not any(f":{port}" in line for port in ports):
        continue
    m = re.search(r"pid=(\d+)", line)
    if m:
        p = m.group(1)
        if p not in pids:
            pids.append(p)
for pid in pids:
    try:
        cmdline = open(f"/proc/{pid}/cmdline", "rb").read().decode("utf-8", "ignore")
    except Exception:
        cmdline = ""
    if "docker-proxy" in cmdline:
        continue
    try:
        raw = open(f"/proc/{pid}/environ", "rb").read().split(b"\0")
    except Exception:
        continue
    for item in raw:
        for key in keys:
            pref = (key + "=").encode("utf-8")
            if item.startswith(pref):
                print(item.decode("utf-8", "ignore").split("=", 1)[1])
                raise SystemExit(0)
paths = (
    "/etc/default/jstun-shadow-control-api",
    "/etc/default/jstun-control-api",
    "/etc/jstun/shadow-control-api.env",
    "/etc/jstun/control-api.env",
    "/opt/jstun/.env",
)
for path in paths:
    try:
        with open(path, "r", encoding="utf-8", errors="ignore") as fh:
            for line in fh:
                txt = line.strip()
                if not txt or txt.startswith("#") or "=" not in txt:
                    continue
                k, v = txt.split("=", 1)
                if k.strip() in keys:
                    print(v.strip().strip('"').strip("'"))
                    raise SystemExit(0)
    except Exception:
        continue
print("")
PY
  true
}

endpoint_ok() {
  local local_port="$1"
  local token="$2"
  local path="$3"
  local body
  body="$(curl -sS -m 4 -H "X-API-Token: ${token}" "http://localhost:${local_port}${path}" 2>/dev/null || true)"
  [[ -n "${body}" ]] || return 1
  case "${body}" in
    *"unauthorized"*|*"forbidden"*)
      return 1
      ;;
  esac
  return 0
}

kill_tunnel_port() {
  local local_port="$1"
  if command -v lsof >/dev/null 2>&1; then
    pids="$(lsof -tiTCP:${local_port} -sTCP:LISTEN 2>/dev/null || true)"
    if [[ -n "${pids}" ]]; then
      echo "${pids}" | xargs -n1 kill >/dev/null 2>&1 || true
    fi
  fi
}

for host in "${aliases[@]}"; do
  local_port="${next_port}"
  next_port=$((next_port + 1))
  remote_port=""
  token="$(get_token "${host}" 2>/dev/null | tr -d '\r' | tail -n1 || true)"
  if [[ -z "${token}" ]]; then
    echo "[skip] ${host}: token_not_found"
    continue
  fi

  edges_ok=0
  uplinks_ok=0
  peers_ok=0

  for rp in 18110 18190; do
    kill_tunnel_port "${local_port}"
    if ssh -o BatchMode=yes -o ConnectTimeout=5 -fN -L "${local_port}:127.0.0.1:${rp}" "${host}" 2>/dev/null; then
      e=0
      u=0
      p=0
      endpoint_ok "${local_port}" "${token}" "/v1/edges" && e=1
      endpoint_ok "${local_port}" "${token}" "/v1/uplinks" && u=1
      endpoint_ok "${local_port}" "${token}" "/v1/peers" && p=1
      if [[ ${e} -eq 1 || ${u} -eq 1 || ${p} -eq 1 ]]; then
        remote_port="${rp}"
        edges_ok="${e}"
        uplinks_ok="${u}"
        peers_ok="${p}"
        break
      fi
    fi
  done
  if [[ -z "${remote_port}" ]]; then
    echo "[skip] ${host}: tunnel_failed"
    kill_tunnel_port "${local_port}"
    continue
  fi

  if [[ ${edges_ok} -eq 1 ]]; then
    disc_urls+=("http://${docker_host}:${local_port}/v1/edges")
    disc_tokens+=("${token}")
  fi
  if [[ ${uplinks_ok} -eq 1 ]]; then
    disc_urls+=("http://${docker_host}:${local_port}/v1/uplinks")
    disc_tokens+=("${token}")
  fi
  if [[ ${peers_ok} -eq 1 ]]; then
    peer_urls+=("http://${docker_host}:${local_port}/v1/peers")
    peer_tokens+=("${token}")
  fi

  if [[ ${edges_ok} -eq 0 && ${uplinks_ok} -eq 0 && ${peers_ok} -eq 0 ]]; then
    echo "[skip] ${host}: all_endpoints_unreachable_or_unauthorized"
    kill_tunnel_port "${local_port}"
    continue
  fi

  echo "[ok] ${host}: local_port=${local_port} remote_port=${remote_port} edges=${edges_ok} uplinks=${uplinks_ok} peers=${peers_ok}"
done

if [[ ${#disc_urls[@]} -eq 0 && ${#peer_urls[@]} -eq 0 ]]; then
  echo "no reachable sources discovered"
  exit 1
fi

disc_urls_csv="$(IFS=,; echo "${disc_urls[*]}")"
disc_tokens_csv="$(IFS=,; echo "${disc_tokens[*]}")"
peer_urls_csv="$(IFS=,; echo "${peer_urls[*]}")"
peer_tokens_csv="$(IFS=,; echo "${peer_tokens[*]}")"

python3 - "${ENV_FILE}" "${disc_urls_csv}" "${disc_tokens_csv}" "${peer_urls_csv}" "${peer_tokens_csv}" <<'PY'
import pathlib, sys
env_path = pathlib.Path(sys.argv[1])
disc_urls = sys.argv[2]
disc_tokens = sys.argv[3]
peer_urls = sys.argv[4]
peer_tokens = sys.argv[5]

lines = env_path.read_text(encoding="utf-8").splitlines()
updates = {
    "MONITORING_DISCOVERY_URLS": disc_urls,
    "MONITORING_DISCOVERY_URL_TOKENS": disc_tokens,
    "MONITORING_CONTROL_PEERS_URLS": peer_urls,
    "MONITORING_CONTROL_PEERS_TOKENS": peer_tokens,
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
for key, value in updates.items():
    if key not in seen:
        out.append(f"{key}={value}")
env_path.write_text("\n".join(out) + "\n", encoding="utf-8")
PY

echo "updated ${ENV_FILE}"
echo "restart monitoring stack to apply:"
echo "  docker compose -f monitoring/docker-compose.yml up -d --build monitoring-ingestor"
