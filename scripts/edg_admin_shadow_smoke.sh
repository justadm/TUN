#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"

ssh "${EDG_HOST}" "bash -s" <<'SH'
set -euo pipefail
TOKEN="$(sudo awk -F= '/^WG_PORTAL_ADMIN_TOKEN=/{print $2}' /etc/wireguard/wg-portal.env)"
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

curl -s -c "${TMP}/cookies.txt" -o "${TMP}/live.html" "http://10.200.0.4:18090/admin/live/?token=${TOKEN}&read_mode=shadow"
printf 'page_has_shadow='
grep -q '<code>shadow</code>' "${TMP}/live.html" && echo yes || echo no
printf 'live_data='
curl -s "http://10.200.0.4:18090/admin/live/data/?token=${TOKEN}&read_mode=shadow"
SH
