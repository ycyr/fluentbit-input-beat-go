#!/usr/bin/env bash
# Generates a throwaway CA + server + client cert for the mTLS example.
# Output lands in ./certs (gitignored). Re-run anytime; it overwrites.
#
# The server cert's SAN must match the host Filebeat dials — the compose
# service name "fluent-bit" — or Filebeat rejects the handshake on hostname
# verification.
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p certs && cd certs

# CA
openssl req -x509 -newkey rsa:2048 -nodes -keyout ca.key -out ca.crt \
  -subj "/CN=beats-demo-ca" -days 365

# Server cert, signed by the CA, SAN = the dialed hostname
openssl req -newkey rsa:2048 -nodes -keyout server.key -out server.csr \
  -subj "/CN=fluent-bit"
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out server.crt -days 365 \
  -extfile <(printf "subjectAltName=DNS:fluent-bit,DNS:localhost")

# Client cert, signed by the same CA (this is what mTLS verifies)
openssl req -newkey rsa:2048 -nodes -keyout client.key -out client.csr \
  -subj "/CN=beats-client"
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out client.crt -days 365

rm -f ./*.csr
echo "wrote certs/ -> ca.crt server.crt server.key client.crt client.key"
