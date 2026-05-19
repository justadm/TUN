#!/usr/bin/env bash
set -euo pipefail

HOSTNAME_FQDN="bridge-fra"
PUB_IF="eth0"
WG_ADDR="10.200.0.6/24"
WG_PORT="51820"
WG_AMS_PUB="iE81pG7cjJSePXPG15inPCkEAhlE4iIcDfWpSW4eugY="
WG_AMS_ENDPOINT="147.45.238.121:51820"
OPSADMIN_KEYS=$'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAl/E8Oj2dK3Mxrv2ad8f9fBw64t34mUfjNk+Cn9Ad58 justadm@ya.ru\nssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIICmsuTYpfDq8O74d12ixk8ZWn2+FoWjyZ5K71GyFURT otp_vds@20251027111519'
L2TP_PSK='2246b7aaf88fc90a5fef1b66ef75aa888ef3bda4ae7ae6c9'
L2TP_USER='user'
L2TP_PASS='840a34b491e27d92b5ef9d1df95444a86ad5b809d671454f'

export DEBIAN_FRONTEND=noninteractive

apt-get update
apt-get install -y \
  wireguard \
  ufw \
  libreswan \
  xl2tpd

hostnamectl set-hostname "$HOSTNAME_FQDN"

if ! id -u opsadmin >/dev/null 2>&1; then
  adduser --disabled-password --gecos "" opsadmin
fi
usermod -aG sudo opsadmin

install -d -m 700 -o opsadmin -g opsadmin /home/opsadmin/.ssh
printf '%s\n' "$OPSADMIN_KEYS" > /home/opsadmin/.ssh/authorized_keys
chown opsadmin:opsadmin /home/opsadmin/.ssh/authorized_keys
chmod 600 /home/opsadmin/.ssh/authorized_keys

install -d -m 700 /root/.ssh
printf '%s\n' "$OPSADMIN_KEYS" > /root/.ssh/authorized_keys
chmod 600 /root/.ssh/authorized_keys

mkdir -p /etc/ssh/sshd_config.d
if ! grep -q '^Port 65022$' /etc/ssh/sshd_config; then
  perl -0pi -e 's/^#?Port\s+\d+\n/Port 65022\n/m or $_ .= "\nPort 65022\n"/e' /etc/ssh/sshd_config
fi
cat >/etc/ssh/sshd_config.d/99-p0-hardening.conf <<'EOF'
PermitRootLogin no
PasswordAuthentication no
PubkeyAuthentication yes
EOF
cat >/etc/ssh/sshd_config.d/92-p2-minimize.conf <<'EOF'
X11Forwarding no
AllowTcpForwarding no
EOF
cat >/etc/ssh/sshd_config.d/99-local-proxyjump.conf <<'EOF'
# Allow ProxyJump for opsadmin only.
Match User opsadmin
    AllowTcpForwarding yes
    PermitOpen any
EOF
if command -v sshd >/dev/null 2>&1; then
  sshd -t
else
  /usr/sbin/sshd -t
fi
systemctl restart ssh

cp /etc/sysctl.conf /etc/sysctl.conf.bak.$(date +%Y%m%d%H%M%S)
python3 - <<'PY'
from pathlib import Path
path = Path("/etc/sysctl.conf")
text = path.read_text()
block = """# BEGIN ANSIBLE MANAGED BLOCK
kernel.msgmnb = 65536
kernel.msgmax = 65536
kernel.shmmax = 68719476736
kernel.shmall = 4294967296

net.ipv4.ip_forward = 1
net.ipv4.conf.all.accept_source_route = 0
net.ipv4.conf.all.accept_redirects = 0
net.ipv4.conf.all.send_redirects = 0
net.ipv4.conf.all.rp_filter = 0
net.ipv4.conf.default.accept_source_route = 0
net.ipv4.conf.default.accept_redirects = 0
net.ipv4.conf.default.send_redirects = 0
net.ipv4.conf.default.rp_filter = 0
net.ipv4.conf.eth0.send_redirects = 0
net.ipv4.conf.eth0.rp_filter = 0

net.core.wmem_max = 12582912
net.core.rmem_max = 12582912
net.ipv4.tcp_rmem = 10240 87380 12582912
net.ipv4.tcp_wmem = 10240 87380 12582912
# END ANSIBLE MANAGED BLOCK
"""
start = "# BEGIN ANSIBLE MANAGED BLOCK"
end = "# END ANSIBLE MANAGED BLOCK"
if start in text and end in text:
    prefix = text.split(start)[0].rstrip()
    text = prefix + "\n\n" + block + "\n"
else:
    text = text.rstrip() + "\n\n" + block + "\n"
path.write_text(text)
PY
sysctl --system

install -d -m 700 /etc/wireguard
if [ ! -f /etc/wireguard/fra.key ]; then
  wg genkey | tee /etc/wireguard/fra.key | wg pubkey >/etc/wireguard/fra.pub
  chmod 600 /etc/wireguard/fra.key
  chmod 644 /etc/wireguard/fra.pub
fi
WG_PRIV="$(cat /etc/wireguard/fra.key)"
cat >/etc/wireguard/wg0.conf <<EOF
[Interface]
Address = ${WG_ADDR}
ListenPort = ${WG_PORT}
PrivateKey = ${WG_PRIV}
PostUp = sysctl -w net.ipv4.ip_forward=1
PostUp = iptables -t nat -A POSTROUTING -s 10.200.0.0/24 -o ${PUB_IF} -j MASQUERADE
PostDown = iptables -t nat -D POSTROUTING -s 10.200.0.0/24 -o ${PUB_IF} -j MASQUERADE

[Peer]
PublicKey = ${WG_AMS_PUB}
Endpoint = ${WG_AMS_ENDPOINT}
AllowedIPs = 10.200.0.1/32
PersistentKeepalive = 25
EOF
chmod 600 /etc/wireguard/wg0.conf

cat >/usr/local/bin/apply-fra-network-rules.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

ensure_rule() {
  local table="$1"
  shift
  if [ "$table" = nat ]; then
    iptables -t nat -C "$@" 2>/dev/null || iptables -t nat -A "$@"
  else
    iptables -C "$@" 2>/dev/null || iptables -I "$@"
  fi
}

ensure_rule filter FORWARD -i wg0 -o wg0 -s 10.200.0.0/24 -d 10.200.0.0/24 -j ACCEPT
ensure_rule nat POSTROUTING -s 10.200.0.0/24 -o eth0 -j MASQUERADE
ensure_rule nat POSTROUTING -s 192.168.42.0/24 -o eth0 -j MASQUERADE
ensure_rule filter FORWARD -s 192.168.42.0/24 -o eth0 -j ACCEPT
ensure_rule filter FORWARD -d 192.168.42.0/24 -i eth0 -m state --state RELATED,ESTABLISHED -j ACCEPT
EOF
chmod 755 /usr/local/bin/apply-fra-network-rules.sh

cat >/etc/systemd/system/fra-network-rules.service <<'EOF'
[Unit]
Description=Apply FRA iptables rules for WG and L2TP
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/apply-fra-network-rules.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF

cat >/etc/ipsec.conf <<'EOF'
version 2.0

config setup
  virtual-private=%v4:10.0.0.0/8,%v4:192.168.0.0/16,%v4:172.16.0.0/12,%v4:!192.168.42.0/24,%v4:!192.168.43.0/24
  protostack=netkey
  interfaces=%defaultroute
  uniqueids=no

conn shared
  left=%defaultroute
  leftid=103.110.65.30
  right=%any
  encapsulation=yes
  authby=secret
  pfs=no
  rekey=no
  keyingtries=5
  dpddelay=30
  dpdtimeout=120
  dpdaction=clear
  ikev2=never
  ike=aes256-sha2,aes128-sha2,aes256-sha1,aes128-sha1,aes256-sha2;modp1024,aes128-sha1;modp1024
  phase2alg=aes_gcm-null,aes128-sha1,aes256-sha1,aes256-sha2_512,aes128-sha2,aes256-sha2
  sha2-truncbug=no

conn l2tp-psk
  auto=add
  leftprotoport=17/1701
  rightprotoport=17/%any
  type=transport
  phase2=esp
  also=shared

conn xauth-psk
  auto=add
  leftsubnet=0.0.0.0/0
  rightaddresspool=192.168.43.10-192.168.43.250
  modecfgdns="8.8.8.8 8.8.4.4"
  leftxauthserver=yes
  rightxauthclient=yes
  leftmodecfgserver=yes
  rightmodecfgclient=yes
  modecfgpull=yes
  xauthby=file
  ike-frag=yes
  cisco-unity=yes
  also=shared
EOF

cat >/etc/ipsec.secrets <<EOF
include /etc/ipsec.d/*.secrets
%any %any : PSK "${L2TP_PSK}"
EOF

install -d -m 755 /etc/xl2tpd /etc/ppp
cat >/etc/xl2tpd/xl2tpd.conf <<'EOF'
[global]
port = 1701

[lns default]
ip range = 192.168.42.10-192.168.42.250
local ip = 192.168.42.1
require chap = yes
refuse pap = yes
require authentication = yes
name = l2tpd
pppoptfile = /etc/ppp/options.xl2tpd
length bit = yes
EOF

cat >/etc/ppp/options.xl2tpd <<'EOF'
+mschap-v2
ipcp-accept-local
ipcp-accept-remote
noccp
auth
mtu 1280
mru 1280
proxyarp
lcp-echo-failure 4
lcp-echo-interval 30
connect-delay 5000
ms-dns 8.8.8.8
ms-dns 8.8.4.4
EOF

cat >/etc/ppp/chap-secrets <<EOF
"${L2TP_USER}" l2tpd "${L2TP_PASS}" *
EOF
chmod 600 /etc/ipsec.secrets /etc/ppp/chap-secrets

ufw --force reset
sed -i 's/^DEFAULT_INPUT_POLICY=.*/DEFAULT_INPUT_POLICY="DROP"/' /etc/default/ufw
sed -i 's/^DEFAULT_OUTPUT_POLICY=.*/DEFAULT_OUTPUT_POLICY="ACCEPT"/' /etc/default/ufw
sed -i 's/^DEFAULT_FORWARD_POLICY=.*/DEFAULT_FORWARD_POLICY="DROP"/' /etc/default/ufw
ufw allow 500,1701,4500,51820/udp comment 'vpn-udp'
ufw allow 65022/tcp comment 'ssh-65022-no-rate-limit'
ufw deny 22/tcp comment 'block-legacy-ssh'
ufw route allow in on wg0 out on eth0 from 10.200.0.0/24 comment 'wg-client-egress'
ufw route allow in on eth0 out on wg0 to 10.200.0.0/24 comment 'wg-client-return'
ufw --force enable

systemctl daemon-reload
systemctl enable --now wg-quick@wg0 fra-network-rules ipsec xl2tpd ufw ssh

printf '\n===FRA_WG_PUB===\n'
cat /etc/wireguard/fra.pub
printf '\n===DONE===\n'
