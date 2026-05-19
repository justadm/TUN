#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"

ssh "${EDG_HOST}" "sudo python3 - /etc/wireguard/wg-portal.env" <<'PY'
import pathlib
import sys

target = pathlib.Path(sys.argv[1])
text = target.read_text(encoding='utf-8', errors='replace').splitlines()
out = []
seen = False
for line in text:
    raw = line.rstrip('\n')
    if raw.strip().startswith('JSTUN_ADMIN_READ_MODE_DEFAULT='):
        out.append('JSTUN_ADMIN_READ_MODE_DEFAULT=shadow')
        seen = True
    else:
        out.append(raw)
if not seen:
    out.append('JSTUN_ADMIN_READ_MODE_DEFAULT=shadow')
target.write_text('\n'.join(out).rstrip() + '\n', encoding='utf-8')
PY

ssh "${EDG_HOST}" "sudo systemctl restart wg-portal-http.service && sudo systemctl is-active wg-portal-http.service"
ssh "${EDG_HOST}" "sudo awk -F= '/^JSTUN_ADMIN_READ_MODE_DEFAULT=/{print \$2}' /etc/wireguard/wg-portal.env"
