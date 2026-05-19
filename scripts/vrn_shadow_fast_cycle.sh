#!/usr/bin/env bash
set -euo pipefail

EDG_HOST="${1:-edg}"
VRN_HOST="${2:-vrn}"

echo "[1/6] sync effective routing from ${EDG_HOST} to ${VRN_HOST}"
scripts/vrn_shadow_sync_effective_routing.sh "${EDG_HOST}" "${VRN_HOST}"

echo "[2/5] redeploy shadow control API on ${VRN_HOST}"
scripts/vrn_shadow_control_api_deploy.sh "${VRN_HOST}"

echo "[3/5] redeploy shadow portal HTTP on ${VRN_HOST}"
scripts/vrn_shadow_portal_http_deploy.sh "${VRN_HOST}"

echo "[4/5] run shadow DB read smoke"
scripts/vrn_shadow_read_smoke.sh "${VRN_HOST}"

echo "[5/6] compare ${EDG_HOST} legacy vs ${VRN_HOST} shadow"
scripts/compare_edg_vrn_shadow.sh "${EDG_HOST}" "${VRN_HOST}"

echo "[6/7] compare admin HTML surfaces"
scripts/compare_portal_admin_surfaces.sh "${EDG_HOST}" "${VRN_HOST}"

echo "[7/7] compact VRN shadow summary"
scripts/vrn_shadow_status_summary.sh "${VRN_HOST}"
