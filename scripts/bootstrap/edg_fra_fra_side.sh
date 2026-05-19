#!/usr/bin/env bash
set -euo pipefail

cat >/etc/wireguard/wg-edg-fra.conf <<EOF
[Interface]
Address = 10.202.0.2/30
ListenPort = 51824
PrivateKey = $(cat /etc/wireguard/edg-fra.key)

[Peer]
PublicKey = MC/KJOkAX5A6lJek+EaNL7po4sM9dALKmQNjXFI2wlo=
Endpoint = 85.239.44.100:51824
AllowedIPs = 10.202.0.1/32
PersistentKeepalive = 25
EOF

chmod 600 /etc/wireguard/wg-edg-fra.conf
ufw allow 51824/udp comment 'wg-fra' || true
systemctl enable --now wg-quick@wg-edg-fra
wg show wg-edg-fra
