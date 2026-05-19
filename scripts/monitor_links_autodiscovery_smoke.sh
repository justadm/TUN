#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/monitor_links_autodiscovery_smoke.sh [options]

Validates monitoring auto-discovery contract against runtime-helper:
  1) schema endpoint is reachable and advertises links health stream
  2) links inventory endpoint is readable
  3) links.health.stream emits JSONL events and completes with done

Options:
  --endpoint <url>                 helper endpoint (default: http://127.0.0.1:19090)
  --unix-socket <path>             optional helper unix socket path
  --token-file <path>              optional helper auth token file
  --timeout <dur>                  helperctl timeout (default: 5s)
  --stream-interval <dur>          links stream interval (default: 1s)
  --stream-duration <dur>          links stream duration (default: 8s)
  --retry true|false               reconnect stream on unexpected EOF (default: true)
  --retry-max <n>                  max reconnect attempts (default: 2)
  --retry-backoff-min <dur>        reconnect min backoff (default: 200ms)
  --retry-backoff-max <dur>        reconnect max backoff (default: 2s)
  --min-links-events <n>           required links events in stream output (default: 1)
  --out-file <path>                optional path to write stream JSONL output
  -h, --help                       show help
EOF
}

endpoint="http://127.0.0.1:19090"
unix_socket=""
token_file=""
timeout="5s"
stream_interval="1s"
stream_duration="8s"
retry="true"
retry_max="2"
retry_backoff_min="200ms"
retry_backoff_max="2s"
min_links_events="1"
out_file=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --endpoint) endpoint="${2:-}"; shift 2 ;;
    --unix-socket) unix_socket="${2:-}"; shift 2 ;;
    --token-file) token_file="${2:-}"; shift 2 ;;
    --timeout) timeout="${2:-}"; shift 2 ;;
    --stream-interval) stream_interval="${2:-}"; shift 2 ;;
    --stream-duration) stream_duration="${2:-}"; shift 2 ;;
    --retry) retry="${2:-}"; shift 2 ;;
    --retry-max) retry_max="${2:-}"; shift 2 ;;
    --retry-backoff-min) retry_backoff_min="${2:-}"; shift 2 ;;
    --retry-backoff-max) retry_backoff_max="${2:-}"; shift 2 ;;
    --min-links-events) min_links_events="${2:-}"; shift 2 ;;
    --out-file) out_file="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "${retry}" in
  true|false) ;;
  *)
    echo "invalid --retry value: ${retry} (expected true|false)" >&2
    exit 2
    ;;
esac

if ! [[ "${min_links_events}" =~ ^[0-9]+$ ]]; then
  echo "invalid --min-links-events value: ${min_links_events}" >&2
  exit 2
fi

tmp_dir="$(mktemp -d)"
stream_file="${tmp_dir}/links-health-stream.jsonl"
cleanup() {
  rm -rf "${tmp_dir}"
}
trap cleanup EXIT

declare -a ctl=(go run ./cmd/runtime-helperctl -timeout "${timeout}")
if [[ -n "${token_file}" ]]; then
  ctl+=(-token-file "${token_file}")
fi
if [[ -n "${unix_socket}" ]]; then
  ctl+=(-unix-socket "${unix_socket}")
else
  ctl+=(-endpoint "${endpoint}")
fi

echo "[monitor-smoke] check schema"
schema_json="$("${ctl[@]}" -action schema)"
if ! printf '%s' "${schema_json}" | grep -qF '"apiVersion":"v1"'; then
  echo "[monitor-smoke] schema apiVersion!=v1" >&2
  exit 1
fi
if ! printf '%s' "${schema_json}" | grep -qF '/v1/helper/links/health.stream'; then
  echo "[monitor-smoke] schema does not advertise /v1/helper/links/health.stream" >&2
  exit 1
fi

echo "[monitor-smoke] check links inventory"
links_json="$("${ctl[@]}" -action links)"
if ! printf '%s' "${links_json}" | grep -qF '"links"'; then
  echo "[monitor-smoke] links response does not contain links field" >&2
  exit 1
fi

echo "[monitor-smoke] stream links health"
"${ctl[@]}" \
  -action links.health.stream \
  -links-health-interval "${stream_interval}" \
  -links-health-duration "${stream_duration}" \
  -links-health-jsonl true \
  -links-health-retry "${retry}" \
  -links-health-retry-max "${retry_max}" \
  -links-health-retry-backoff-min "${retry_backoff_min}" \
  -links-health-retry-backoff-max "${retry_backoff_max}" > "${stream_file}"

if ! grep -qF '"stream":"links.health"' "${stream_file}"; then
  echo "[monitor-smoke] stream output does not contain links.health envelope" >&2
  exit 1
fi
links_events_count="$(grep -cF '"event":"links"' "${stream_file}" || true)"
if (( links_events_count < min_links_events )); then
  echo "[monitor-smoke] expected at least ${min_links_events} links events, got ${links_events_count}" >&2
  exit 1
fi
if ! grep -qF '"event":"done"' "${stream_file}"; then
  echo "[monitor-smoke] stream output has no done event" >&2
  exit 1
fi

if [[ -n "${out_file}" ]]; then
  cp "${stream_file}" "${out_file}"
  echo "[monitor-smoke] stream output saved: ${out_file}"
fi

echo "[monitor-smoke] passed (links_events=${links_events_count})"
