#!/bin/sh
# Writes .env (random secrets, mode 0600) and tls/ (a test certificate for
# localhost). It refuses to replace a file that exists.
set -eu

cd "$(dirname "$0")"

if [ -e .env ] || [ -e tls/cert.pem ]; then
  echo "init: .env or tls/cert.pem exists. Remove them first if you want new ones." >&2
  exit 1
fi

for tool in openssl; do
  command -v "$tool" >/dev/null 2>&1 || { echo "init: $tool is not installed." >&2; exit 1; }
done

umask 077
{
  echo "POSTGRES_PASSWORD=$(openssl rand -hex 24)"
  echo "S3_ACCESS_KEY=$(openssl rand -hex 10)"
  echo "S3_SECRET_KEY=$(openssl rand -hex 24)"
  echo "DUSTPAN_ADMIN_TOKEN=$(openssl rand -hex 32)"
  echo "DUSTPAN_KEY_ENCRYPTION_KEY=$(openssl rand -base64 32)"
} > .env

mkdir -p tls
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 30 \
  -subj /CN=localhost -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
  -keyout tls/key.pem -out tls/cert.pem >/dev/null 2>&1
# The server runs as an unprivileged user in the container and reads these
# files through a bind mount. The certificate is a test certificate that
# expires in 30 days.
chmod 755 tls
chmod 644 tls/cert.pem tls/key.pem

echo "init: wrote .env and tls/. Keep .env private: it holds the admin token and the key-encryption key."
