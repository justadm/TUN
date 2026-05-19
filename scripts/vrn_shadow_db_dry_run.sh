#!/usr/bin/env bash
set -euo pipefail

VRN_HOST="${1:-vrn}"
REMOTE_ROOT="/opt/jstun-shadow"
REMOTE_ETC="/etc/jstun-shadow"
REMOTE_VAR="/var/lib/jstun-shadow"
REMOTE_SQL_DIR="${REMOTE_ROOT}/sql"
LOCAL_SQL=".tmp/migration-dry-run/edg-import.sql"
LOCAL_DDL=".docs/control-plane-ddl-draft-2026-04-03.sql"

echo "[1/7] install postgres on ${VRN_HOST}"
ssh "${VRN_HOST}" "sudo apt-get update && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y postgresql"

echo "[2/7] prepare shadow directories"
ssh "${VRN_HOST}" "sudo mkdir -p ${REMOTE_ROOT} ${REMOTE_ETC} ${REMOTE_VAR}/postgres ${REMOTE_VAR}/runtime ${REMOTE_SQL_DIR}"
ssh "${VRN_HOST}" "sudo chown -R postgres:postgres ${REMOTE_VAR}/postgres"

echo "[3/7] configure postgres for shadow port 15432"
ssh "${VRN_HOST}" "sudo -u postgres /usr/lib/postgresql/16/bin/initdb -D ${REMOTE_VAR}/postgres >/dev/null 2>&1 || true"
ssh "${VRN_HOST}" "sudo sed -i \"s/^#\\?port = .*/port = 15432/\" ${REMOTE_VAR}/postgres/postgresql.conf"
ssh "${VRN_HOST}" "printf \"listen_addresses = '127.0.0.1'\\n\" | sudo tee -a ${REMOTE_VAR}/postgres/postgresql.conf >/dev/null"
ssh "${VRN_HOST}" "printf \"host all all 127.0.0.1/32 scram-sha-256\\n\" | sudo tee -a ${REMOTE_VAR}/postgres/pg_hba.conf >/dev/null"

echo "[4/7] install systemd unit"
scp control-plane/deploy/systemd/jstun-shadow-postgres.service "${VRN_HOST}:/tmp/jstun-shadow-postgres.service"
ssh "${VRN_HOST}" "sudo mv /tmp/jstun-shadow-postgres.service /etc/systemd/system/jstun-shadow-postgres.service && sudo systemctl daemon-reload && sudo systemctl enable --now jstun-shadow-postgres.service"

echo "[5/7] provision shadow db and user"
ssh "${VRN_HOST}" "sudo -u postgres psql -p 15432 -c \"do \\\$\\\$ begin if not exists (select 1 from pg_roles where rolname = 'jstun_shadow') then create role jstun_shadow login password 'change-me'; end if; end \\\$\\\$;\""
ssh "${VRN_HOST}" "sudo -u postgres psql -p 15432 -c \"drop database if exists jstun_shadow;\""
ssh "${VRN_HOST}" "sudo -u postgres psql -p 15432 -c \"create database jstun_shadow owner jstun_shadow;\""

echo "[6/7] upload SQL"
scp "${LOCAL_DDL}" "${VRN_HOST}:/tmp/control-plane-ddl.sql"
scp "${LOCAL_SQL}" "${VRN_HOST}:/tmp/edg-import.sql"
ssh "${VRN_HOST}" "sudo mv /tmp/control-plane-ddl.sql ${REMOTE_SQL_DIR}/control-plane-ddl.sql && sudo mv /tmp/edg-import.sql ${REMOTE_SQL_DIR}/edg-import.sql"

echo "[7/7] apply SQL and verify counts"
ssh "${VRN_HOST}" "PGPASSWORD=change-me psql -h 127.0.0.1 -p 15432 -U jstun_shadow -d jstun_shadow -f ${REMOTE_SQL_DIR}/edg-import.sql >/tmp/jstun-shadow-import.log"
ssh "${VRN_HOST}" "PGPASSWORD=change-me psql -h 127.0.0.1 -p 15432 -U jstun_shadow -d jstun_shadow -At -F ' ' -c \"select 'edges', count(*) from edges union all select 'uplinks', count(*) from uplinks union all select 'peers', count(*) from peers union all select 'peer_routing_policy', count(*) from peer_routing_policy union all select 'peer_runtime_state', count(*) from peer_runtime_state union all select 'billing_records', count(*) from billing_records union all select 'events', count(*) from events order by 1;\""
