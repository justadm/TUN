#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/install_frr_bgp_stack.sh --host <ssh-host> [--apply]

Options:
  --host <ssh-host>   SSH host alias/name (required)
  --apply             Execute install/configure remotely
  --dry-run           Print remote plan only (default)
  -h, --help
EOF
}

HOST=""
DRY_RUN="true"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host) HOST="${2:-}"; shift 2 ;;
    --apply) DRY_RUN="false"; shift ;;
    --dry-run) DRY_RUN="true"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ -n "${HOST}" ]] || { echo "--host is required" >&2; exit 2; }

remote_cmd='
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
sudo apt-get update -y
sudo apt-get install -y frr frr-pythontools
sudo grep -q "^zebra=" /etc/frr/daemons && sudo sed -i "s/^zebra=.*/zebra=yes/" /etc/frr/daemons || echo zebra=yes | sudo tee -a /etc/frr/daemons >/dev/null
sudo grep -q "^bgpd=" /etc/frr/daemons && sudo sed -i "s/^bgpd=.*/bgpd=yes/" /etc/frr/daemons || echo bgpd=yes | sudo tee -a /etc/frr/daemons >/dev/null
sudo systemctl enable --now frr
sudo systemctl restart frr
sudo systemctl is-active --quiet frr
echo "[ok] frr active"
'

if [[ "${DRY_RUN}" == "true" ]]; then
  echo "[dry-run] host=${HOST}"
  echo "${remote_cmd}"
  exit 0
fi

ssh "${HOST}" "${remote_cmd}"
