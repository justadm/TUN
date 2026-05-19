#!/usr/bin/env bash
set -euo pipefail

cat >/etc/ssh/sshd_config.d/100-fra-access-restore.conf <<'EOF'
Port 22
Port 65022
PermitRootLogin yes
PasswordAuthentication yes
PubkeyAuthentication yes
EOF

sshd -t
systemctl disable --now ssh.socket || true
systemctl restart ssh

if grep -q 'ike-frag=yes' /etc/ipsec.conf; then
  cp /etc/ipsec.conf /etc/ipsec.conf.bak.$(date +%Y%m%d%H%M%S)
  sed -i '/^[[:space:]]*ike-frag=yes$/d' /etc/ipsec.conf
fi

systemctl restart ipsec || true

printf '===SSH_EFFECTIVE===\n'
sshd -T | egrep '^(port|permitrootlogin|passwordauthentication|pubkeyauthentication) '
printf '===LISTEN===\n'
ss -lntup | egrep '(:22|:65022)\b' || true
printf '===IPSEC_CHECK===\n'
/usr/libexec/ipsec/addconn --config /etc/ipsec.conf --checkconfig || true
printf '===IPSEC_STATUS===\n'
systemctl status --no-pager --lines=40 ipsec || true
