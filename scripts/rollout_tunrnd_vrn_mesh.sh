#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/rollout_tunrnd_vrn_mesh.sh [options]

Rolls out persistent tun-rnd mesh links:
  vrn <-> ams
  vrn <-> fra
  vrn <-> nyc

Actions:
  1) build linux/amd64 binaries locally
  2) install binaries + systemd templates on hosts
  3) render server/client env files
  4) apply strict UFW allows + nft inet/filter allows on vrn
  5) ensure nft persist service on vrn
  6) controlled restart sequence (stop clients -> restart servers -> start clients)

Options:
  --repo <path>                 default: current repo root
  --vrn-host <ssh-host>         default: vrn
  --ams-host <ssh-host>         default: ams
  --fra-host <ssh-host>         default: fra
  --nyc-host <ssh-host>         default: nyc
  --vrn-public-ip <ip>          default: 91.221.109.60
  --server-id <hex16>           default: c1c2c3c4c5c6c7c8c9cacbcccdcecf10
  --server-static-priv <b64>    optional; if omitted, existing /etc/tun/server_static_priv.b64 is reused
  --server-static-pub <b64>     optional; required when --server-static-priv is set
  --help                        show this help
EOF
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
vrn_host="vrn"
ams_host="ams"
fra_host="fra"
nyc_host="nyc"
vrn_public_ip="91.221.109.60"
server_id="c1c2c3c4c5c6c7c8c9cacbcccdcecf10"
server_static_priv=""
server_static_pub=""
nft_policy_file="${repo_root}/deploy/nft/vrn-runtime-ingress.conf"

route_via_ams_host() {
  local host="$1"
  [[ "${host}" == "${fra_host}" || "${host}" == "${nyc_host}" ]]
}

remote_copy() {
  local src="$1"
  local host="$2"
  local dst="$3"
  if route_via_ams_host "${host}"; then
    scp -J "${ams_host}" "${src}" "${host}:${dst}"
  else
    scp "${src}" "${host}:${dst}"
  fi
}

remote_run() {
  local host="$1"
  local cmd="$2"
  if route_via_ams_host "${host}"; then
    ssh -J "${ams_host}" "${host}" "${cmd}"
  else
    ssh "${host}" "${cmd}"
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo) repo_root="${2:-}"; shift 2 ;;
    --vrn-host) vrn_host="${2:-}"; shift 2 ;;
    --ams-host) ams_host="${2:-}"; shift 2 ;;
    --fra-host) fra_host="${2:-}"; shift 2 ;;
    --nyc-host) nyc_host="${2:-}"; shift 2 ;;
    --vrn-public-ip) vrn_public_ip="${2:-}"; shift 2 ;;
    --server-id) server_id="${2:-}"; shift 2 ;;
    --server-static-priv) server_static_priv="${2:-}"; shift 2 ;;
    --server-static-pub) server_static_pub="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

tmp_bin="${repo_root}/.tmp/linux-bin"
mkdir -p "${tmp_bin}" "${repo_root}/.tmp/go-cache" "${repo_root}/.tmp/go-modcache"

echo "[rollout-vrn] build linux binaries"
GOCACHE="${repo_root}/.tmp/go-cache" \
GOMODCACHE="${repo_root}/.tmp/go-modcache" \
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 /opt/homebrew/bin/go build -o "${tmp_bin}/runtime-server-systemd" "${repo_root}/cmd/runtime-server-systemd"
GOCACHE="${repo_root}/.tmp/go-cache" \
GOMODCACHE="${repo_root}/.tmp/go-modcache" \
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 /opt/homebrew/bin/go build -o "${tmp_bin}/runtime-client" "${repo_root}/cmd/runtime-client"
GOCACHE="${repo_root}/.tmp/go-cache" \
GOMODCACHE="${repo_root}/.tmp/go-modcache" \
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 /opt/homebrew/bin/go build -o "${tmp_bin}/runtime-preflight" "${repo_root}/cmd/runtime-preflight"

echo "[rollout-vrn] install binaries"
remote_copy "${tmp_bin}/runtime-server-systemd" "${vrn_host}" "/tmp/runtime-server-systemd"
remote_copy "${tmp_bin}/runtime-preflight" "${vrn_host}" "/tmp/runtime-preflight"
for h in "${ams_host}" "${fra_host}" "${nyc_host}"; do
  remote_copy "${tmp_bin}/runtime-client" "${h}" "/tmp/runtime-client"
  remote_copy "${tmp_bin}/runtime-preflight" "${h}" "/tmp/runtime-preflight"
done
remote_run "${vrn_host}" "sudo -n install -m 0755 /tmp/runtime-server-systemd /usr/local/bin/runtime-server-systemd && sudo -n install -m 0755 /tmp/runtime-preflight /usr/local/bin/runtime-preflight"
for h in "${ams_host}" "${fra_host}" "${nyc_host}"; do
  remote_run "${h}" "sudo -n install -m 0755 /tmp/runtime-client /usr/local/bin/runtime-client && sudo -n install -m 0755 /tmp/runtime-preflight /usr/local/bin/runtime-preflight"
done
remote_run "${vrn_host}" "sudo -n /usr/local/bin/runtime-server-systemd -help >/dev/null 2>&1 && sudo -n /usr/local/bin/runtime-preflight -help >/dev/null 2>&1"
for h in "${ams_host}" "${fra_host}" "${nyc_host}"; do
  remote_run "${h}" "sudo -n /usr/local/bin/runtime-client -help >/dev/null 2>&1 && sudo -n /usr/local/bin/runtime-preflight -help >/dev/null 2>&1"
done

echo "[rollout-vrn] install systemd templates"
remote_copy "${repo_root}/deploy/systemd/tun-runtime-server@.service" "${vrn_host}" "/tmp/tun-runtime-server@.service"
remote_copy "${repo_root}/deploy/systemd/tun-runtime-client@.service" "${ams_host}" "/tmp/tun-runtime-client@.service"
remote_copy "${repo_root}/deploy/systemd/tun-runtime-client@.service" "${fra_host}" "/tmp/tun-runtime-client@.service"
remote_copy "${repo_root}/deploy/systemd/tun-runtime-client@.service" "${nyc_host}" "/tmp/tun-runtime-client@.service"
remote_run "${vrn_host}" "sudo -n install -m 0644 /tmp/tun-runtime-server@.service /etc/systemd/system/tun-runtime-server@.service && sudo -n mkdir -p /etc/tun /var/log/tun"
for h in "${ams_host}" "${fra_host}" "${nyc_host}"; do
  remote_run "${h}" "sudo -n install -m 0644 /tmp/tun-runtime-client@.service /etc/systemd/system/tun-runtime-client@.service && sudo -n mkdir -p /etc/tun /var/log/tun"
done

echo "[rollout-vrn] install runtime watchdog assets"
remote_copy "${repo_root}/scripts/tun_runtime_link_watchdog.sh" "${vrn_host}" "/tmp/tun-runtime-link-watchdog.sh"
remote_copy "${repo_root}/deploy/systemd/tun-runtime-server-watchdog@.service" "${vrn_host}" "/tmp/tun-runtime-server-watchdog@.service"
remote_copy "${repo_root}/deploy/systemd/tun-runtime-server-watchdog@.timer" "${vrn_host}" "/tmp/tun-runtime-server-watchdog@.timer"
remote_run "${vrn_host}" "sudo -n install -m 0755 /tmp/tun-runtime-link-watchdog.sh /usr/local/sbin/tun-runtime-link-watchdog.sh"
remote_run "${vrn_host}" "sudo -n install -m 0644 /tmp/tun-runtime-server-watchdog@.service /etc/systemd/system/tun-runtime-server-watchdog@.service"
remote_run "${vrn_host}" "sudo -n install -m 0644 /tmp/tun-runtime-server-watchdog@.timer /etc/systemd/system/tun-runtime-server-watchdog@.timer"
for h in "${ams_host}" "${fra_host}" "${nyc_host}"; do
  remote_copy "${repo_root}/scripts/tun_runtime_link_watchdog.sh" "${h}" "/tmp/tun-runtime-link-watchdog.sh"
  remote_copy "${repo_root}/deploy/systemd/tun-runtime-client-watchdog@.service" "${h}" "/tmp/tun-runtime-client-watchdog@.service"
  remote_copy "${repo_root}/deploy/systemd/tun-runtime-client-watchdog@.timer" "${h}" "/tmp/tun-runtime-client-watchdog@.timer"
  remote_run "${h}" "sudo -n install -m 0755 /tmp/tun-runtime-link-watchdog.sh /usr/local/sbin/tun-runtime-link-watchdog.sh"
  remote_run "${h}" "sudo -n install -m 0644 /tmp/tun-runtime-client-watchdog@.service /etc/systemd/system/tun-runtime-client-watchdog@.service"
  remote_run "${h}" "sudo -n install -m 0644 /tmp/tun-runtime-client-watchdog@.timer /etc/systemd/system/tun-runtime-client-watchdog@.timer"
done

if [[ -n "${server_static_priv}" ]]; then
  if [[ -z "${server_static_pub}" ]]; then
    echo "when --server-static-priv is provided, --server-static-pub is required" >&2
    exit 2
  fi
else
  server_static_priv="$(remote_run "${vrn_host}" 'sudo test -f /etc/tun/server_static_priv.b64 && sudo cat /etc/tun/server_static_priv.b64 || true' | tr -d '\r\n')"
  if [[ -z "${server_static_priv}" ]]; then
    echo "missing /etc/tun/server_static_priv.b64 on ${vrn_host}; pass --server-static-priv and --server-static-pub explicitly" >&2
    exit 1
  fi
  server_static_pub="$(remote_run "${vrn_host}" 'sudo test -f /etc/tun/server_static_pub.b64 && sudo cat /etc/tun/server_static_pub.b64 || true' | tr -d '\r\n')"
  if [[ -z "${server_static_pub}" ]]; then
    echo "missing /etc/tun/server_static_pub.b64 on ${vrn_host}; pass --server-static-pub explicitly" >&2
    exit 1
  fi
fi

echo "[rollout-vrn] render /etc/tun on vrn"
remote_run "${vrn_host}" "sudo -n mkdir -p /etc/tun && sudo -n chmod 700 /etc/tun"
remote_run "${vrn_host}" "sudo -n sh -c 'cat > /etc/tun/server_static_priv.b64 <<EOF
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
TUN_LISTEN_ADDR=:18643
TUN_NAME=trsrv-ams-vrn
TUN_ADDRESSES=10.253.1.1/30
TUN_HEALTH_ADDR=127.0.0.1:19083
TUN_SUPPORT_BUNDLE_OUT=/var/log/tun/support-bundle-ams-vrn.json
EOF
cat > /etc/tun/runtime-server-fra.env <<EOF
TUN_LISTEN_ADDR=:18644
TUN_NAME=trsrv-fra-vrn
TUN_ADDRESSES=10.253.2.1/30
TUN_HEALTH_ADDR=127.0.0.1:19084
TUN_SUPPORT_BUNDLE_OUT=/var/log/tun/support-bundle-fra-vrn.json
EOF
cat > /etc/tun/runtime-server-nyc.env <<EOF
TUN_LISTEN_ADDR=:18645
TUN_NAME=trsrv-nyc-vrn
TUN_ADDRESSES=10.253.3.1/30
TUN_HEALTH_ADDR=127.0.0.1:19085
TUN_SUPPORT_BUNDLE_OUT=/var/log/tun/support-bundle-nyc-vrn.json
EOF
'"
remote_run "${vrn_host}" "if ! sudo -n test -f /etc/tun/tls_key.pem || ! sudo -n test -f /etc/tun/tls_cert.pem; then sudo -n openssl req -x509 -newkey rsa:2048 -nodes -keyout /etc/tun/tls_key.pem -out /etc/tun/tls_cert.pem -days 3650 -subj '/CN=vrn.tun.local' >/dev/null 2>&1; fi"
remote_run "${vrn_host}" "sudo -n chmod 600 /etc/tun/server_static_priv.b64 /etc/tun/server_static_pub.b64 /etc/tun/tls_key.pem /etc/tun/tls_cert.pem /etc/tun/runtime-server.env /etc/tun/runtime-server-ams.env /etc/tun/runtime-server-fra.env /etc/tun/runtime-server-nyc.env"

echo "[rollout-vrn] render client env files"
cat > "${repo_root}/.tmp/runtime-client-vrn-ams.env" <<EOF
TUN_SERVER_ADDR=${vrn_public_ip}:18643
TUN_SERVER_NAME=localhost
TUN_TLS_INSECURE=true
TUN_CLIENT_ID=c1c2c3c4c5c6c7c8c9cacbcccdcecf21
TUN_SERVER_STATIC_PUB_B64=${server_static_pub}
TUN_NAME=trcli-vrn
TUN_MTU=1420
TUN_ADDRESSES=10.253.1.2/30
TUN_ROUTES=
TUN_CONFIG_MODE=replace
TUN_CLEANUP_ON_CLOSE=true
TUN_HEALTH_ADDR=127.0.0.1:19183
EOF
cat > "${repo_root}/.tmp/runtime-client-vrn-fra.env" <<EOF
TUN_SERVER_ADDR=${vrn_public_ip}:18644
TUN_SERVER_NAME=localhost
TUN_TLS_INSECURE=true
TUN_CLIENT_ID=c1c2c3c4c5c6c7c8c9cacbcccdcecf22
TUN_SERVER_STATIC_PUB_B64=${server_static_pub}
TUN_NAME=trcli-vrn
TUN_MTU=1420
TUN_ADDRESSES=10.253.2.2/30
TUN_ROUTES=
TUN_CONFIG_MODE=replace
TUN_CLEANUP_ON_CLOSE=true
TUN_HEALTH_ADDR=127.0.0.1:19184
EOF
cat > "${repo_root}/.tmp/runtime-client-vrn-nyc.env" <<EOF
TUN_SERVER_ADDR=${vrn_public_ip}:18645
TUN_SERVER_NAME=localhost
TUN_TLS_INSECURE=true
TUN_CLIENT_ID=c1c2c3c4c5c6c7c8c9cacbcccdcecf23
TUN_SERVER_STATIC_PUB_B64=${server_static_pub}
TUN_NAME=trcli-vrn
TUN_MTU=1420
TUN_ADDRESSES=10.253.3.2/30
TUN_ROUTES=
TUN_CONFIG_MODE=replace
TUN_CLEANUP_ON_CLOSE=true
TUN_HEALTH_ADDR=127.0.0.1:19185
EOF
remote_copy "${repo_root}/.tmp/runtime-client-vrn-ams.env" "${ams_host}" "/tmp/runtime-client-vrn.env"
remote_copy "${repo_root}/.tmp/runtime-client-vrn-fra.env" "${fra_host}" "/tmp/runtime-client-vrn.env"
remote_copy "${repo_root}/.tmp/runtime-client-vrn-nyc.env" "${nyc_host}" "/tmp/runtime-client-vrn.env"
for h in "${ams_host}" "${fra_host}" "${nyc_host}"; do
  remote_run "${h}" "sudo -n install -m 600 /tmp/runtime-client-vrn.env /etc/tun/runtime-client-vrn.env"
done

echo "[rollout-vrn] apply firewall rules on vrn"
remote_run "${vrn_host}" "sudo -n ufw allow from 147.45.238.121 to any port 18643 proto tcp comment tun-vrn-ams >/dev/null || true"
remote_run "${vrn_host}" "sudo -n ufw allow from 103.110.65.30 to any port 18644 proto tcp comment tun-vrn-fra >/dev/null || true"
remote_run "${vrn_host}" "sudo -n ufw allow from 108.165.154.213 to any port 18645 proto tcp comment tun-vrn-nyc >/dev/null || true"

echo "[rollout-vrn] install managed nft ingress policy + reload service"
if [[ ! -f "${nft_policy_file}" ]]; then
  echo "missing nft policy file: ${nft_policy_file}" >&2
  exit 1
fi
remote_copy "${nft_policy_file}" "${vrn_host}" "/tmp/vrn-runtime-ingress.conf"
remote_run "${vrn_host}" "sudo -n install -m 0644 /tmp/vrn-runtime-ingress.conf /etc/tun/nft-runtime-ingress.conf"
remote_run "${vrn_host}" "sudo -n sh -c 'cat > /usr/local/sbin/tun-runtime-nft-reload.sh <<'\''EOF'\''
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
remote_run "${vrn_host}" "sudo -n systemctl daemon-reload && sudo -n systemctl enable --now tun-runtime-nft-reload.service"
remote_run "${vrn_host}" "sudo -n /usr/local/sbin/tun-runtime-nft-reload.sh"
remote_run "${vrn_host}" "sudo -n systemctl disable --now tun-vrn-nft-allow.service >/dev/null 2>&1 || true; sudo -n rm -f /etc/systemd/system/tun-vrn-nft-allow.service /usr/local/sbin/tun-vrn-nft-allow.sh; sudo -n systemctl daemon-reload"

echo "[rollout-vrn] controlled restart sequence"
for h in "${ams_host}" "${fra_host}" "${nyc_host}"; do
  remote_run "${h}" "sudo -n systemctl daemon-reload && sudo -n systemctl stop tun-runtime-client@vrn.service || true"
done
remote_run "${vrn_host}" "sudo -n systemctl daemon-reload"
for s in ams fra nyc; do
  remote_run "${vrn_host}" "sudo -n systemctl enable --now tun-runtime-server@${s}.service && sudo -n systemctl restart tun-runtime-server@${s}.service"
  remote_run "${vrn_host}" "sudo -n systemctl enable --now tun-runtime-server-watchdog@${s}.timer"
done
remote_run "${ams_host}" "sudo -n systemctl enable --now tun-runtime-client@vrn.service && sudo -n systemctl restart tun-runtime-client@vrn.service"
remote_run "${fra_host}" "sudo -n systemctl enable --now tun-runtime-client@vrn.service && sudo -n systemctl restart tun-runtime-client@vrn.service"
remote_run "${nyc_host}" "sudo -n systemctl enable --now tun-runtime-client@vrn.service && sudo -n systemctl restart tun-runtime-client@vrn.service"
remote_run "${ams_host}" "sudo -n systemctl enable --now tun-runtime-client-watchdog@vrn.timer"
remote_run "${fra_host}" "sudo -n systemctl enable --now tun-runtime-client-watchdog@vrn.timer"
remote_run "${nyc_host}" "sudo -n systemctl enable --now tun-runtime-client-watchdog@vrn.timer"

echo "[rollout-vrn] quick health checks"
remote_run "${vrn_host}" "sudo -n systemctl is-active tun-runtime-server@ams.service tun-runtime-server@fra.service tun-runtime-server@nyc.service"
remote_run "${ams_host}" "sudo -n systemctl is-active tun-runtime-client@vrn.service && sudo -n ping -I trcli-vrn -c 2 -W 2 10.253.1.1"
remote_run "${fra_host}" "sudo -n systemctl is-active tun-runtime-client@vrn.service && sudo -n ping -I trcli-vrn -c 2 -W 2 10.253.2.1"
remote_run "${nyc_host}" "sudo -n systemctl is-active tun-runtime-client@vrn.service && sudo -n ping -I trcli-vrn -c 2 -W 2 10.253.3.1"

echo "[rollout-vrn] done"
