#!/usr/bin/env bash
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive

apt-get update
apt-get install -y fail2ban

rm -f /etc/ssh/sshd_config.d/100-fra-access-restore.conf

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

python3 - <<'PY'
from pathlib import Path
path = Path('/etc/ssh/sshd_config')
lines = path.read_text().splitlines()
out = []
port_written = False
for line in lines:
    if line.startswith('Port '):
        if not port_written:
            out.append('Port 65022')
            port_written = True
        continue
    out.append(line)
if not port_written:
    out.append('Port 65022')
path.write_text('\n'.join(out) + '\n')
PY

sshd -t
systemctl disable --now ssh.socket || true
systemctl restart ssh

ufw delete allow 22/tcp || true
ufw delete allow 22/tcp comment '' || true
ufw delete allow 22/tcp comment 'restore-temp' || true
ufw deny 22/tcp comment 'block-legacy-ssh' || true
ufw deny 22/tcp || true

systemctl enable --now fail2ban

printf '===SSH_EFFECTIVE===\n'
sshd -T | egrep '^(port|permitrootlogin|passwordauthentication|pubkeyauthentication|x11forwarding|allowtcpforwarding) '
printf '===UFW===\n'
ufw status numbered
printf '===SERVICES===\n'
systemctl list-unit-files --type=service --state=enabled | egrep 'ssh|ufw|ipsec|xl2tpd|wg-quick|zabbix|netfilter|fail2ban' || true
printf '===LISTEN===\n'
ss -lntup | egrep '(:22|:65022)\b' || true
