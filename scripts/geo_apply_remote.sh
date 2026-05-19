#!/usr/bin/env bash
set -euo pipefail

DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    *)
      break
      ;;
  esac
done

if [[ $# -ne 3 ]]; then
  echo "usage: $0 [--dry-run] <snapshot_dir> <target_host> <target_kind(edg|vrn)>" >&2
  exit 1
fi

SNAPSHOT_DIR=$1
TARGET_HOST=$2
TARGET_KIND=$3
MANIFEST="$SNAPSHOT_DIR/manifest.json"

if [[ ! -f "$MANIFEST" ]]; then
  echo "manifest missing: $MANIFEST" >&2
  exit 1
fi

VERSION=$(python3 - <<'PY' "$MANIFEST"
import json, sys
from pathlib import Path
manifest = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
print(manifest["version"])
PY
)

REMOTE_STAGE="/tmp/geo-sync-$VERSION"

case "$TARGET_KIND" in
  edg)
    GEO_DIR="/etc/geo-sync"
    STATE_DIR="/var/lib/geo-sync"
    DNSMASQ_DIR="/etc/dnsmasq.d"
    ;;
  vrn)
    GEO_DIR="/etc/vrn-geo"
    STATE_DIR="/var/lib/geo-sync"
    DNSMASQ_DIR="/etc/dnsmasq.d"
    ;;
  *)
    echo "unknown target kind: $TARGET_KIND" >&2
    exit 1
    ;;
esac

if [[ "$DRY_RUN" == "1" ]]; then
  echo "dry-run: would deploy $VERSION to $TARGET_HOST ($TARGET_KIND)"
  exit 0
fi

ssh "$TARGET_HOST" "rm -rf '$REMOTE_STAGE' && mkdir -p '$REMOTE_STAGE'"
scp -r "$SNAPSHOT_DIR"/. "$TARGET_HOST:$REMOTE_STAGE/"

ssh "$TARGET_HOST" bash -s -- "$TARGET_KIND" "$REMOTE_STAGE" "$GEO_DIR" "$DNSMASQ_DIR" "$STATE_DIR" <<'EOF'
set -euo pipefail
TARGET_KIND=$1
REMOTE_STAGE=$2
GEO_DIR=$3
DNSMASQ_DIR=$4
STATE_DIR=$5
BACKUP_DIR="$STATE_DIR/rollback-current"
ROLLBACK_STATE="$STATE_DIR/last-rollback.json"
MANAGED_DNSMASQ_FILES="20-openai.conf 30-ru-streaming.conf 40-wg-portal-telegram.conf 10-geo-base.conf"
GEO_FILES="ru4.txt ru6.txt msk_custom4.txt msk_custom6.txt openai_v4.txt openai_v6.txt manifest.json"

backup_current() {
  sudo rm -rf "$BACKUP_DIR"
  sudo mkdir -p "$BACKUP_DIR/geo" "$BACKUP_DIR/dnsmasq"
  for name in $GEO_FILES; do
    if sudo test -f "$GEO_DIR/$name"; then
      sudo cp "$GEO_DIR/$name" "$BACKUP_DIR/geo/$name"
    fi
  done
  for name in $MANAGED_DNSMASQ_FILES; do
    if sudo test -f "$DNSMASQ_DIR/$name"; then
      sudo cp "$DNSMASQ_DIR/$name" "$BACKUP_DIR/dnsmasq/$name"
    fi
  done
}

restore_backup() {
  sudo mkdir -p "$GEO_DIR" "$DNSMASQ_DIR"
  for name in $GEO_FILES; do
    if sudo test -f "$BACKUP_DIR/geo/$name"; then
      sudo cp "$BACKUP_DIR/geo/$name" "$GEO_DIR/$name"
    fi
  done
  for name in $MANAGED_DNSMASQ_FILES; do
    if sudo test -f "$BACKUP_DIR/dnsmasq/$name"; then
      sudo cp "$BACKUP_DIR/dnsmasq/$name" "$DNSMASQ_DIR/$name"
    else
      sudo rm -f "$DNSMASQ_DIR/$name"
    fi
  done
}

install_geo_files() {
  sudo mkdir -p "$GEO_DIR" "$DNSMASQ_DIR" "$STATE_DIR"
  for name in ru4 ru6 msk_custom4 msk_custom6 openai_v4 openai_v6; do
    sudo install -m 0644 "$REMOTE_STAGE/$name.txt" "$GEO_DIR/$name.txt"
  done
  sudo install -m 0644 "$REMOTE_STAGE/manifest.json" "$GEO_DIR/manifest.json"

  if [[ -d "$REMOTE_STAGE/dnsmasq" ]]; then
    for f in "$REMOTE_STAGE"/dnsmasq/*.conf; do
      [[ -f "$f" ]] || continue
      if [[ "$TARGET_KIND" == "vrn" ]]; then
        sed 's/#inet#msk_geo#/#inet#vrn#/g' "$f" | sudo tee "$DNSMASQ_DIR/$(basename "$f")" >/dev/null
        sudo chmod 0644 "$DNSMASQ_DIR/$(basename "$f")"
      else
        sudo install -m 0644 "$f" "$DNSMASQ_DIR/$(basename "$f")"
      fi
    done
  fi
}

ensure_edg_dnsmasq_base() {
  sudo tee "$DNSMASQ_DIR/10-geo-base.conf" >/dev/null <<'CFG'
no-resolv
server=1.1.1.1
server=8.8.8.8
interface=wg0
listen-address=10.8.0.1
bind-interfaces
cache-size=1000
CFG
  sudo chmod 0644 "$DNSMASQ_DIR/10-geo-base.conf"
}

apply_edg_sets() {
  local tmp
  tmp=$(mktemp)
  {
    echo "flush set inet msk_geo ru4"
    echo "flush set inet msk_geo ru6"
    echo "flush set inet msk_geo msk_custom4"
    echo "flush set inet msk_geo msk_custom6"
    echo "flush set inet msk_geo openai_v4"
    echo "flush set inet msk_geo openai_v6"
    awk '{print "add element inet msk_geo ru4 { " $1 " }"}' "$GEO_DIR/ru4.txt"
    awk '{print "add element inet msk_geo ru6 { " $1 " }"}' "$GEO_DIR/ru6.txt"
    awk '{print "add element inet msk_geo msk_custom4 { " $1 " }"}' "$GEO_DIR/msk_custom4.txt"
    awk '{print "add element inet msk_geo msk_custom6 { " $1 " }"}' "$GEO_DIR/msk_custom6.txt"
    awk '{print "add element inet msk_geo openai_v4 { " $1 " }"}' "$GEO_DIR/openai_v4.txt"
    awk '{print "add element inet msk_geo openai_v6 { " $1 " }"}' "$GEO_DIR/openai_v6.txt"
  } > "$tmp"
  sudo nft -f "$tmp"
  rm -f "$tmp"
}

validate_edg() {
  sudo dnsmasq --test >/dev/null
  sudo systemctl is-active --quiet dnsmasq
  sudo nft list set inet msk_geo ru4 >/dev/null
  sudo nft list set inet msk_geo openai_v4 >/dev/null
  sudo test -x /usr/local/bin/check-split.sh
  sudo /usr/local/bin/check-split.sh >/dev/null
}

validate_vrn() {
  sudo dnsmasq --test >/dev/null
  sudo systemctl is-active --quiet dnsmasq
  sudo nft list table inet vrn >/dev/null
  sudo nft list set inet vrn ru4 >/dev/null
  sudo nft list set inet vrn openai_v4 >/dev/null
  dig @127.0.0.1 openai.com +short | grep -q .
}

apply_target() {
  case "$TARGET_KIND" in
    edg)
      ensure_edg_dnsmasq_base
      apply_edg_sets
      sudo systemctl reload dnsmasq || sudo systemctl restart dnsmasq
      validate_edg
      ;;
    vrn)
      sudo /usr/local/sbin/vrn-routing-apply
      sudo systemctl reload dnsmasq || sudo systemctl restart dnsmasq
      validate_vrn
      ;;
    *)
      echo "unsupported target kind: $TARGET_KIND" >&2
      exit 1
      ;;
  esac
}

write_state() {
  local version applied_at
  version=$(python3 - <<'PY' "$GEO_DIR/manifest.json"
import json, sys
from pathlib import Path
print(json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))["version"])
PY
)
  applied_at=$(date -u +%FT%TZ)
  sudo tee "$STATE_DIR/last-applied.json" >/dev/null <<JSON
{
  "target_kind": "$TARGET_KIND",
  "version": "$version",
  "applied_at": "$applied_at"
}
JSON
}

write_rollback_state() {
  local failed_version rolled_back_to rolled_back_at
  failed_version=$(python3 - <<'PY' "$REMOTE_STAGE/manifest.json"
import json, sys
from pathlib import Path
print(json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))["version"])
PY
)
  if sudo test -f "$BACKUP_DIR/geo/manifest.json"; then
    rolled_back_to=$(python3 - <<'PY' "$BACKUP_DIR/geo/manifest.json"
import json, sys
from pathlib import Path
print(json.loads(Path(sys.argv[1]).read_text(encoding="utf-8")).get("version", "unknown"))
PY
)
  else
    rolled_back_to="unknown"
  fi
  rolled_back_at=$(date -u +%FT%TZ)
  sudo tee "$ROLLBACK_STATE" >/dev/null <<JSON
{
  "target_kind": "$TARGET_KIND",
  "failed_version": "$failed_version",
  "rolled_back_to": "$rolled_back_to",
  "rolled_back_at": "$rolled_back_at"
}
JSON
}

backup_current
install_geo_files

if apply_target; then
  write_state
  sudo rm -rf "$BACKUP_DIR"
  rm -rf "$REMOTE_STAGE"
  exit 0
fi

restore_backup
apply_target
write_rollback_state
rm -rf "$REMOTE_STAGE"
echo "rollback completed on $TARGET_KIND" >&2
exit 1
EOF
