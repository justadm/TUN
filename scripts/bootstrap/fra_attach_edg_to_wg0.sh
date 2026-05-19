#!/usr/bin/env bash
set -euo pipefail

python3 - <<'PY'
from pathlib import Path
p = Path('/etc/wireguard/wg0.conf')
text = p.read_text()
if '10.202.0.2/30' not in text:
    text = text.replace('Address = 10.200.0.6/24', 'Address = 10.200.0.6/24,10.202.0.2/30')
peer = '\n[Peer]\nPublicKey = MC/KJOkAX5A6lJek+EaNL7po4sM9dALKmQNjXFI2wlo=\nAllowedIPs = 10.202.0.1/32\nPersistentKeepalive = 25\n'
if 'MC/KJOkAX5A6lJek+EaNL7po4sM9dALKmQNjXFI2wlo=' not in text:
    text += peer
p.write_text(text)
PY

ufw allow in on wg0 from 10.202.0.1 comment 'wg-edg-fra-wg0' || true
systemctl restart wg-quick@wg0
wg show wg0
