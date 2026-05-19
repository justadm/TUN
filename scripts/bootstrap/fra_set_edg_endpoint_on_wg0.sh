#!/usr/bin/env bash
set -euo pipefail

python3 - <<'PY'
from pathlib import Path
p = Path('/etc/wireguard/wg0.conf')
text = p.read_text()
needle = 'PublicKey = MC/KJOkAX5A6lJek+EaNL7po4sM9dALKmQNjXFI2wlo='
if needle in text and 'Endpoint = 85.239.44.100:51824' not in text:
    text = text.replace(needle, needle + '\nEndpoint = 85.239.44.100:51824')
p.write_text(text)
PY

systemctl restart wg-quick@wg0
wg show wg0
