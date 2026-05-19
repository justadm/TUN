#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/rollout_tunrnd_edg_mesh.sh [options]

Rolls out persistent tun-rnd mesh links:
  edg <-> ams
  edg <-> fra
  edg <-> nyc

Actions:
  1) build linux/amd64 binaries locally
  2) install binaries + systemd templates on hosts
  3) render server/client env files
  4) apply strict UFW allows + nft inet/filter allows on edg
  5) ensure nft persist service on edg
  6) controlled restart sequence (stop clients -> restart servers -> start clients)

Options:
  --repo <path>                 default: current repo root
  --edg-host <ssh-host>         default: edg
  --ams-host <ssh-host>         default: ams
  --fra-host <ssh-host>         default: fra
  --nyc-host <ssh-host>         default: nyc
  --edg-public-ip <ip>          default: 85.239.44.100
  --server-id <hex16>           default: d1d2d3d4d5d6d7d8d9dadbdcdddedee0
  --server-static-priv <b64>    optional; if omitted, existing /etc/tun/server_static_priv.b64 is reused or generated
  --help                        show this help
EOF
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
edg_host="edg"
ams_host="ams"
fra_host="fra"
nyc_host="nyc"
edg_public_ip="85.239.44.100"
server_id="d1d2d3d4d5d6d7d8d9dadbdcdddedee0"
server_static_priv=""
nft_policy_file="${repo_root}/deploy/nft/edg-runtime-ingress.conf"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo) repo_root="${2:-}"; shift 2 ;;
    --edg-host) edg_host="${2:-}"; shift 2 ;;
    --ams-host) ams_host="${2:-}"; shift 2 ;;
    --fra-host) fra_host="${2:-}"; shift 2 ;;
    --nyc-host) nyc_host="${2:-}"; shift 2 ;;
    --edg-public-ip) edg_public_ip="${2:-}"; shift 2 ;;
    --server-id) server_id="${2:-}"; shift 2 ;;
    --server-static-priv) server_static_priv="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

tmp_bin="${repo_root}/.tmp/linux-bin"
mkdir -p "${tmp_bin}" "${repo_root}/.tmp/go-cache" "${repo_root}/.tmp/go-modcache"

echo "[rollout-edg] build linux binaries"
GOCACHE="${repo_root}/.tmp/go-cache" \
GOMODCACHE="${repo_root}/.tmp/go-modcache" \
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 /opt/homebrew/bin/go build -o "${tmp_bin}/runtime-server-systemd" "${repo_root}/cmd/runtime-server-systemd"
GOCACHE="${repo_root}/.tmp/go-cache" \
GOMODCACHE="${repo_root}/.tmp/go-modcache" \
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 /opt/homebrew/bin/go build -o "${tmp_bin}/runtime-client" "${repo_root}/cmd/runtime-client"
GOCACHE="${repo_root}/.tmp/go-cache" \
GOMODCACHE="${repo_root}/.tmp/go-modcache" \
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 /opt/homebrew/bin/go build -o "${tmp_bin}/runtime-preflight" "${repo_root}/cmd/runtime-preflight"

echo "[rollout-edg] install binaries"
scp "${tmp_bin}/runtime-server-systemd" "${edg_host}:/tmp/runtime-server-systemd"
scp "${tmp_bin}/runtime-preflight" "${edg_host}:/tmp/runtime-preflight"
for h in "${ams_host}" "${fra_host}" "${nyc_host}"; do
  scp "${tmp_bin}/runtime-client" "${h}:/tmp/runtime-client"
  scp "${tmp_bin}/runtime-preflight" "${h}:/tmp/runtime-preflight"
done
ssh "${edg_host}" "sudo -n install -m 0755 /tmp/runtime-server-systemd /usr/local/bin/runtime-server-systemd && sudo -n install -m 0755 /tmp/runtime-preflight /usr/local/bin/runtime-preflight"
for h in "${ams_host}" "${fra_host}" "${nyc_host}"; do
  ssh "${h}" "sudo -n install -m 0755 /tmp/runtime-client /usr/local/bin/runtime-client && sudo -n install -m 0755 /tmp/runtime-preflight /usr/local/bin/runtime-preflight"
done
ssh "${edg_host}" "sudo -n /usr/local/bin/runtime-server-systemd -help >/dev/null 2>&1 && sudo -n /usr/local/bin/runtime-preflight -help >/dev/null 2>&1"
for h in "${ams_host}" "${fra_host}" "${nyc_host}"; do
  ssh "${h}" "sudo -n /usr/local/bin/runtime-client -help >/dev/null 2>&1 && sudo -n /usr/local/bin/runtime-preflight -help >/dev/null 2>&1"
done

echo "[rollout-edg] install systemd templates"
scp "${repo_root}/deploy/systemd/tun-runtime-server@.service" "${edg_host}:/tmp/tun-runtime-server@.service"
scp "${repo_root}/deploy/systemd/tun-runtime-client@.service" "${ams_host}:/tmp/tun-runtime-client@.service"
scp "${repo_root}/deploy/systemd/tun-runtime-client@.service" "${fra_host}:/tmp/tun-runtime-client@.service"
scp "${repo_root}/deploy/systemd/tun-runtime-client@.service" "${nyc_host}:/tmp/tun-runtime-client@.service"
ssh "${edg_host}" "sudo -n install -m 0644 /tmp/tun-runtime-server@.service /etc/systemd/system/tun-runtime-server@.service && sudo -n mkdir -p /etc/tun /var/log/tun"
for h in "${ams_host}" "${fra_host}" "${nyc_host}"; do
  ssh "${h}" "sudo -n install -m 0644 /tmp/tun-runtime-client@.service /etc/systemd/system/tun-runtime-client@.service && sudo -n mkdir -p /etc/tun /var/log/tun"
done

echo "[rollout-edg] install runtime watchdog assets"
scp "${repo_root}/scripts/tun_runtime_link_watchdog.sh" "${edg_host}:/tmp/tun-runtime-link-watchdog.sh"
scp "${repo_root}/deploy/systemd/tun-runtime-server-watchdog@.service" "${edg_host}:/tmp/tun-runtime-server-watchdog@.service"
scp "${repo_root}/deploy/systemd/tun-runtime-server-watchdog@.timer" "${edg_host}:/tmp/tun-runtime-server-watchdog@.timer"
ssh "${edg_host}" "sudo -n install -m 0755 /tmp/tun-runtime-link-watchdog.sh /usr/local/sbin/tun-runtime-link-watchdog.sh"
ssh "${edg_host}" "sudo -n install -m 0644 /tmp/tun-runtime-server-watchdog@.service /etc/systemd/system/tun-runtime-server-watchdog@.service"
ssh "${edg_host}" "sudo -n install -m 0644 /tmp/tun-runtime-server-watchdog@.timer /etc/systemd/system/tun-runtime-server-watchdog@.timer"
for h in "${ams_host}" "${fra_host}" "${nyc_host}"; do
  scp "${repo_root}/scripts/tun_runtime_link_watchdog.sh" "${h}:/tmp/tun-runtime-link-watchdog.sh"
  scp "${repo_root}/deploy/systemd/tun-runtime-client-watchdog@.service" "${h}:/tmp/tun-runtime-client-watchdog@.service"
  scp "${repo_root}/deploy/systemd/tun-runtime-client-watchdog@.timer" "${h}:/tmp/tun-runtime-client-watchdog@.timer"
  ssh "${h}" "sudo -n install -m 0755 /tmp/tun-runtime-link-watchdog.sh /usr/local/sbin/tun-runtime-link-watchdog.sh"
  ssh "${h}" "sudo -n install -m 0644 /tmp/tun-runtime-client-watchdog@.service /etc/systemd/system/tun-runtime-client-watchdog@.service"
  ssh "${h}" "sudo -n install -m 0644 /tmp/tun-runtime-client-watchdog@.timer /etc/systemd/system/tun-runtime-client-watchdog@.timer"
done

if [[ -n "${server_static_priv}" ]]; then
  server_static_pub="$(
    python3 - "${server_static_priv}" <<'PY'
import base64, sys
from cryptography.hazmat.primitives.asymmetric import x25519
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat
priv = x25519.X25519PrivateKey.from_private_bytes(base64.b64decode(sys.argv[1]))
print(base64.b64encode(priv.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw)).decode())
PY
)"
else
  server_static_priv="$(ssh "${edg_host}" 'sudo test -f /etc/tun/server_static_priv.b64 && sudo cat /etc/tun/server_static_priv.b64 || true' | tr -d '\r\n')"
  if [[ -z "${server_static_priv}" ]]; then
    server_static_priv="$(
      python3 - <<'PY'
import base64
from cryptography.hazmat.primitives.asymmetric import x25519
print(base64.b64encode(x25519.X25519PrivateKey.generate().private_bytes_raw()).decode())
PY
)"
  fi
  server_static_pub="$(
    python3 - "${server_static_priv}" <<'PY'
import base64, sys
from cryptography.hazmat.primitives.asymmetric import x25519
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat
priv = x25519.X25519PrivateKey.from_private_bytes(base64.b64decode(sys.argv[1]))
print(base64.b64encode(priv.public_key().public_bytes(Encoding.Raw, PublicFormat.Raw)).decode())
PY
)"
fi

echo "[rollout-edg] render /etc/tun on edg"
ssh "${edg_host}" "sudo -n mkdir -p /etc/tun && sudo -n chmod 700 /etc/tun"
ssh "${edg_host}" "sudo -n sh -c 'cat > /etc/tun/server_static_priv.b64 <<EOF
${server_static_priv}
EOF
cat > /etc/tun/server_static_pub.b64 <<EOF
${server_static_pub}
EOF
cat > /etc/tun/runtime-server.env <<EOF
TUN_SERVER_ID=${server_id}
TUN_SERVER_STATIC_PRIV_B64=${server_static_priv}
TUN_TLS_CERT_FILE=/etc/tun/tls_cert.pem
TUN_TLS_KEY_FILE=/etc/tun/tls_key.pem
TUN_MTU=1420
TUN_ROUTES=
TUN_CONFIG_MODE=replace
TUN_CLEANUP_ON_CLOSE=true
TUN_SUPPORT_SIGNING_KEY_FILE=/etc/tun/support-signing-k2.key
TUN_SUPPORT_SIGNING_KEY_ID=k2
TUN_DEPLOY_RING=prod
EOF
cat > /etc/tun/runtime-server-ams.env <<EOF
TUN_LISTEN_ADDR=:18653
TUN_NAME=trsrv-ams-edg
TUN_ADDRESSES=10.254.1.1/30
TUN_HEALTH_ADDR=127.0.0.1:19153
TUN_SUPPORT_BUNDLE_OUT=/var/log/tun/support-bundle-ams-edg.json
EOF
cat > /etc/tun/runtime-server-fra.env <<EOF
TUN_LISTEN_ADDR=:18654
TUN_NAME=trsrv-fra-edg
TUN_ADDRESSES=10.254.2.1/30
TUN_HEALTH_ADDR=127.0.0.1:19154
TUN_SUPPORT_BUNDLE_OUT=/var/log/tun/support-bundle-fra-edg.json
EOF
cat > /etc/tun/runtime-server-nyc.env <<EOF
TUN_LISTEN_ADDR=:18655
TUN_NAME=trsrv-nyc-edg
TUN_ADDRESSES=10.254.3.1/30
TUN_HEALTH_ADDR=127.0.0.1:19155
TUN_SUPPORT_BUNDLE_OUT=/var/log/tun/support-bundle-nyc-edg.json
EOF
'"
ssh "${edg_host}" "if ! sudo -n test -f /etc/tun/tls_key.pem || ! sudo -n test -f /etc/tun/tls_cert.pem; then sudo -n openssl req -x509 -newkey rsa:2048 -nodes -keyout /etc/tun/tls_key.pem -out /etc/tun/tls_cert.pem -days 3650 -subj '/CN=edg.tun.local' >/dev/null 2>&1; fi"
ssh "${edg_host}" "sudo -n chmod 600 /etc/tun/server_static_priv.b64 /etc/tun/server_static_pub.b64 /etc/tun/tls_key.pem /etc/tun/tls_cert.pem /etc/tun/runtime-server.env /etc/tun/runtime-server-ams.env /etc/tun/runtime-server-fra.env /etc/tun/runtime-server-nyc.env"

echo "[rollout-edg] render client env files"
cat > "${repo_root}/.tmp/runtime-client-edg-ams.env" <<EOF
TUN_SERVER_ADDR=${edg_public_ip}:18653
TUN_SERVER_NAME=edg.tun.local
TUN_TLS_INSECURE=true
TUN_CLIENT_ID=d1d2d3d4d5d6d7d8d9dadbdcdddedf31
TUN_SERVER_STATIC_PUB_B64=${server_static_pub}
TUN_NAME=trcli-edg
TUN_MTU=1420
TUN_ADDRESSES=10.254.1.2/30
TUN_ROUTES=
TUN_CONFIG_MODE=replace
TUN_CLEANUP_ON_CLOSE=true
TUN_HEALTH_ADDR=127.0.0.1:19253
EOF
cat > "${repo_root}/.tmp/runtime-client-edg-fra.env" <<EOF
TUN_SERVER_ADDR=${edg_public_ip}:18654
TUN_SERVER_NAME=edg.tun.local
TUN_TLS_INSECURE=true
TUN_CLIENT_ID=d1d2d3d4d5d6d7d8d9dadbdcdddedf32
TUN_SERVER_STATIC_PUB_B64=${server_static_pub}
TUN_NAME=trcli-edg
TUN_MTU=1420
TUN_ADDRESSES=10.254.2.2/30
TUN_ROUTES=
TUN_CONFIG_MODE=replace
TUN_CLEANUP_ON_CLOSE=true
TUN_HEALTH_ADDR=127.0.0.1:19254
EOF
cat > "${repo_root}/.tmp/runtime-client-edg-nyc.env" <<EOF
TUN_SERVER_ADDR=${edg_public_ip}:18655
TUN_SERVER_NAME=edg.tun.local
TUN_TLS_INSECURE=true
TUN_CLIENT_ID=d1d2d3d4d5d6d7d8d9dadbdcdddedf33
TUN_SERVER_STATIC_PUB_B64=${server_static_pub}
TUN_NAME=trcli-edg
TUN_MTU=1420
TUN_ADDRESSES=10.254.3.2/30
TUN_ROUTES=
TUN_CONFIG_MODE=replace
TUN_CLEANUP_ON_CLOSE=true
TUN_HEALTH_ADDR=127.0.0.1:19255
EOF
scp "${repo_root}/.tmp/runtime-client-edg-ams.env" "${ams_host}:/tmp/runtime-client-edg.env"
scp "${repo_root}/.tmp/runtime-client-edg-fra.env" "${fra_host}:/tmp/runtime-client-edg.env"
scp "${repo_root}/.tmp/runtime-client-edg-nyc.env" "${nyc_host}:/tmp/runtime-client-edg.env"
for h in "${ams_host}" "${fra_host}" "${nyc_host}"; do
  ssh "${h}" "sudo -n install -m 600 /tmp/runtime-client-edg.env /etc/tun/runtime-client-edg.env"
done

echo "[rollout-edg] apply firewall rules on edg"
ssh "${edg_host}" "sudo -n ufw allow from 147.45.238.121 to any port 18653 proto tcp comment tun-edg-ams >/dev/null || true"
ssh "${edg_host}" "sudo -n ufw allow from 103.110.65.30 to any port 18654 proto tcp comment tun-edg-fra >/dev/null || true"
ssh "${edg_host}" "sudo -n ufw allow from 108.165.154.213 to any port 18655 proto tcp comment tun-edg-nyc >/dev/null || true"

echo "[rollout-edg] install managed nft ingress policy + reload service"
if [[ ! -f "${nft_policy_file}" ]]; then
  echo "missing nft policy file: ${nft_policy_file}" >&2
  exit 1
fi
scp "${nft_policy_file}" "${edg_host}:/tmp/edg-runtime-ingress.conf"
ssh "${edg_host}" "sudo -n install -m 0644 /tmp/edg-runtime-ingress.conf /etc/tun/nft-runtime-ingress.conf"
ssh "${edg_host}" "sudo -n sh -c 'cat > /usr/local/sbin/tun-runtime-nft-reload.sh <<'\''EOF'\''
#!/usr/bin/env bash
set -euo pipefail
policy_file=\"/etc/tun/nft-runtime-ingress.conf\"
if [[ ! -f \"\${policy_file}\" ]]; then
  echo \"missing policy file: \${policy_file}\" >&2
  exit 1
fi
family=\"\"
table=\"\"
chain=\"\"
if sudo nft list chain inet filter input >/dev/null 2>&1; then
  family=\"inet\"
  table=\"filter\"
  chain=\"input\"
elif sudo nft list chain ip filter ufw-user-input >/dev/null 2>&1; then
  family=\"ip\"
  table=\"filter\"
  chain=\"ufw-user-input\"
elif sudo nft list chain ip filter INPUT >/dev/null 2>&1; then
  family=\"ip\"
  table=\"filter\"
  chain=\"INPUT\"
else
  echo \"missing suitable nft input chain (inet/filter/input or ip/filter/ufw-user-input)\" >&2
  exit 1
fi
while read -r src port tag; do
  [[ -z \"\${src}\" ]] && continue
  [[ \"\${src:0:1}\" == \"#\" ]] && continue
  if sudo nft list chain \"\${family}\" \"\${table}\" \"\${chain}\" | grep -Fq \"ip saddr \${src} tcp dport \${port} accept comment \\\"\${tag}\\\"\"; then
    continue
  fi
  sudo nft add rule \"\${family}\" \"\${table}\" \"\${chain}\" ip saddr \"\${src}\" tcp dport \"\${port}\" accept comment \"\${tag}\"
done < \"\${policy_file}\"
EOF
chmod 0755 /usr/local/sbin/tun-runtime-nft-reload.sh
cat > /etc/systemd/system/tun-runtime-nft-reload.service <<EOF
[Unit]
Description=Reload tun-rnd managed nft ingress policy
After=network-online.target nftables.service ufw.service
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/tun-runtime-nft-reload.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
'"
ssh "${edg_host}" "sudo -n systemctl daemon-reload && sudo -n systemctl enable --now tun-runtime-nft-reload.service"
ssh "${edg_host}" "sudo -n /usr/local/sbin/tun-runtime-nft-reload.sh"
ssh "${edg_host}" "sudo -n systemctl disable --now tun-edg-nft-allow.service >/dev/null 2>&1 || true; sudo -n rm -f /etc/systemd/system/tun-edg-nft-allow.service /usr/local/sbin/tun-edg-nft-allow.sh; sudo -n systemctl daemon-reload"

echo "[rollout-edg] controlled restart sequence"
for h in "${ams_host}" "${fra_host}" "${nyc_host}"; do
  ssh "${h}" "sudo -n systemctl daemon-reload && sudo -n systemctl stop tun-runtime-client@edg.service || true"
done
ssh "${edg_host}" "sudo -n systemctl daemon-reload"
for s in ams fra nyc; do
  ssh "${edg_host}" "sudo -n systemctl enable --now tun-runtime-server@${s}.service && sudo -n systemctl restart tun-runtime-server@${s}.service"
  ssh "${edg_host}" "sudo -n systemctl enable --now tun-runtime-server-watchdog@${s}.timer"
done
ssh "${ams_host}" "sudo -n systemctl enable --now tun-runtime-client@edg.service && sudo -n systemctl restart tun-runtime-client@edg.service"
ssh "${fra_host}" "sudo -n systemctl enable --now tun-runtime-client@edg.service && sudo -n systemctl restart tun-runtime-client@edg.service"
ssh "${nyc_host}" "sudo -n systemctl enable --now tun-runtime-client@edg.service && sudo -n systemctl restart tun-runtime-client@edg.service"
ssh "${ams_host}" "sudo -n systemctl enable --now tun-runtime-client-watchdog@edg.timer"
ssh "${fra_host}" "sudo -n systemctl enable --now tun-runtime-client-watchdog@edg.timer"
ssh "${nyc_host}" "sudo -n systemctl enable --now tun-runtime-client-watchdog@edg.timer"

echo "[rollout-edg] quick health checks"
ssh "${edg_host}" "sudo -n systemctl is-active tun-runtime-server@ams.service tun-runtime-server@fra.service tun-runtime-server@nyc.service"
ssh "${ams_host}" "sudo -n systemctl is-active tun-runtime-client@edg.service && sudo -n ping -I trcli-edg -c 2 -W 2 10.254.1.1"
ssh "${fra_host}" "sudo -n systemctl is-active tun-runtime-client@edg.service && sudo -n ping -I trcli-edg -c 2 -W 2 10.254.2.1"
ssh "${nyc_host}" "sudo -n systemctl is-active tun-runtime-client@edg.service && sudo -n ping -I trcli-edg -c 2 -W 2 10.254.3.1"

echo "[rollout-edg] done"
