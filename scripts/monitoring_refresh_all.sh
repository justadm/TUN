#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

./scripts/monitoring_sync_notifiers.sh
./scripts/monitoring_auto_sources.sh "$@"
docker compose -f monitoring/docker-compose.yml up -d --build monitoring-ingestor monitoring-api

echo "monitoring refresh completed"
