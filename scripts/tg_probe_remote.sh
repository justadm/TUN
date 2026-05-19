#!/usr/bin/env bash
set -euo pipefail

ENV_FILE=${1:-/etc/b24-remote-testing/telegram.env}
MSG=${2:-"MSK geo-sync TG probe"}

set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
set +a

echo "BOT_SET=$([[ -n ${TELEGRAM_BOT_TOKEN:-} ]] && echo yes || echo no)"
echo "CHAT_SET=$([[ -n ${TELEGRAM_CHAT_ID:-} ]] && echo yes || echo no)"

code=$(curl -s -o /tmp/tg_probe_getme.out -w "%{http_code}" "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/getMe")
echo "GETME_HTTP=$code"
head -c 400 /tmp/tg_probe_getme.out
printf '\n---\n'

code2=$(curl -s -o /tmp/tg_probe_send.out -w "%{http_code}" -X POST "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/sendMessage" -d "chat_id=${TELEGRAM_CHAT_ID}" --data-urlencode "text=${MSG}")
echo "SEND_HTTP=$code2"
head -c 400 /tmp/tg_probe_send.out
printf '\n'
