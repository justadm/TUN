#!/usr/bin/env bash
set -euo pipefail

VRN_HOST="${1:-vrn}"
REMOTE_ROOT="/opt/jstun-shadow"
REMOTE_ETC="/etc/jstun-shadow"
REMOTE_VAR="/var/lib/jstun-shadow"
REMOTE_CONTROL_API_DIR="${REMOTE_ROOT}/control-api"
REMOTE_PORTAL_CLI_DIR="${REMOTE_ROOT}/portal-cli"
REMOTE_BIN_DIR="${REMOTE_ROOT}/bin"
SHADOW_TOKEN="${JSTUN_SHADOW_CONTROL_API_TOKEN:-shadow-read-smoke-token}"
ADMIN_TOKEN="${JSTUN_SHADOW_ADMIN_TOKEN:-shadow-admin-token}"
DB_WRITE_MIRROR_ENABLED="${JSTUN_DB_WRITE_MIRROR_ENABLED:-0}"
DB_WRITE_MIRROR_EVENTS_ENABLED="${JSTUN_DB_WRITE_MIRROR_EVENTS_ENABLED:-1}"
TMP_ENV="$(mktemp)"
trap 'rm -f "${TMP_ENV}"' EXIT

echo "[1/6] prepare remote directories"
ssh "${VRN_HOST}" "sudo mkdir -p ${REMOTE_CONTROL_API_DIR} ${REMOTE_PORTAL_CLI_DIR} ${REMOTE_BIN_DIR} ${REMOTE_VAR}/runtime ${REMOTE_ETC}"

echo "[2/6] upload control-api and portal-cli"
scp control-plane/control-api/wg_control_api_server.py "${VRN_HOST}:/tmp/wg_control_api_server.py"
scp control-plane/portal-cli/wg_portal.py "${VRN_HOST}:/tmp/wg_portal.py"
scp control-plane/portal-cli/wgstub.py "${VRN_HOST}:/tmp/wgstub.py"
ssh "${VRN_HOST}" "sudo mv /tmp/wg_control_api_server.py ${REMOTE_CONTROL_API_DIR}/wg_control_api_server.py && sudo mv /tmp/wg_portal.py ${REMOTE_PORTAL_CLI_DIR}/wg_portal.py && sudo mv /tmp/wgstub.py ${REMOTE_BIN_DIR}/wgstub.py && sudo chmod 755 ${REMOTE_CONTROL_API_DIR}/wg_control_api_server.py ${REMOTE_PORTAL_CLI_DIR}/wg_portal.py ${REMOTE_BIN_DIR}/wgstub.py"

echo "[3/6] write shadow env"
cat > "${TMP_ENV}" <<EOF
WG_CONTROL_API_HOST=127.0.0.1
WG_CONTROL_API_PORT=18190
WG_CONTROL_API_TOKEN=${SHADOW_TOKEN}
WG_PORTAL_CLI=${REMOTE_PORTAL_CLI_DIR}/wg_portal.py
WG_PORTAL_IFACE=wgshadow0
WG_PORTAL_CONF=${REMOTE_VAR}/runtime/wgshadow0.conf
WG_PORTAL_ENV=${REMOTE_ETC}/jstun-shadow.env
WG_PORTAL_STATE=${REMOTE_VAR}/runtime
WG_PORTAL_WG_BIN=${REMOTE_BIN_DIR}/wgstub.py
WG_PORTAL_QRENCODE_BIN=qrencode
WG_STUB_STATE=${REMOTE_VAR}/runtime/wgstub-state.json
WG_PORTAL_NET=10.250.0.0/24
WG_PORTAL_IP_START=50
WG_PORTAL_IP_END=250
WG_PORTAL_HTTP_HOST=127.0.0.1
WG_PORTAL_HTTP_PORT=18210
WG_PORTAL_USE_CONTROL_API=1
WG_CONTROL_API_BASE=http://127.0.0.1:18190/v1
WG_CONTROL_API_TIMEOUT_SEC=5
WG_CONTROL_API_FALLBACK_CLI=1
WG_PORTAL_ADMIN_TOKEN=${ADMIN_TOKEN}
WG_PORTAL_UPLINK_ENABLED=1
JSTUN_DB_READ_ENABLED=1
JSTUN_DB_READ_PEERS_ENABLED=1
JSTUN_DB_READ_UPLINKS_ENABLED=1
JSTUN_DB_READ_EVENTS_ENABLED=1
JSTUN_DB_WRITE_MIRROR_ENABLED=${DB_WRITE_MIRROR_ENABLED}
JSTUN_DB_WRITE_MIRROR_EVENTS_ENABLED=${DB_WRITE_MIRROR_EVENTS_ENABLED}
JSTUN_DB_PSQL_BIN=psql
JSTUN_DB_HOST=127.0.0.1
JSTUN_DB_PORT=15432
JSTUN_DB_NAME=jstun_shadow
JSTUN_DB_USER=jstun_shadow
JSTUN_DB_PASSWORD=change-me
EOF
scp "${TMP_ENV}" "${VRN_HOST}:/tmp/jstun-shadow.env"
ssh "${VRN_HOST}" "sudo mv /tmp/jstun-shadow.env ${REMOTE_ETC}/jstun-shadow.env && sudo chmod 600 ${REMOTE_ETC}/jstun-shadow.env"

echo "[4/6] install shadow control-api unit"
scp control-plane/deploy/systemd/jstun-shadow-control-api.service "${VRN_HOST}:/tmp/jstun-shadow-control-api.service"
ssh "${VRN_HOST}" "sudo mv /tmp/jstun-shadow-control-api.service /etc/systemd/system/jstun-shadow-control-api.service && sudo systemctl daemon-reload && sudo systemctl enable jstun-shadow-control-api.service && sudo systemctl restart jstun-shadow-control-api.service"

echo "[5/6] verify service status"
ssh "${VRN_HOST}" "systemctl is-active jstun-shadow-control-api.service && systemctl status --no-pager --lines=20 jstun-shadow-control-api.service | sed -n '1,20p'"

echo "[6/6] smoke HTTP endpoints"
ssh "${VRN_HOST}" "curl -s http://127.0.0.1:18190/v1/health && printf '\n---\n' && curl -s -H 'X-API-Token: ${SHADOW_TOKEN}' http://127.0.0.1:18190/v1/peers && printf '\n---\n' && curl -s -H 'X-API-Token: ${SHADOW_TOKEN}' http://127.0.0.1:18190/v1/uplinks"
