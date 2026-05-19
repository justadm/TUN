#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/decommission_tunrnd_contour.sh [options]

Decommissions tun-rnd contour links:
  - stops/disables systemd units
  - removes contour env files from /etc/tun
  - removes UFW rules by contour comment
  - removes nft rules by contour comment (inet/filter input)
  - optionally removes tun-runtime-nft-reload stack

Options:
  --contour <edg|vrn|both>          default: both
  --edg-host <host>                  default: edg
  --vrn-host <host>                  default: vrn
  --ams-host <host>                  default: ams
  --fra-host <host>                  default: fra
  --nyc-host <host>                  default: nyc
  --edg-via <host>                   optional SSH jump host for edg operations
  --vrn-via <host>                   optional SSH jump host for vrn operations
  --fra-via <host>                   optional SSH jump host for fra operations
  --nyc-via <host>                   optional SSH jump host for nyc operations
  --remove-nft-reload true|false     default: true
  --dry-run                          print commands without execution
  -h, --help                         show help
EOF
}

contour="both"
edg_host="edg"
vrn_host="vrn"
ams_host="ams"
fra_host="fra"
nyc_host="nyc"
edg_via=""
vrn_via=""
fra_via=""
nyc_via=""
remove_nft_reload="true"
dry_run="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --contour) contour="${2:-}"; shift 2 ;;
    --edg-host) edg_host="${2:-}"; shift 2 ;;
    --vrn-host) vrn_host="${2:-}"; shift 2 ;;
    --ams-host) ams_host="${2:-}"; shift 2 ;;
    --fra-host) fra_host="${2:-}"; shift 2 ;;
    --nyc-host) nyc_host="${2:-}"; shift 2 ;;
    --edg-via) edg_via="${2:-}"; shift 2 ;;
    --vrn-via) vrn_via="${2:-}"; shift 2 ;;
    --fra-via) fra_via="${2:-}"; shift 2 ;;
    --nyc-via) nyc_via="${2:-}"; shift 2 ;;
    --remove-nft-reload) remove_nft_reload="${2:-}"; shift 2 ;;
    --dry-run) dry_run="true"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

case "${contour}" in
  edg|vrn|both) ;;
  *) echo "invalid --contour: ${contour}" >&2; exit 2 ;;
esac

case "${remove_nft_reload}" in
  true|false) ;;
  *) echo "invalid --remove-nft-reload: ${remove_nft_reload}" >&2; exit 2 ;;
esac

run_cmd() {
  if [[ "${dry_run}" == "true" ]]; then
    echo "+ $*"
    return 0
  fi
  "$@"
}

ssh_cmd_for() {
  local host="$1"
  local via=""
  case "${host}" in
    "${edg_host}") via="${edg_via}" ;;
    "${vrn_host}") via="${vrn_via}" ;;
    "${fra_host}") via="${fra_via}" ;;
    "${nyc_host}") via="${nyc_via}" ;;
  esac
  if [[ -n "${via}" ]]; then
    printf 'ssh -J %q %q' "${via}" "${host}"
  else
    printf 'ssh %q' "${host}"
  fi
}

remote_run() {
  local host="$1"
  local cmd="$2"
  local ssh_prefix
  ssh_prefix="$(ssh_cmd_for "${host}")"
  if [[ "${dry_run}" == "true" ]]; then
    echo "+ ${ssh_prefix} ${cmd}"
    return 0
  fi
  eval "${ssh_prefix} \"$cmd\""
}

decommission_clients_for_contour() {
  local contour_name="$1" # edg|vrn
  local host
  for host in "${ams_host}" "${fra_host}" "${nyc_host}"; do
    remote_run "${host}" "sudo -n systemctl stop tun-runtime-client@${contour_name}.service || true"
    remote_run "${host}" "sudo -n systemctl disable tun-runtime-client@${contour_name}.service || true"
    remote_run "${host}" "sudo -n rm -f /etc/tun/runtime-client-${contour_name}.env"
  done
}

delete_ufw_rules_by_comment() {
  local host="$1"
  local comment_prefix="$2"
  remote_run "${host}" "set -e; nums=\$(sudo -n ufw status numbered | grep \"${comment_prefix}\" | sed -E 's/^\\[ *([0-9]+)\\].*/\\1/' | sort -rn || true); for n in \$nums; do sudo -n ufw --force delete \"\$n\" >/dev/null; done"
}

delete_nft_rules_by_comment() {
  local host="$1"
  local comment_prefix="$2"
  remote_run "${host}" "set -e; handles=\$(sudo -n nft -a list chain inet filter input 2>/dev/null | grep \"comment \\\"${comment_prefix}\" | sed -E 's/.*# handle ([0-9]+).*/\\1/' || true); for h in \$handles; do sudo -n nft delete rule inet filter input handle \"\$h\" || true; done"
}

remove_nft_reload_stack() {
  local host="$1"
  remote_run "${host}" "sudo -n systemctl disable --now tun-runtime-nft-reload.service || true"
  remote_run "${host}" "sudo -n rm -f /etc/tun/nft-runtime-ingress.conf /usr/local/sbin/tun-runtime-nft-reload.sh /etc/systemd/system/tun-runtime-nft-reload.service"
  remote_run "${host}" "sudo -n systemctl daemon-reload"
}

decommission_server_contour() {
  local server_host="$1"
  local contour_name="$2" # edg|vrn
  local tag_prefix="tun-${contour_name}-"

  remote_run "${server_host}" "sudo -n systemctl stop tun-runtime-server@ams.service tun-runtime-server@fra.service tun-runtime-server@nyc.service || true"
  remote_run "${server_host}" "sudo -n systemctl disable tun-runtime-server@ams.service tun-runtime-server@fra.service tun-runtime-server@nyc.service || true"
  remote_run "${server_host}" "sudo -n rm -f /etc/tun/runtime-server-ams.env /etc/tun/runtime-server-fra.env /etc/tun/runtime-server-nyc.env"

  delete_ufw_rules_by_comment "${server_host}" "${tag_prefix}"
  delete_nft_rules_by_comment "${server_host}" "${tag_prefix}"

  if [[ "${remove_nft_reload}" == "true" ]]; then
    remove_nft_reload_stack "${server_host}"
  fi
}

verify_clients() {
  local host
  for host in "${ams_host}" "${fra_host}" "${nyc_host}"; do
    remote_run "${host}" "hostname; sudo -n systemctl list-units --type=service --all 'tun-runtime-client@*.service' --no-pager; sudo -n ls -1 /etc/tun | grep -E 'runtime-client-(edg|vrn|msk)\\.env' || true"
  done
}

verify_server_contour() {
  local server_host="$1"
  local contour_name="$2"
  remote_run "${server_host}" "hostname; sudo -n systemctl list-units --type=service --all 'tun-runtime-server@*.service' --no-pager; sudo -n ls -1 /etc/tun | grep -E 'runtime-server-(ams|fra|nyc)\\.env|runtime-server\\.env' || true; sudo -n nft -a list ruleset | grep -E '${contour_name}|1864[3-5]|1865[3-5]' || true"
}

echo "[decommission] contour=${contour} remove_nft_reload=${remove_nft_reload} dry_run=${dry_run}"

if [[ "${contour}" == "edg" || "${contour}" == "both" ]]; then
  echo "[decommission] clients for contour=edg on ${ams_host},${fra_host},${nyc_host}"
  decommission_clients_for_contour "edg"
  echo "[decommission] server contour=edg on ${edg_host}"
  decommission_server_contour "${edg_host}" "edg"
fi

if [[ "${contour}" == "vrn" || "${contour}" == "both" ]]; then
  echo "[decommission] clients for contour=vrn on ${ams_host},${fra_host},${nyc_host}"
  decommission_clients_for_contour "vrn"
  echo "[decommission] server contour=vrn on ${vrn_host}"
  decommission_server_contour "${vrn_host}" "vrn"
fi

echo "[decommission] post-check clients"
verify_clients

if [[ "${contour}" == "edg" || "${contour}" == "both" ]]; then
  echo "[decommission] post-check edg contour"
  verify_server_contour "${edg_host}" "tun-edg"
fi
if [[ "${contour}" == "vrn" || "${contour}" == "both" ]]; then
  echo "[decommission] post-check vrn contour"
  verify_server_contour "${vrn_host}" "tun-vrn"
fi

echo "[decommission] done"

