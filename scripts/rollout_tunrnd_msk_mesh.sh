#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/rollout_tunrnd_msk_mesh.sh [options]

Rolls out persistent tun-rnd mesh links:
  msk_d <-> ams
  msk_d <-> fra
  msk_d <-> nyc

Actions:
  1) build linux/amd64 binaries locally
  2) install binaries + systemd templates on hosts
  3) render link env files
  4) enable/start:
       msk: tun-runtime-server@{ams,fra,nyc}.service
       ams/fra/nyc: tun-runtime-client@msk.service
  5) apply strict UFW allow rules for msk listen ports

Options:
  --repo <path>                 default: current repo root
  --msk-host <ssh-host>         default: bx_msk_d
  --ams-host <ssh-host>         default: ams
  --fra-host <ssh-host>         default: fra
  --nyc-host <ssh-host>         default: nyc
  --msk-public-ip <ip>          default: 158.160.254.197
  --server-id <hex16>           default: a1a2a3a4a5a6a7a8a9aaabacadaeaf01
  --server-static-pub <base64>  default: read .tmp/pilot/server_static_pub.b64
  --help                        show this help
EOF
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
msk_host="bx_msk_d"
ams_host="ams"
fra_host="fra"
nyc_host="nyc"
msk_public_ip="158.160.254.197"
server_id="a1a2a3a4a5a6a7a8a9aaabacadaeaf01"
server_static_pub=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo) repo_root="${2:-}"; shift 2 ;;
    --msk-host) msk_host="${2:-}"; shift 2 ;;
    --ams-host) ams_host="${2:-}"; shift 2 ;;
    --fra-host) fra_host="${2:-}"; shift 2 ;;
    --nyc-host) nyc_host="${2:-}"; shift 2 ;;
    --msk-public-ip) msk_public_ip="${2:-}"; shift 2 ;;
    --server-id) server_id="${2:-}"; shift 2 ;;
    --server-static-pub) server_static_pub="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ -z "${server_static_pub}" ]]; then
  if [[ -f "${repo_root}/.tmp/pilot/server_static_pub.b64" ]]; then
    server_static_pub="$(tr -d '\r\n' < "${repo_root}/.tmp/pilot/server_static_pub.b64")"
  else
    echo "server static pub is required: pass --server-static-pub or create ${repo_root}/.tmp/pilot/server_static_pub.b64" >&2
    exit 1
  fi
fi

tmp_bin="${repo_root}/.tmp/linux-bin"
mkdir -p "${tmp_bin}"

echo "[rollout] build linux binaries"
GOCACHE="${repo_root}/.tmp/go-cache" \
GOMODCACHE="${repo_root}/.tmp/go-modcache" \
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 /opt/homebrew/bin/go build -o "${tmp_bin}/runtime-server-systemd" "${repo_root}/cmd/runtime-server-systemd"
GOCACHE="${repo_root}/.tmp/go-cache" \
GOMODCACHE="${repo_root}/.tmp/go-modcache" \
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 /opt/homebrew/bin/go build -o "${tmp_bin}/runtime-client" "${repo_root}/cmd/runtime-client"
GOCACHE="${repo_root}/.tmp/go-cache" \
GOMODCACHE="${repo_root}/.tmp/go-modcache" \
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 /opt/homebrew/bin/go build -o "${tmp_bin}/runtime-preflight" "${repo_root}/cmd/runtime-preflight"

links=(
  "ams:18443:10.251.1.1/30:10.251.1.2/30:${ams_host}:147.45.238.121:b1b2b3b4b5b6b7b8b9babbbcbdbebf11"
  "fra:18444:10.251.2.1/30:10.251.2.2/30:${fra_host}:103.110.65.30:b1b2b3b4b5b6b7b8b9babbbcbdbebf12"
  "nyc:18445:10.251.3.1/30:10.251.3.2/30:${nyc_host}:108.165.154.213:b1b2b3b4b5b6b7b8b9babbbcbdbebf13"
)

echo "[rollout] install binaries on msk/client hosts"
scp "${tmp_bin}/runtime-server-systemd" "${msk_host}:/tmp/runtime-server-systemd"
scp "${tmp_bin}/runtime-preflight" "${msk_host}:/tmp/runtime-preflight"
for link in "${links[@]}"; do
  IFS=':' read -r _ _ _ _ cli_host _ _ <<<"${link}"
  if [[ "${cli_host}" == "${fra_host}" || "${cli_host}" == "${nyc_host}" ]]; then
    scp -J "${ams_host}" "${tmp_bin}/runtime-client" "${cli_host}:/tmp/runtime-client"
    scp -J "${ams_host}" "${tmp_bin}/runtime-preflight" "${cli_host}:/tmp/runtime-preflight"
  else
    scp "${tmp_bin}/runtime-client" "${cli_host}:/tmp/runtime-client"
    scp "${tmp_bin}/runtime-preflight" "${cli_host}:/tmp/runtime-preflight"
  fi
done

ssh "${msk_host}" "sudo -n install -m 0755 /tmp/runtime-server-systemd /usr/local/bin/runtime-server-systemd && sudo -n install -m 0755 /tmp/runtime-preflight /usr/local/bin/runtime-preflight"
for link in "${links[@]}"; do
  IFS=':' read -r _ _ _ _ cli_host _ _ <<<"${link}"
  if [[ "${cli_host}" == "${fra_host}" || "${cli_host}" == "${nyc_host}" ]]; then
    ssh -J "${ams_host}" "${cli_host}" "sudo -n install -m 0755 /tmp/runtime-client /usr/local/bin/runtime-client && sudo -n install -m 0755 /tmp/runtime-preflight /usr/local/bin/runtime-preflight"
  else
    ssh "${cli_host}" "sudo -n install -m 0755 /tmp/runtime-client /usr/local/bin/runtime-client && sudo -n install -m 0755 /tmp/runtime-preflight /usr/local/bin/runtime-preflight"
  fi
done
ssh "${msk_host}" "sudo -n /usr/local/bin/runtime-server-systemd -help >/dev/null 2>&1 && sudo -n /usr/local/bin/runtime-preflight -help >/dev/null 2>&1"
for link in "${links[@]}"; do
  IFS=':' read -r _ _ _ _ cli_host _ _ <<<"${link}"
  if [[ "${cli_host}" == "${fra_host}" || "${cli_host}" == "${nyc_host}" ]]; then
    ssh -J "${ams_host}" "${cli_host}" "sudo -n /usr/local/bin/runtime-client -help >/dev/null 2>&1 && sudo -n /usr/local/bin/runtime-preflight -help >/dev/null 2>&1"
  else
    ssh "${cli_host}" "sudo -n /usr/local/bin/runtime-client -help >/dev/null 2>&1 && sudo -n /usr/local/bin/runtime-preflight -help >/dev/null 2>&1"
  fi
done

echo "[rollout] install systemd templates"
scp "${repo_root}/deploy/systemd/tun-runtime-server@.service" "${msk_host}:/tmp/tun-runtime-server@.service"
scp "${repo_root}/deploy/systemd/tun-runtime-client@.service" "${ams_host}:/tmp/tun-runtime-client@.service"
scp -J "${ams_host}" "${repo_root}/deploy/systemd/tun-runtime-client@.service" "${fra_host}:/tmp/tun-runtime-client@.service"
scp -J "${ams_host}" "${repo_root}/deploy/systemd/tun-runtime-client@.service" "${nyc_host}:/tmp/tun-runtime-client@.service"

ssh "${msk_host}" "sudo -n install -m 0644 /tmp/tun-runtime-server@.service /etc/systemd/system/tun-runtime-server@.service && sudo -n mkdir -p /etc/tun /var/log/tun"
ssh "${ams_host}" "sudo -n install -m 0644 /tmp/tun-runtime-client@.service /etc/systemd/system/tun-runtime-client@.service && sudo -n mkdir -p /etc/tun /var/log/tun"
ssh -J "${ams_host}" "${fra_host}" "sudo -n install -m 0644 /tmp/tun-runtime-client@.service /etc/systemd/system/tun-runtime-client@.service && sudo -n mkdir -p /etc/tun /var/log/tun"
ssh -J "${ams_host}" "${nyc_host}" "sudo -n install -m 0644 /tmp/tun-runtime-client@.service /etc/systemd/system/tun-runtime-client@.service && sudo -n mkdir -p /etc/tun /var/log/tun"

echo "[rollout] install runtime watchdog assets"
scp "${repo_root}/scripts/tun_runtime_link_watchdog.sh" "${msk_host}:/tmp/tun-runtime-link-watchdog.sh"
scp "${repo_root}/deploy/systemd/tun-runtime-server-watchdog@.service" "${msk_host}:/tmp/tun-runtime-server-watchdog@.service"
scp "${repo_root}/deploy/systemd/tun-runtime-server-watchdog@.timer" "${msk_host}:/tmp/tun-runtime-server-watchdog@.timer"
ssh "${msk_host}" "sudo -n install -m 0755 /tmp/tun-runtime-link-watchdog.sh /usr/local/sbin/tun-runtime-link-watchdog.sh"
ssh "${msk_host}" "sudo -n install -m 0644 /tmp/tun-runtime-server-watchdog@.service /etc/systemd/system/tun-runtime-server-watchdog@.service"
ssh "${msk_host}" "sudo -n install -m 0644 /tmp/tun-runtime-server-watchdog@.timer /etc/systemd/system/tun-runtime-server-watchdog@.timer"
scp "${repo_root}/scripts/tun_runtime_link_watchdog.sh" "${ams_host}:/tmp/tun-runtime-link-watchdog.sh"
scp "${repo_root}/deploy/systemd/tun-runtime-client-watchdog@.service" "${ams_host}:/tmp/tun-runtime-client-watchdog@.service"
scp "${repo_root}/deploy/systemd/tun-runtime-client-watchdog@.timer" "${ams_host}:/tmp/tun-runtime-client-watchdog@.timer"
ssh "${ams_host}" "sudo -n install -m 0755 /tmp/tun-runtime-link-watchdog.sh /usr/local/sbin/tun-runtime-link-watchdog.sh"
ssh "${ams_host}" "sudo -n install -m 0644 /tmp/tun-runtime-client-watchdog@.service /etc/systemd/system/tun-runtime-client-watchdog@.service"
ssh "${ams_host}" "sudo -n install -m 0644 /tmp/tun-runtime-client-watchdog@.timer /etc/systemd/system/tun-runtime-client-watchdog@.timer"
scp -J "${ams_host}" "${repo_root}/scripts/tun_runtime_link_watchdog.sh" "${fra_host}:/tmp/tun-runtime-link-watchdog.sh"
scp -J "${ams_host}" "${repo_root}/deploy/systemd/tun-runtime-client-watchdog@.service" "${fra_host}:/tmp/tun-runtime-client-watchdog@.service"
scp -J "${ams_host}" "${repo_root}/deploy/systemd/tun-runtime-client-watchdog@.timer" "${fra_host}:/tmp/tun-runtime-client-watchdog@.timer"
ssh -J "${ams_host}" "${fra_host}" "sudo -n install -m 0755 /tmp/tun-runtime-link-watchdog.sh /usr/local/sbin/tun-runtime-link-watchdog.sh"
ssh -J "${ams_host}" "${fra_host}" "sudo -n install -m 0644 /tmp/tun-runtime-client-watchdog@.service /etc/systemd/system/tun-runtime-client-watchdog@.service"
ssh -J "${ams_host}" "${fra_host}" "sudo -n install -m 0644 /tmp/tun-runtime-client-watchdog@.timer /etc/systemd/system/tun-runtime-client-watchdog@.timer"
scp -J "${ams_host}" "${repo_root}/scripts/tun_runtime_link_watchdog.sh" "${nyc_host}:/tmp/tun-runtime-link-watchdog.sh"
scp -J "${ams_host}" "${repo_root}/deploy/systemd/tun-runtime-client-watchdog@.service" "${nyc_host}:/tmp/tun-runtime-client-watchdog@.service"
scp -J "${ams_host}" "${repo_root}/deploy/systemd/tun-runtime-client-watchdog@.timer" "${nyc_host}:/tmp/tun-runtime-client-watchdog@.timer"
ssh -J "${ams_host}" "${nyc_host}" "sudo -n install -m 0755 /tmp/tun-runtime-link-watchdog.sh /usr/local/sbin/tun-runtime-link-watchdog.sh"
ssh -J "${ams_host}" "${nyc_host}" "sudo -n install -m 0644 /tmp/tun-runtime-client-watchdog@.service /etc/systemd/system/tun-runtime-client-watchdog@.service"
ssh -J "${ams_host}" "${nyc_host}" "sudo -n install -m 0644 /tmp/tun-runtime-client-watchdog@.timer /etc/systemd/system/tun-runtime-client-watchdog@.timer"

echo "[rollout] ensure msk base server env exists"
ssh "${msk_host}" "sudo -n test -f /etc/tun/runtime-server.env || (priv=\$(sudo -n cat /etc/tun/server_static_priv.b64 | tr -d '\r\n'); sudo -n sh -c \"cat > /etc/tun/runtime-server.env <<EOF
TUN_SERVER_ID=${server_id}
TUN_SERVER_STATIC_PRIV_B64=\${priv}
TUN_TLS_CERT_FILE=/etc/tun/tls_cert.pem
TUN_TLS_KEY_FILE=/etc/tun/tls_key.pem
TUN_MTU=1420
TUN_CONFIG_MODE=replace
TUN_CLEANUP_ON_CLOSE=true
TUN_SUPPORT_SIGNING_KEY_FILE=/etc/tun/support-signing-k2.key
TUN_SUPPORT_SIGNING_KEY_ID=k2
TUN_DEPLOY_RING=prod
EOF
\" && sudo -n chmod 600 /etc/tun/runtime-server.env)"
ssh "${msk_host}" "sudo -n sh -c 'v=\$(grep -E \"^TUN_SERVER_STATIC_PRIV_B64=\" /etc/tun/runtime-server.env | tail -n1 | cut -d= -f2-); if [ -z \"\$v\" ]; then p=\$(cat /etc/tun/server_static_priv.b64 | tr -d \"\r\n\"); sed -i \"s|^TUN_SERVER_STATIC_PRIV_B64=.*|TUN_SERVER_STATIC_PRIV_B64=\$p|\" /etc/tun/runtime-server.env; fi'"
ssh "${msk_host}" "sudo -n sh -c 'grep -q \"^TUN_TLS_CERT_FILE=\" /etc/tun/runtime-server.env || echo \"TUN_TLS_CERT_FILE=/etc/tun/tls_cert.pem\" >> /etc/tun/runtime-server.env'"
ssh "${msk_host}" "sudo -n sh -c 'grep -q \"^TUN_TLS_KEY_FILE=\" /etc/tun/runtime-server.env || echo \"TUN_TLS_KEY_FILE=/etc/tun/tls_key.pem\" >> /etc/tun/runtime-server.env'"
ssh "${msk_host}" "sudo -n sh -c 'grep -q \"^TUN_SUPPORT_SIGNING_KEY_FILE=\" /etc/tun/runtime-server.env || echo \"TUN_SUPPORT_SIGNING_KEY_FILE=/etc/tun/support-signing-k2.key\" >> /etc/tun/runtime-server.env'"
ssh "${msk_host}" "sudo -n sh -c 'grep -q \"^TUN_SUPPORT_SIGNING_KEY_ID=\" /etc/tun/runtime-server.env || echo \"TUN_SUPPORT_SIGNING_KEY_ID=k2\" >> /etc/tun/runtime-server.env'"
ssh "${msk_host}" "sudo -n sh -c 'grep -q \"^TUN_DEPLOY_RING=\" /etc/tun/runtime-server.env || echo \"TUN_DEPLOY_RING=prod\" >> /etc/tun/runtime-server.env'"

echo "[rollout] render per-link env files"
for link in "${links[@]}"; do
  IFS=':' read -r link_name port srv_cidr cli_cidr cli_host cli_ip cli_id <<<"${link}"
  srv_env="$(mktemp)"
  cat > "${srv_env}" <<EOF
TUN_LISTEN_ADDR=:${port}
TUN_NAME=trsrv-${link_name}
TUN_MTU=1420
TUN_ADDRESSES=${srv_cidr}
TUN_ROUTES=
TUN_CONFIG_MODE=replace
TUN_CLEANUP_ON_CLOSE=true
TUN_HEALTH_ADDR=127.0.0.1:$((18080 + port - 18440))
TUN_SUPPORT_BUNDLE_OUT=/var/log/tun/support-bundle-${link_name}.json
EOF
  scp "${srv_env}" "${msk_host}:/tmp/runtime-server-${link_name}.env"
  rm -f "${srv_env}"
  ssh "${msk_host}" "sudo -n install -m 600 /tmp/runtime-server-${link_name}.env /etc/tun/runtime-server-${link_name}.env"

  cli_env="$(mktemp)"
  cat > "${cli_env}" <<EOF
TUN_SERVER_ADDR=${msk_public_ip}:${port}
TUN_SERVER_NAME=localhost
TUN_TLS_INSECURE=true
TUN_CLIENT_ID=${cli_id}
TUN_SERVER_STATIC_PUB_B64=${server_static_pub}
TUN_NAME=trcli-msk
TUN_MTU=1420
TUN_ADDRESSES=${cli_cidr}
TUN_ROUTES=
TUN_CONFIG_MODE=replace
TUN_CLEANUP_ON_CLOSE=true
TUN_HEALTH_ADDR=127.0.0.1:$((18180 + port - 18440))
EOF
  if [[ "${cli_host}" == "${fra_host}" || "${cli_host}" == "${nyc_host}" ]]; then
    scp -J "${ams_host}" "${cli_env}" "${cli_host}:/tmp/runtime-client-msk.env"
    ssh -J "${ams_host}" "${cli_host}" "sudo -n install -m 600 /tmp/runtime-client-msk.env /etc/tun/runtime-client-msk.env"
  else
    scp "${cli_env}" "${cli_host}:/tmp/runtime-client-msk.env"
    ssh "${cli_host}" "sudo -n install -m 600 /tmp/runtime-client-msk.env /etc/tun/runtime-client-msk.env"
  fi
  rm -f "${cli_env}"
done

echo "[rollout] apply msk firewall rules"
for link in "${links[@]}"; do
  IFS=':' read -r _ port _ _ _ cli_ip _ <<<"${link}"
  ssh "${msk_host}" "sudo -n ufw allow proto tcp from ${cli_ip} to any port ${port} comment tun-rnd-mesh-${port} >/dev/null"
done

echo "[rollout] reload systemd and start units"
ssh "${msk_host}" "sudo -n systemctl daemon-reload"
for link in "${links[@]}"; do
  IFS=':' read -r link_name _ _ _ _ _ _ <<<"${link}"
  ssh "${msk_host}" "sudo -n systemctl enable --now tun-runtime-server@${link_name}.service"
  ssh "${msk_host}" "sudo -n systemctl enable --now tun-runtime-server-watchdog@${link_name}.timer"
done
ssh "${ams_host}" "sudo -n systemctl daemon-reload && sudo -n systemctl enable --now tun-runtime-client@msk.service"
ssh -J "${ams_host}" "${fra_host}" "sudo -n systemctl daemon-reload && sudo -n systemctl enable --now tun-runtime-client@msk.service"
ssh -J "${ams_host}" "${nyc_host}" "sudo -n systemctl daemon-reload && sudo -n systemctl enable --now tun-runtime-client@msk.service"
ssh "${ams_host}" "sudo -n systemctl enable --now tun-runtime-client-watchdog@msk.timer"
ssh -J "${ams_host}" "${fra_host}" "sudo -n systemctl enable --now tun-runtime-client-watchdog@msk.timer"
ssh -J "${ams_host}" "${nyc_host}" "sudo -n systemctl enable --now tun-runtime-client-watchdog@msk.timer"

echo "[rollout] health checks"
ssh "${msk_host}" "sudo -n systemctl --no-pager --full status tun-runtime-server@ams.service tun-runtime-server@fra.service tun-runtime-server@nyc.service | sed -n '1,120p'"
ssh "${ams_host}" "sudo -n systemctl --no-pager --full status tun-runtime-client@msk.service | sed -n '1,80p'"
ssh -J "${ams_host}" "${fra_host}" "sudo -n systemctl --no-pager --full status tun-runtime-client@msk.service | sed -n '1,80p'"
ssh -J "${ams_host}" "${nyc_host}" "sudo -n systemctl --no-pager --full status tun-runtime-client@msk.service | sed -n '1,80p'"

echo "[rollout] done"
