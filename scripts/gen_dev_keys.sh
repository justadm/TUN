#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${ROOT_DIR}/.dev"

mkdir -p "${OUT_DIR}"

echo "Generating X25519 server static keypair..."
openssl genpkey -algorithm X25519 -out "${OUT_DIR}/server_static_priv.pem"
openssl pkey -in "${OUT_DIR}/server_static_priv.pem" -pubout -out "${OUT_DIR}/server_static_pub.pem"

SERVER_PRIV_B64="$(openssl pkey -in "${OUT_DIR}/server_static_priv.pem" -outform DER | tail -c 32 | base64)"
SERVER_PUB_B64="$(openssl pkey -in "${OUT_DIR}/server_static_pub.pem" -outform DER | tail -c 32 | base64)"

echo "${SERVER_PRIV_B64}" > "${OUT_DIR}/server_static_priv.b64"
echo "${SERVER_PUB_B64}" > "${OUT_DIR}/server_static_pub.b64"

echo "Generating self-signed TLS certificate..."
openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -subj "/CN=localhost" \
  -keyout "${OUT_DIR}/tls_key.pem" \
  -out "${OUT_DIR}/tls_cert.pem"

echo "Done. Files in ${OUT_DIR}:"
ls -1 "${OUT_DIR}"
