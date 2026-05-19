#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/provision_tunrnd_link.sh [options]

Provision one tun-rnd link:
  server_host (tun-runtime-server@<link-name>)
    <-> client_host (tun-runtime-client@<client-instance>)

This script:
  - writes server/client env files
  - optionally opens UFW and nft ingress rules on server
  - enables and starts both systemd units

Important:
  - expects binaries and systemd templates already installed on hosts
  - expects /etc/tun/runtime-server.env and TLS/static keys already present on server

Required options:
  --server-host <host>
  --client-host <host>
  --link-name <name>                        systemd instance name for server unit
  --server-listen-port <port>
  --server-tun-name <name>
  --server-address <cidr>
  --server-health-addr <ip:port>
  --client-instance <name>                  systemd instance name for client unit
  --client-env-suffix <suffix>              /etc/tun/runtime-client-<suffix>.env
  --client-id <hex16>
  --server-static-pub-b64 <b64>
  --client-tun-name <name>
  --client-address <cidr>
  --client-health-addr <ip:port>
  --server-public-addr <ip-or-host:port>    client dial target
  --client-public-ip <ip>                   source ip for server firewall allow

Optional:
  --server-name <sni>                       default: localhost
  --tls-insecure true|false                 default: true
  --mtu <n>                                 default: 1420
  --cleanup-on-close true|false             default: true
  --server-post-up-hook <cmd>               optional hook command for server unit env (fail-safe)
  --client-post-up-hook <cmd>               optional hook command for client unit env (fail-safe)
  --config-mode <replace|merge|...>         default: replace
  --allow-ufw true|false                    default: true
  --allow-nft true|false                    default: true
  --enable-watchdog true|false              default: true
  --firewall-comment <text>                 default: tun-link-<link-name>
  --server-via <host>                       optional SSH jump host for server
  --client-via <host>                       optional SSH jump host for client
  --dry-run                                 print commands only
  -h, --help
EOF
}

server_host=""
client_host=""
link_name=""
server_listen_port=""
server_tun_name=""
server_address=""
server_health_addr=""
client_instance=""
client_env_suffix=""
client_id=""
server_static_pub_b64=""
client_tun_name=""
client_address=""
client_health_addr=""
server_public_addr=""
client_public_ip=""

server_name="localhost"
tls_insecure="true"
mtu="1420"
cleanup_on_close="true"
config_mode="replace"
server_post_up_hook=""
client_post_up_hook=""
allow_ufw="true"
allow_nft="true"
enable_watchdog="true"
firewall_comment=""
server_via=""
client_via=""
dry_run="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --server-host) server_host="${2:-}"; shift 2 ;;
    --client-host) client_host="${2:-}"; shift 2 ;;
    --link-name) link_name="${2:-}"; shift 2 ;;
    --server-listen-port) server_listen_port="${2:-}"; shift 2 ;;
    --server-tun-name) server_tun_name="${2:-}"; shift 2 ;;
    --server-address) server_address="${2:-}"; shift 2 ;;
    --server-health-addr) server_health_addr="${2:-}"; shift 2 ;;
    --client-instance) client_instance="${2:-}"; shift 2 ;;
    --client-env-suffix) client_env_suffix="${2:-}"; shift 2 ;;
    --client-id) client_id="${2:-}"; shift 2 ;;
    --server-static-pub-b64) server_static_pub_b64="${2:-}"; shift 2 ;;
    --client-tun-name) client_tun_name="${2:-}"; shift 2 ;;
    --client-address) client_address="${2:-}"; shift 2 ;;
    --client-health-addr) client_health_addr="${2:-}"; shift 2 ;;
    --server-public-addr) server_public_addr="${2:-}"; shift 2 ;;
    --client-public-ip) client_public_ip="${2:-}"; shift 2 ;;
    --server-name) server_name="${2:-}"; shift 2 ;;
    --tls-insecure) tls_insecure="${2:-}"; shift 2 ;;
    --mtu) mtu="${2:-}"; shift 2 ;;
    --cleanup-on-close) cleanup_on_close="${2:-}"; shift 2 ;;
    --server-post-up-hook) server_post_up_hook="${2:-}"; shift 2 ;;
    --client-post-up-hook) client_post_up_hook="${2:-}"; shift 2 ;;
    --config-mode) config_mode="${2:-}"; shift 2 ;;
    --allow-ufw) allow_ufw="${2:-}"; shift 2 ;;
    --allow-nft) allow_nft="${2:-}"; shift 2 ;;
    --enable-watchdog) enable_watchdog="${2:-}"; shift 2 ;;
    --firewall-comment) firewall_comment="${2:-}"; shift 2 ;;
    --server-via) server_via="${2:-}"; shift 2 ;;
    --client-via) client_via="${2:-}"; shift 2 ;;
    --dry-run) dry_run="true"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

for required in \
  server_host client_host link_name server_listen_port server_tun_name server_address server_health_addr \
  client_instance client_env_suffix client_id server_static_pub_b64 client_tun_name client_address client_health_addr \
  server_public_addr client_public_ip
do
  if [[ -z "${!required}" ]]; then
    echo "missing required option: --${required//_/-}" >&2
    exit 2
  fi
done

for b in tls_insecure cleanup_on_close allow_ufw allow_nft enable_watchdog; do
  case "${!b}" in
    true|false) ;;
    *) echo "invalid boolean for --${b//_/-}: ${!b}" >&2; exit 2 ;;
  esac
done

if [[ -z "${firewall_comment}" ]]; then
  firewall_comment="tun-link-${link_name}"
fi

ssh_prefix_for() {
  local host="$1"
  local via=""
  if [[ "${host}" == "${server_host}" ]]; then
    via="${server_via}"
  elif [[ "${host}" == "${client_host}" ]]; then
    via="${client_via}"
  fi
  if [[ -n "${via}" ]]; then
    printf 'ssh -J %q %q' "${via}" "${host}"
  else
    printf 'ssh %q' "${host}"
  fi
}

remote_run() {
  local host="$1"
  local cmd="$2"
  local pfx
  pfx="$(ssh_prefix_for "${host}")"
  if [[ "${dry_run}" == "true" ]]; then
    echo "+ ${pfx} ${cmd}"
    return 0
  fi
  eval "${pfx} \"$cmd\""
}

remote_copy() {
  local src="$1"
  local host="$2"
  local dst="$3"
  local via=""
  if [[ "${host}" == "${server_host}" ]]; then
    via="${server_via}"
  elif [[ "${host}" == "${client_host}" ]]; then
    via="${client_via}"
  fi
  if [[ "${dry_run}" == "true" ]]; then
    if [[ -n "${via}" ]]; then
      echo "+ scp -J ${via} ${src} ${host}:${dst}"
    else
      echo "+ scp ${src} ${host}:${dst}"
    fi
    return 0
  fi
  if [[ -n "${via}" ]]; then
    scp -J "${via}" "${src}" "${host}:${dst}"
  else
    scp "${src}" "${host}:${dst}"
  fi
}

quote_env_value() {
  local value="$1"
  value="${value//\'/\'\"\'\"\'}"
  printf "'%s'" "${value}"
}

server_post_up_hook_env="$(quote_env_value "${server_post_up_hook}")"
client_post_up_hook_env="$(quote_env_value "${client_post_up_hook}")"

echo "[provision] server=${server_host} client=${client_host} link=${link_name} dry_run=${dry_run}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
watchdog_script="${repo_root}/scripts/tun_runtime_link_watchdog.sh"
watchdog_client_svc="${repo_root}/deploy/systemd/tun-runtime-client-watchdog@.service"
watchdog_client_tmr="${repo_root}/deploy/systemd/tun-runtime-client-watchdog@.timer"
watchdog_server_svc="${repo_root}/deploy/systemd/tun-runtime-server-watchdog@.service"
watchdog_server_tmr="${repo_root}/deploy/systemd/tun-runtime-server-watchdog@.timer"

if [[ "${enable_watchdog}" == "true" ]]; then
  for f in "${watchdog_script}" "${watchdog_client_svc}" "${watchdog_client_tmr}" "${watchdog_server_svc}" "${watchdog_server_tmr}"; do
    if [[ ! -f "${f}" ]]; then
      echo "missing watchdog asset: ${f}" >&2
      exit 1
    fi
  done
fi

remote_run "${server_host}" "sudo -n mkdir -p /etc/tun /var/log/tun"
remote_run "${server_host}" "sudo -n sh -c 'cat > /etc/tun/runtime-server-${link_name}.env <<EOF
TUN_LISTEN_ADDR=:${server_listen_port}
TUN_NAME=${server_tun_name}
TUN_ADDRESSES=${server_address}
TUN_HEALTH_ADDR=${server_health_addr}
TUN_SUPPORT_BUNDLE_OUT=/var/log/tun/support-bundle-${link_name}.json
TUN_POST_UP_HOOK=${server_post_up_hook_env}
EOF'"
remote_run "${server_host}" "sudo -n chmod 600 /etc/tun/runtime-server-${link_name}.env"

remote_run "${client_host}" "sudo -n mkdir -p /etc/tun /var/log/tun"
remote_run "${client_host}" "sudo -n sh -c 'cat > /etc/tun/runtime-client-${client_env_suffix}.env <<EOF
TUN_SERVER_ADDR=${server_public_addr}
TUN_SERVER_NAME=${server_name}
TUN_TLS_INSECURE=${tls_insecure}
TUN_CLIENT_ID=${client_id}
TUN_SERVER_STATIC_PUB_B64=${server_static_pub_b64}
TUN_NAME=${client_tun_name}
TUN_MTU=${mtu}
TUN_ADDRESSES=${client_address}
TUN_ROUTES=
TUN_CONFIG_MODE=${config_mode}
TUN_CLEANUP_ON_CLOSE=${cleanup_on_close}
TUN_HEALTH_ADDR=${client_health_addr}
TUN_POST_UP_HOOK=${client_post_up_hook_env}
EOF'"
remote_run "${client_host}" "sudo -n chmod 600 /etc/tun/runtime-client-${client_env_suffix}.env"

if [[ "${allow_ufw}" == "true" ]]; then
  remote_run "${server_host}" "sudo -n ufw allow from ${client_public_ip} to any port ${server_listen_port} proto tcp comment '${firewall_comment}' >/dev/null || true"
fi

if [[ "${allow_nft}" == "true" ]]; then
  remote_run "${server_host}" "sudo -n sh -c 'if sudo nft list chain inet filter input >/dev/null 2>&1; then sudo nft add rule inet filter input ip saddr ${client_public_ip} tcp dport ${server_listen_port} accept comment \"${firewall_comment}\" 2>/dev/null || true; fi'"
fi

if [[ "${enable_watchdog}" == "true" ]]; then
  echo "[provision] install watchdog assets"
  remote_copy "${watchdog_script}" "${server_host}" "/tmp/tun-runtime-link-watchdog.sh"
  remote_copy "${watchdog_server_svc}" "${server_host}" "/tmp/tun-runtime-server-watchdog@.service"
  remote_copy "${watchdog_server_tmr}" "${server_host}" "/tmp/tun-runtime-server-watchdog@.timer"
  remote_run "${server_host}" "sudo -n install -m 0755 /tmp/tun-runtime-link-watchdog.sh /usr/local/sbin/tun-runtime-link-watchdog.sh"
  remote_run "${server_host}" "sudo -n install -m 0644 /tmp/tun-runtime-server-watchdog@.service /etc/systemd/system/tun-runtime-server-watchdog@.service"
  remote_run "${server_host}" "sudo -n install -m 0644 /tmp/tun-runtime-server-watchdog@.timer /etc/systemd/system/tun-runtime-server-watchdog@.timer"

  remote_copy "${watchdog_script}" "${client_host}" "/tmp/tun-runtime-link-watchdog.sh"
  remote_copy "${watchdog_client_svc}" "${client_host}" "/tmp/tun-runtime-client-watchdog@.service"
  remote_copy "${watchdog_client_tmr}" "${client_host}" "/tmp/tun-runtime-client-watchdog@.timer"
  remote_run "${client_host}" "sudo -n install -m 0755 /tmp/tun-runtime-link-watchdog.sh /usr/local/sbin/tun-runtime-link-watchdog.sh"
  remote_run "${client_host}" "sudo -n install -m 0644 /tmp/tun-runtime-client-watchdog@.service /etc/systemd/system/tun-runtime-client-watchdog@.service"
  remote_run "${client_host}" "sudo -n install -m 0644 /tmp/tun-runtime-client-watchdog@.timer /etc/systemd/system/tun-runtime-client-watchdog@.timer"
fi

remote_run "${server_host}" "sudo -n systemctl daemon-reload"
remote_run "${server_host}" "sudo -n systemctl enable --now tun-runtime-server@${link_name}.service"
remote_run "${client_host}" "sudo -n systemctl daemon-reload"
remote_run "${client_host}" "sudo -n systemctl enable --now tun-runtime-client@${client_instance}.service"
if [[ "${enable_watchdog}" == "true" ]]; then
  remote_run "${server_host}" "sudo -n systemctl enable --now tun-runtime-server-watchdog@${link_name}.timer"
  remote_run "${client_host}" "sudo -n systemctl enable --now tun-runtime-client-watchdog@${client_instance}.timer"
fi

echo "[provision] verify"
remote_run "${server_host}" "sudo -n systemctl is-active tun-runtime-server@${link_name}.service"
remote_run "${client_host}" "sudo -n systemctl is-active tun-runtime-client@${client_instance}.service"
if [[ "${enable_watchdog}" == "true" ]]; then
  remote_run "${server_host}" "sudo -n systemctl is-active tun-runtime-server-watchdog@${link_name}.timer"
  remote_run "${client_host}" "sudo -n systemctl is-active tun-runtime-client-watchdog@${client_instance}.timer"
fi
remote_run "${server_host}" "sudo -n systemctl status --no-pager tun-runtime-server@${link_name}.service | head -n 8"
remote_run "${client_host}" "sudo -n systemctl status --no-pager tun-runtime-client@${client_instance}.service | head -n 8"

echo "[provision] done"
