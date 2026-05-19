#!/usr/bin/env bash
set -euo pipefail

ENV_FILE="/etc/wireguard/wg-portal.env"
API_TOKEN="$(sudo awk -F= '/^WG_CONTROL_API_TOKEN=/{print substr($0,index($0,"=")+1)}' "$ENV_FILE" | tail -n1)"

echo -n "UPLINKS_JSON="
curl --max-time 10 -fsS -H "X-API-Token: ${API_TOKEN}" http://127.0.0.1:18110/v1/uplinks | tr -d '\n'
echo

echo -n "PEER_UPLINK_JSON="
curl --max-time 10 -fsS -H "X-API-Token: ${API_TOKEN}" http://127.0.0.1:18110/v1/peers/p75929c6eedd14d6a/uplink | tr -d '\n'
echo
