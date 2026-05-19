#!/usr/bin/env bash
set -euo pipefail

ENV_FILE="/etc/wireguard/wg-portal.env"
PORTAL_TOKEN="$(sudo awk -F= '/^WG_PORTAL_ADMIN_TOKEN=/{print substr($0,index($0,"=")+1)}' "$ENV_FILE" | tail -n1)"
API_TOKEN="$(sudo awk -F= '/^WG_CONTROL_API_TOKEN=/{print substr($0,index($0,"=")+1)}' "$ENV_FILE" | tail -n1)"
BASE="http://10.200.0.4:18090"

echo "TOKEN_LEN=${#PORTAL_TOKEN}"
PEERS_HTML="$(curl --max-time 10 -fsS "${BASE}/admin/peers/?token=${PORTAL_TOKEN}")"
if grep -q "value='uplink_fra'" <<<"$PEERS_HTML"; then
  echo "PEERS_FRA_OK=1"
else
  echo "PEERS_FRA_OK=0"
fi

PEER="$(printf '%s\n' "$PEERS_HTML" | sed -n "s#.*href='/admin/peers/\([^/]*\)/'.*#\1#p" | head -n1)"
echo "PEER=${PEER}"

if [[ -n "$PEER" ]]; then
  DETAIL_HTML="$(curl --max-time 10 -fsS "${BASE}/admin/peers/${PEER}/?token=${PORTAL_TOKEN}")"
  if grep -q "value='uplink_fra'" <<<"$DETAIL_HTML"; then
    echo "DETAIL_FRA_OK=1"
  else
    echo "DETAIL_FRA_OK=0"
  fi
fi

echo -n "UPLINKS_JSON="
curl --max-time 10 -fsS -H "Authorization: Bearer ${API_TOKEN}" http://127.0.0.1:18110/v1/uplinks | tr -d '\n'
echo
