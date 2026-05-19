#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "${script_dir}/ops_sre_linux_baseline_gate.sh" --allow-ubuntu 22.04 "$@"
