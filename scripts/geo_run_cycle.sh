#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=${ROOT_DIR:-/opt/geo-sync}
SOURCE_HOST=${SOURCE_HOST:-edg}
DEPLOY_TARGETS=${DEPLOY_TARGETS:-}
SHADOW_TARGETS=${SHADOW_TARGETS:-}
MANUAL_ROOT=${MANUAL_ROOT:-$ROOT_DIR/manual}
DRY_RUN=${DRY_RUN:-0}
KEEP_VERSIONS=${KEEP_VERSIONS:-10}
TG_ENV=${TG_ENV:-/etc/b24-remote-testing/telegram.env}
NOTIFY_CHANGE=${NOTIFY_CHANGE:-1}
NOTIFY_NOCHANGE=${NOTIFY_NOCHANGE:-0}
NOTIFY_FAILURE=${NOTIFY_FAILURE:-1}
STATE_DIR="$ROOT_DIR/state"
LOCK_DIR="$STATE_DIR/geo-sync.lock.d"

BUILDER="$ROOT_DIR/bin/geo_build_snapshot.py"
APPLIER="$ROOT_DIR/bin/geo_apply_remote.sh"
SHADOW_VERIFY="$ROOT_DIR/bin/geo_shadow_verify.py"

mkdir -p "$STATE_DIR"

if ! mkdir "$LOCK_DIR" 2>/dev/null; then
  echo "another geo sync cycle is already running" >&2
  exit 1
fi
trap 'rmdir "$LOCK_DIR" 2>/dev/null || true' EXIT

send_tg() {
  local msg="$1"
  [[ -f "$TG_ENV" ]] || return 0
  # shellcheck disable=SC1090
  source "$TG_ENV"
  [[ -n "${TELEGRAM_BOT_TOKEN:-}" && -n "${TELEGRAM_CHAT_ID:-}" ]] || return 0
  local resp http
  resp=$(mktemp)
  http=$(curl -s -o "$resp" -w "%{http_code}" "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/sendMessage" \
    -d "chat_id=${TELEGRAM_CHAT_ID}" \
    --data-urlencode "text=${msg}" || true)
  echo "telegram notify http=${http:-curl-failed} msg=$(head -c 160 <<<"$msg")"
  if [[ "${http:-}" != "200" ]]; then
    echo "telegram notify body=$(head -c 300 "$resp")"
  fi
  rm -f "$resp"
}

if [[ ! -x "$BUILDER" ]]; then
  echo "builder not found: $BUILDER" >&2
  exit 1
fi
if [[ -n "$SHADOW_TARGETS" && ! -x "$SHADOW_VERIFY" ]]; then
  echo "shadow verifier not found: $SHADOW_VERIFY" >&2
  exit 1
fi

latest_manifest="$ROOT_DIR/snapshots/latest/manifest.json"
prev_tree=""
prev_ru4_sha=""
prev_ru6_sha=""
if [[ -f "$latest_manifest" ]]; then
  readarray -t manifest_state < <(python3 - <<'PY' "$latest_manifest"
import json, sys
from pathlib import Path
data = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
print(data.get("tree_sha256", ""))
print(data.get("sets", {}).get("ru4", {}).get("sha256", ""))
print(data.get("sets", {}).get("ru6", {}).get("sha256", ""))
PY
)
  prev_tree=${manifest_state[0]:-}
  prev_ru4_sha=${manifest_state[1]:-}
  prev_ru6_sha=${manifest_state[2]:-}
fi

snapshot_dir=$("$BUILDER" --source-host "$SOURCE_HOST" --output-root "$ROOT_DIR" --manual-root "$MANUAL_ROOT")
snapshot_manifest="$snapshot_dir/manifest.json"

read_manifest_field() {
  python3 - <<'PY' "$snapshot_manifest" "$1"
import json, sys
from pathlib import Path
data = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
value = data
for part in sys.argv[2].split("."):
    value = value.get(part, "")
print(value)
PY
}

version=$(read_manifest_field version)
tree_sha=$(read_manifest_field tree_sha256)
generated_at=$(read_manifest_field generated_at)
ru4_count=$(read_manifest_field sets.ru4.count)
ru6_count=$(read_manifest_field sets.ru6.count)
ru4_sha=$(read_manifest_field sets.ru4.sha256)
ru6_sha=$(read_manifest_field sets.ru6.sha256)
ru4_changed=yes
ru6_changed=yes
[[ -n "$prev_ru4_sha" && "$prev_ru4_sha" == "$ru4_sha" ]] && ru4_changed=no
[[ -n "$prev_ru6_sha" && "$prev_ru6_sha" == "$ru6_sha" ]] && ru6_changed=no

echo "snapshot=$snapshot_dir"
echo "version=$version tree_sha256=$tree_sha"

run_shadow_verify() {
  local snapshot="$1"
  local status="ok"
  local tmp_results
  tmp_results=$(mktemp)
  printf '[]' > "$tmp_results"
  IFS=',' read -r -a shadow_hosts <<< "$SHADOW_TARGETS"
  for host in "${shadow_hosts[@]}"; do
    [[ -n "$host" ]] || continue
    out="$STATE_DIR/shadow-${host}.json"
    if "$SHADOW_VERIFY" --snapshot-dir "$snapshot" --host "$host" --output "$out" >/dev/null; then
      item_status="ok"
    else
      item_status="mismatch"
      status="mismatch"
    fi
    python3 - <<'PY' "$tmp_results" "$host" "$item_status"
import json, sys
from pathlib import Path
path = Path(sys.argv[1])
payload = json.loads(path.read_text(encoding="utf-8"))
payload.append({"host": sys.argv[2], "status": sys.argv[3]})
path.write_text(json.dumps(payload, ensure_ascii=True, indent=2) + "\n", encoding="utf-8")
PY
  done
  cat "$tmp_results"
  rm -f "$tmp_results"
  [[ "$status" == "ok" ]]
}

if [[ "$DRY_RUN" == "1" ]]; then
  echo "dry-run: deploy skipped"
  python3 - <<'PY' "$STATE_DIR/last-run.json" "$version" "$tree_sha" "$generated_at"
import json, sys
from pathlib import Path
payload = {
    "status": "dry-run",
    "version": sys.argv[2],
    "tree_sha256": sys.argv[3],
    "generated_at": sys.argv[4],
}
Path(sys.argv[1]).write_text(json.dumps(payload, ensure_ascii=True, indent=2) + "\n", encoding="utf-8")
PY
  exit 0
fi

if [[ -n "$prev_tree" && "$prev_tree" == "$tree_sha" ]]; then
  echo "no content changes detected; deploy skipped"
  rm -rf "$snapshot_dir"
  shadow_status="not-run"
  shadow_results="[]"
  if [[ -n "$SHADOW_TARGETS" ]]; then
    shadow_results=$(run_shadow_verify "$ROOT_DIR/snapshots/latest" || true)
    if grep -q '"status": "mismatch"' <<<"$shadow_results"; then
      shadow_status="mismatch"
    else
      shadow_status="ok"
    fi
  fi
  python3 - <<'PY' "$STATE_DIR/last-run.json" "$version" "$tree_sha" "$generated_at" "$shadow_status" "$shadow_results"
import json, sys
from pathlib import Path
payload = {
    "status": "no-change",
    "version": sys.argv[2],
    "tree_sha256": sys.argv[3],
    "generated_at": sys.argv[4],
    "shadow_status": sys.argv[5],
    "shadow_results": json.loads(sys.argv[6]),
}
Path(sys.argv[1]).write_text(json.dumps(payload, ensure_ascii=True, indent=2) + "\n", encoding="utf-8")
PY
  if [[ "$NOTIFY_NOCHANGE" == "1" ]]; then
    msg="[MSK geo-sync] RU GeoIP обновление: v4=${ru4_count} (chg:${ru4_changed}), v6=${ru6_count} (chg:${ru6_changed})"
    if [[ "$shadow_status" == "ok" ]]; then
      msg="${msg} | shadow:ok"
    elif [[ "$shadow_status" == "mismatch" ]]; then
      msg="${msg} | shadow:mismatch"
    fi
    send_tg "$msg"
    if [[ "$shadow_status" == "mismatch" && "$NOTIFY_FAILURE" == "1" ]]; then
      send_tg "[MSK geo-sync] RU GeoIP shadow verify FAILED on ${SHADOW_TARGETS}: live EDG differs from latest snapshot"
    fi
  fi
  exit 0
fi

if [[ -z "$DEPLOY_TARGETS" ]]; then
  echo "deploy targets are empty; build only"
  python3 - <<'PY' "$STATE_DIR/last-run.json" "$version" "$tree_sha" "$generated_at"
import json, sys
from pathlib import Path
payload = {
    "status": "build-only",
    "version": sys.argv[2],
    "tree_sha256": sys.argv[3],
    "generated_at": sys.argv[4],
    "changed": True,
}
Path(sys.argv[1]).write_text(json.dumps(payload, ensure_ascii=True, indent=2) + "\n", encoding="utf-8")
PY
  if [[ "$NOTIFY_CHANGE" == "1" ]]; then
    send_tg "[MSK geo-sync] RU GeoIP обновление: v4=${ru4_count} (chg:${ru4_changed}), v6=${ru6_count} (chg:${ru6_changed}) | build-only on MSK"
  fi
  exit 0
fi

IFS=',' read -r -a targets <<< "$DEPLOY_TARGETS"
tmp_results=$(mktemp)
printf '[]' > "$tmp_results"
for item in "${targets[@]}"; do
  host=${item%%:*}
  kind=${item##*:}
  echo "deploying snapshot to $host ($kind)"
  if "$APPLIER" "$snapshot_dir" "$host" "$kind"; then
    status="ok"
  else
    status="failed"
  fi
  python3 - <<'PY' "$tmp_results" "$host" "$kind" "$status"
import json, sys
from pathlib import Path
path = Path(sys.argv[1])
payload = json.loads(path.read_text(encoding="utf-8"))
payload.append({
    "host": sys.argv[2],
    "kind": sys.argv[3],
    "status": sys.argv[4],
})
path.write_text(json.dumps(payload, ensure_ascii=True, indent=2) + "\n", encoding="utf-8")
PY
  if [[ "$status" != "ok" ]]; then
    python3 - <<'PY' "$STATE_DIR/last-run.json" "$version" "$tree_sha" "$generated_at" "$tmp_results"
import json, sys
from pathlib import Path
payload = {
    "status": "failed",
    "version": sys.argv[2],
    "tree_sha256": sys.argv[3],
    "generated_at": sys.argv[4],
    "targets": json.loads(Path(sys.argv[5]).read_text(encoding="utf-8")),
}
Path(sys.argv[1]).write_text(json.dumps(payload, ensure_ascii=True, indent=2) + "\n", encoding="utf-8")
PY
    if [[ "$NOTIFY_FAILURE" == "1" ]]; then
      send_tg "[MSK geo-sync] RU GeoIP apply FAILED: version=${version}, target=${host}/${kind}. Rollback attempted. v4=${ru4_count} (chg:${ru4_changed}), v6=${ru6_count} (chg:${ru6_changed})"
    fi
    exit 1
  fi
done

python3 - <<'PY' "$STATE_DIR/last-run.json" "$version" "$tree_sha" "$generated_at" "$tmp_results"
import json, sys
from pathlib import Path
payload = {
    "status": "deployed",
    "version": sys.argv[2],
    "tree_sha256": sys.argv[3],
    "generated_at": sys.argv[4],
    "targets": json.loads(Path(sys.argv[5]).read_text(encoding="utf-8")),
}
Path(sys.argv[1]).write_text(json.dumps(payload, ensure_ascii=True, indent=2) + "\n", encoding="utf-8")
PY

if [[ "$NOTIFY_CHANGE" == "1" ]]; then
  send_tg "[MSK geo-sync] RU GeoIP обновление: v4=${ru4_count} (chg:${ru4_changed}), v6=${ru6_count} (chg:${ru6_changed}) | applied to ${DEPLOY_TARGETS}"
fi

mapfile -t snapshot_dirs < <(find "$ROOT_DIR/snapshots" -mindepth 1 -maxdepth 1 -type d ! -name latest | sort)
if (( ${#snapshot_dirs[@]} > KEEP_VERSIONS )); then
  remove_count=$(( ${#snapshot_dirs[@]} - KEEP_VERSIONS ))
  for ((i=0; i<remove_count; i++)); do
    rm -rf "${snapshot_dirs[$i]}"
  done
fi

rm -f "$tmp_results"
