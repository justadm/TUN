#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"
VRN_HOST="${2:-vrn}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UNIT_SRC="${ROOT_DIR}/control-plane/deploy/systemd/jstun-shadow-read-tunnel.service"
PORTAL_SRC="${ROOT_DIR}/control-plane/portal-http/wg_portal_http.py"

TMP_ENV="$(mktemp)"
trap 'rm -f "${TMP_ENV}"' EXIT

echo "[1/7] fetch VRN shadow API token"
SHADOW_TOKEN="$(
  ssh "${VRN_HOST}" "awk -F= '/^WG_CONTROL_API_TOKEN=/{print \$2}' /etc/jstun-shadow/jstun-shadow.env" | tr -d '\r' | tail -n1
)"
if [[ -z "${SHADOW_TOKEN}" ]]; then
  echo "shadow token is empty" >&2
  exit 1
fi

cat > "${TMP_ENV}" <<EOF
JSTUN_ADMIN_READ_CANARY_ENABLED=1
JSTUN_ADMIN_READ_MODE_DEFAULT=local
JSTUN_READ_SHADOW_ENABLED=1
JSTUN_READ_SHADOW_BASE=http://127.0.0.1:18191/v1
JSTUN_READ_SHADOW_TOKEN=${SHADOW_TOKEN}
JSTUN_READ_SHADOW_TIMEOUT_SEC=5
JSTUN_READ_SHADOW_RESOURCES=peers,uplinks,peer_uplink,events
JSTUN_READ_SHADOW_TUNNEL_TARGET_HOST=user@91.221.109.60
JSTUN_READ_SHADOW_TUNNEL_SSH_PORT=65022
JSTUN_READ_SHADOW_TUNNEL_TARGET_PORT=18190
JSTUN_READ_SHADOW_TUNNEL_LOCAL_PORT=18191
JSTUN_READ_SHADOW_TUNNEL_IDENTITY_FILE=/home/opsadmin/.ssh/jstun_shadow_read_ed25519
EOF

echo "[2/7] upload portal-http, unit, and env overlay"
scp "${PORTAL_SRC}" "${EDG_HOST}:/tmp/wg_portal_http.py"
scp "${UNIT_SRC}" "${EDG_HOST}:/tmp/jstun-shadow-read-tunnel.service"
scp "${TMP_ENV}" "${EDG_HOST}:/tmp/jstun-admin-read-canary.env"

echo "[3/7] install tunnel unit"
ssh "${EDG_HOST}" "sudo mv /tmp/jstun-shadow-read-tunnel.service /etc/systemd/system/jstun-shadow-read-tunnel.service && sudo chmod 644 /etc/systemd/system/jstun-shadow-read-tunnel.service"

echo "[4/7] merge env overlay into /etc/wireguard/wg-portal.env"
ssh "${EDG_HOST}" "sudo python3 - /etc/wireguard/wg-portal.env /tmp/jstun-admin-read-canary.env" <<'PY'
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

echo "[5/7] install updated portal-http"
ssh "${EDG_HOST}" "sudo mv /tmp/wg_portal_http.py /usr/local/bin/wg_portal_http.py && sudo chmod 755 /usr/local/bin/wg_portal_http.py"

echo "[6/7] reload systemd and restart services"
ssh "${EDG_HOST}" "sudo systemctl daemon-reload && sudo systemctl enable --now jstun-shadow-read-tunnel.service && sudo systemctl restart wg-portal-http.service"

echo "[7/7] verify tunnel and admin surface"
ssh "${EDG_HOST}" "sudo systemctl is-active jstun-shadow-read-tunnel.service && sudo systemctl is-active wg-portal-http.service && curl -sI http://127.0.0.1:18191/v1/health | head -n 1"
