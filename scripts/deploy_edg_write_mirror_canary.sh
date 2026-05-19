#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"
VRN_HOST="${2:-vrn}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONTROL_API_SRC="${ROOT_DIR}/control-plane/control-api/wg_control_api_server.py"
UNIT_SRC="${ROOT_DIR}/control-plane/deploy/systemd/jstun-shadow-write-db-tunnel.service"
TMP_ENV="$(mktemp)"
trap 'rm -f "${TMP_ENV}"' EXIT

echo "[1/8] verify EDG has psql client"
if ! ssh "${EDG_HOST}" "command -v psql >/dev/null"; then
  echo "EDG is missing psql client; install postgresql-client first" >&2
  exit 1
fi

echo "[2/8] fetch VRN shadow DB password"
DB_PASS="$(
  ssh "${VRN_HOST}" "awk -F= '/^JSTUN_DB_PASSWORD=/{print \$2}' /etc/jstun-shadow/jstun-shadow.env" | tr -d '\r' | tail -n1
)"
if [[ -z "${DB_PASS}" ]]; then
  echo "VRN shadow DB password is empty" >&2
  exit 1
fi

cat > "${TMP_ENV}" <<EOF
JSTUN_DB_WRITE_MIRROR_ENABLED=1
JSTUN_DB_WRITE_MIRROR_EVENTS_ENABLED=1
JSTUN_DB_PSQL_BIN=psql
JSTUN_DB_HOST=127.0.0.1
JSTUN_DB_PORT=15433
JSTUN_DB_NAME=jstun_shadow
JSTUN_DB_USER=jstun_shadow
JSTUN_DB_PASSWORD=${DB_PASS}
JSTUN_WRITE_SHADOW_DB_TUNNEL_TARGET_HOST=user@91.221.109.60
JSTUN_WRITE_SHADOW_DB_TUNNEL_SSH_PORT=65022
JSTUN_WRITE_SHADOW_DB_TUNNEL_TARGET_PORT=15432
JSTUN_WRITE_SHADOW_DB_TUNNEL_LOCAL_PORT=15433
JSTUN_WRITE_SHADOW_DB_TUNNEL_IDENTITY_FILE=/home/opsadmin/.ssh/jstun_shadow_read_ed25519
EOF

echo "[3/8] upload control-api, tunnel unit, and env overlay"
scp "${CONTROL_API_SRC}" "${EDG_HOST}:/tmp/wg_control_api_server.py"
scp "${UNIT_SRC}" "${EDG_HOST}:/tmp/jstun-shadow-write-db-tunnel.service"
scp "${TMP_ENV}" "${EDG_HOST}:/tmp/jstun-write-mirror-canary.env"

echo "[4/8] install write-db tunnel unit"
ssh "${EDG_HOST}" "sudo mv /tmp/jstun-shadow-write-db-tunnel.service /etc/systemd/system/jstun-shadow-write-db-tunnel.service && sudo chmod 644 /etc/systemd/system/jstun-shadow-write-db-tunnel.service"

echo "[5/8] merge env overlay into /etc/wireguard/wg-portal.env"
ssh "${EDG_HOST}" "sudo python3 - /etc/wireguard/wg-portal.env /tmp/jstun-write-mirror-canary.env" <<'PY'
import pathlib
import sys

target = pathlib.Path(sys.argv[1])
overlay = pathlib.Path(sys.argv[2])

def parse_env(path):
    out = []
    for raw in path.read_text(encoding='utf-8', errors='replace').splitlines():
        line = raw.rstrip('\n')
        if not line or line.lstrip().startswith('#') or '=' not in line:
            out.append((None, line))
            continue
        k, v = line.split('=', 1)
        out.append((k.strip(), f"{k.strip()}={v}"))
    return out

base_rows = parse_env(target)
overlay_map = {}
for line in overlay.read_text(encoding='utf-8', errors='replace').splitlines():
    if not line or line.lstrip().startswith('#') or '=' not in line:
        continue
    k, v = line.split('=', 1)
    overlay_map[k.strip()] = v

seen = set()
out_lines = []
for key, raw in base_rows:
    if key is None:
        out_lines.append(raw)
        continue
    if key in overlay_map:
        out_lines.append(f"{key}={overlay_map[key]}")
        seen.add(key)
    else:
        out_lines.append(raw)

for key in sorted(overlay_map):
    if key not in seen:
        out_lines.append(f"{key}={overlay_map[key]}")

target.write_text("\n".join(out_lines).rstrip() + "\n", encoding='utf-8')
PY

echo "[6/8] install updated control-api"
ssh "${EDG_HOST}" "sudo mv /tmp/wg_control_api_server.py /usr/local/bin/wg_control_api_server.py && sudo chmod 755 /usr/local/bin/wg_control_api_server.py"

echo "[7/8] reload systemd and restart services"
ssh "${EDG_HOST}" "sudo systemctl daemon-reload && sudo systemctl enable --now jstun-shadow-write-db-tunnel.service && sudo systemctl restart wg-control-api.service"

echo "[8/8] verify tunnel and control-api"
ssh "${EDG_HOST}" "sudo systemctl is-active jstun-shadow-write-db-tunnel.service && sudo systemctl is-active wg-control-api.service && (command -v psql >/dev/null) && PGPASSWORD='${DB_PASS}' psql -h 127.0.0.1 -p 15433 -U jstun_shadow -d jstun_shadow -At -c 'select 1' && curl -s http://127.0.0.1:18110/v1/health"
