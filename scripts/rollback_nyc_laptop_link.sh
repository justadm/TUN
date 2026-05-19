#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/rollback_nyc_laptop_link.sh [options]

Rolls back NYC laptop test link:
  - stops/disables tun-runtime-server@laptop
  - removes /etc/tun/runtime-server-laptop.env
  - removes optional firewall rules tagged "tun-laptop-nyc"

Options:
  --host <ssh-host>    default: nyc
  --via <ssh-host>     optional SSH jump host
  --dry-run            print commands only
  -h, --help           show help
EOF
}

host="nyc"
via=""
dry_run="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) host="${2:-}"; shift 2 ;;
    --via) via="${2:-}"; shift 2 ;;
    --dry-run) dry_run="true"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

ssh_cmd=(ssh)
if [[ -n "${via}" ]]; then
  ssh_cmd+=(-J "${via}")
fi
ssh_cmd+=("${host}")

remote_cmd='
set -euo pipefail
sudo -n systemctl disable --now tun-runtime-server@laptop.service || true
sudo -n rm -f /etc/tun/runtime-server-laptop.env
nums=$(sudo -n ufw status numbered | grep "tun-laptop-nyc" | sed -E "s/^\[ *([0-9]+)\].*/\1/" | sort -rn || true)
for n in $nums; do
  sudo -n ufw --force delete "$n" >/dev/null || true
done
handles=$(sudo -n nft -a list chain inet filter input 2>/dev/null | grep "comment \"tun-laptop-nyc\"" | sed -E "s/.*# handle ([0-9]+).*/\1/" || true)
for h in $handles; do
  sudo -n nft delete rule inet filter input handle "$h" || true
done
sudo -n systemctl daemon-reload
echo "rollback complete on $(hostname)"
'

if [[ "${dry_run}" == "true" ]]; then
  echo "+ ${ssh_cmd[*]} <remote rollback commands>"
  exit 0
fi

"${ssh_cmd[@]}" "${remote_cmd}"

