#!/usr/bin/env bash
set -euo pipefail

VRN_HOST="${1:-vrn}"
REMOTE_ROOT="/opt/jstun-shadow"
REMOTE_ETC="/etc/jstun-shadow"
REMOTE_VAR="/var/lib/jstun-shadow"
REMOTE_PORTAL_HTTP_DIR="${REMOTE_ROOT}/portal-http"
REMOTE_PORTAL_CLI_DIR="${REMOTE_ROOT}/portal-cli"
SHADOW_TOKEN="${JSTUN_SHADOW_CONTROL_API_TOKEN:-shadow-read-smoke-token}"
ADMIN_TOKEN="${JSTUN_SHADOW_ADMIN_TOKEN:-shadow-admin-token}"
TMP_ENV="$(mktemp)"
trap 'rm -f "${TMP_ENV}"' EXIT

echo "[1/6] prepare remote directories"
ssh "${VRN_HOST}" "sudo mkdir -p ${REMOTE_PORTAL_HTTP_DIR} ${REMOTE_PORTAL_CLI_DIR} ${REMOTE_VAR}/runtime ${REMOTE_ETC}"

echo "[2/6] upload portal-http"
scp control-plane/portal-http/wg_portal_http.py "${VRN_HOST}:/tmp/wg_portal_http.py"
ssh "${VRN_HOST}" "sudo mv /tmp/wg_portal_http.py ${REMOTE_PORTAL_HTTP_DIR}/wg_portal_http.py && sudo chmod 755 ${REMOTE_PORTAL_HTTP_DIR}/wg_portal_http.py"

echo "[3/6] write shared shadow env"
cat > "${TMP_ENV}" <<EOF
WG_CONTROL_API_HOST=127.0.0.1
WG_CONTROL_API_PORT=18190
WG_CONTROL_API_TOKEN=${SHADOW_TOKEN}
WG_PORTAL_CLI=${REMOTE_PORTAL_CLI_DIR}/wg_portal.py
WG_PORTAL_STATE=${REMOTE_VAR}/runtime
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
JSTUN_DB_PSQL_BIN=psql
JSTUN_DB_HOST=127.0.0.1
JSTUN_DB_PORT=15432
JSTUN_DB_NAME=jstun_shadow
JSTUN_DB_USER=jstun_shadow
JSTUN_DB_PASSWORD=change-me
EOF
scp "${TMP_ENV}" "${VRN_HOST}:/tmp/jstun-shadow.env"
ssh "${VRN_HOST}" "sudo mv /tmp/jstun-shadow.env ${REMOTE_ETC}/jstun-shadow.env && sudo chmod 600 ${REMOTE_ETC}/jstun-shadow.env"

echo "[4/6] install shadow portal-http unit"
scp control-plane/deploy/systemd/jstun-shadow-portal-http.service "${VRN_HOST}:/tmp/jstun-shadow-portal-http.service"
ssh "${VRN_HOST}" "sudo mv /tmp/jstun-shadow-portal-http.service /etc/systemd/system/jstun-shadow-portal-http.service && sudo systemctl daemon-reload && sudo systemctl enable jstun-shadow-portal-http.service && sudo systemctl restart jstun-shadow-portal-http.service"

echo "[5/6] verify service status"
ssh "${VRN_HOST}" "systemctl is-active jstun-shadow-portal-http.service && systemctl status --no-pager --lines=20 jstun-shadow-portal-http.service | sed -n '1,20p'"

echo "[6/6] smoke HTTP pages"
ssh "${VRN_HOST}" "curl -sI http://127.0.0.1:18210/ && printf '\n---\n' && curl -s 'http://127.0.0.1:18210/admin/?token=${ADMIN_TOKEN}' | sed -n '1,20p' && printf '\n---\n' && curl -s 'http://127.0.0.1:18210/admin/events/?token=${ADMIN_TOKEN}' | sed -n '1,20p' && printf '\n---\n' && curl -s 'http://127.0.0.1:18210/admin/live/data/?token=${ADMIN_TOKEN}'"
