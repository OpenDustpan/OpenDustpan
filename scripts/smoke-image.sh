#!/usr/bin/env bash
# Local smoke test for the container image (issue #97, ADR 27).
#
# It builds the image, generates a self-signed certificate, starts the
# container over TLS, creates a namespace, requests the API and the console
# page, waits until the container reports healthy, checks that the process
# user is not root, then removes the container. It is not part of `make
# check`: run it with `make image-smoke`.
#
# TLS goes through the environment (DUSTPAN_TLS_CERT, DUSTPAN_TLS_KEY), not
# through serve flags, so the HEALTHCHECK probe of the image sees the same
# TLS settings as the server (issue #272).
#
# Environment:
#   IMAGE           image tag to build (default opendustpan:smoke)
#   CONSOLE_URL     console tarball: file:<repo-relative path> builds a local
#                   test tarball first (default file:console-smoke.tgz), or an
#                   https: URL of a console release tarball
#   CONSOLE_SHA256  tarball checksum (default: computed for a local tarball,
#                   required for an https: URL)
#   EXTRA_CA_FILE   optional PEM bundle file with one more CA to trust during
#                   the build, for hosts behind a TLS-intercepting proxy
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE="${IMAGE:-opendustpan:smoke}"
CONSOLE_URL="${CONSOLE_URL:-file:console-smoke.tgz}"
CONSOLE_SHA256="${CONSOLE_SHA256:-}"
CONTAINER="opendustpan-smoke-$$"
STAGED_TARBALL=""

for tool in docker openssl curl tar awk; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "smoke-image: missing required tool: $tool" >&2
    exit 1
  fi
done

WORK="$(mktemp -d)"
CERT_DIR="$WORK/certs"
DATA_DIR="$WORK/data"
trap 'docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; rm -rf "$WORK" 2>/dev/null || true; if [ -n "$STAGED_TARBALL" ]; then rm -f "$STAGED_TARBALL"; fi' EXIT

fail() {
  echo "smoke-image: FAIL: $1" >&2
  echo "--- container logs ---" >&2
  docker logs "$CONTAINER" 2>&1 || true
  exit 1
}

# Build a local test console tarball when CONSOLE_URL is a file: path.
case "$CONSOLE_URL" in
  file:*)
    STAGED_TARBALL="$ROOT/${CONSOLE_URL#file:}"
    mkdir -p "$WORK/console-src"
    printf '%s\n' '<!doctype html><title>smoke</title><p>opendustpan smoke console</p>' >"$WORK/console-src/index.html"
    tar -czf "$STAGED_TARBALL" -C "$WORK/console-src" .
    if [ -z "$CONSOLE_SHA256" ]; then
      CONSOLE_SHA256="$(sha256sum "$STAGED_TARBALL" | awk '{print $1}')"
    fi
    ;;
  https:*)
    if [ -z "$CONSOLE_SHA256" ]; then
      echo "smoke-image: CONSOLE_SHA256 is required with an https: CONSOLE_URL" >&2
      exit 1
    fi
    ;;
  *)
    echo "smoke-image: CONSOLE_URL must start with file: or https:" >&2
    exit 1
    ;;
esac

echo "smoke-image: building $IMAGE"
BUILD_SECRETS=()
if [ -n "${EXTRA_CA_FILE:-}" ]; then
  BUILD_SECRETS+=(--secret "id=extra_ca,src=$EXTRA_CA_FILE")
fi
docker build -t "$IMAGE" \
  --build-arg VERSION=smoke \
  --build-arg CONSOLE_URL="$CONSOLE_URL" \
  --build-arg CONSOLE_SHA256="$CONSOLE_SHA256" \
  "${BUILD_SECRETS[@]}" \
  "$ROOT"

echo "smoke-image: generating a self-signed certificate"
mkdir -p "$CERT_DIR" "$DATA_DIR"
chmod 777 "$DATA_DIR"
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "$CERT_DIR/key.pem" -out "$CERT_DIR/cert.pem" \
  -days 2 -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" >/dev/null 2>&1
# The container runs as a non-root user, so it must read the certificate.
# These are throwaway test files, removed on exit.
chmod 644 "$CERT_DIR/cert.pem" "$CERT_DIR/key.pem"

export DUSTPAN_ADMIN_TOKEN
DUSTPAN_ADMIN_TOKEN="$(openssl rand -hex 32)"

echo "smoke-image: starting $CONTAINER"
docker run -d --name "$CONTAINER" -p 127.0.0.1::8080 \
  -v "$DATA_DIR:/data" -v "$CERT_DIR:/certs:ro" \
  -e DUSTPAN_ADMIN_TOKEN \
  -e DUSTPAN_TLS_CERT=/certs/cert.pem -e DUSTPAN_TLS_KEY=/certs/key.pem \
  "$IMAGE" serve \
  --database sqlite:/data/dustpan.db --listen :8080 \
  --console-dir /console >/dev/null

HOSTPORT="$(docker port "$CONTAINER" 8080/tcp | head -n 1)"
HOSTPORT="${HOSTPORT##*:}"
BASE="https://127.0.0.1:$HOSTPORT"

# Issue #87 adds a GET /api/v2/ping check here. Until then the smoke test
# checks GET /api/capabilities, a public Pulumi route that exists today.
echo "smoke-image: waiting for $BASE"
for _ in $(seq 1 60); do
  if curl -sf --cacert "$CERT_DIR/cert.pem" "$BASE/api/capabilities" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

echo "smoke-image: creating namespace acme through the API"
docker exec -e DUSTPAN_ADMIN_TOKEN "$CONTAINER" \
  /opendustpan namespace create --server https://localhost:8080 --ca-file /certs/cert.pem acme | grep -q "created namespace acme" \
  || fail "namespace create did not report the namespace"

echo "smoke-image: checking GET /api/capabilities"
CAP_OUT="$WORK/capabilities.json"
if ! curl -sSf --cacert "$CERT_DIR/cert.pem" "$BASE/api/capabilities" -o "$CAP_OUT"; then
  fail "GET /api/capabilities did not answer 200"
fi
if ! grep -q "batch-encrypt" "$CAP_OUT"; then
  fail "GET /api/capabilities answered without batch-encrypt"
fi

echo "smoke-image: checking GET / (console page)"
if ! curl -sSf --cacert "$CERT_DIR/cert.pem" "$BASE/" | grep -q "opendustpan smoke console"; then
  fail "GET / did not serve the console page"
fi

echo "smoke-image: waiting for a healthy status"
for _ in $(seq 1 120); do
  HEALTH="$(docker inspect -f '{{.State.Health.Status}}' "$CONTAINER")"
  if [ "$HEALTH" = "healthy" ]; then
    break
  fi
  if [ "$HEALTH" = "unhealthy" ]; then
    fail "container reported unhealthy"
  fi
  sleep 1
done
if [ "$(docker inspect -f '{{.State.Health.Status}}' "$CONTAINER")" != "healthy" ]; then
  fail "container did not report healthy in time"
fi

echo "smoke-image: checking the version stamp"
if [ "$(docker exec "$CONTAINER" /opendustpan --version)" != "smoke" ]; then
  fail "--version did not print the stamped version"
fi

echo "smoke-image: checking the process user is not root"
USER_NAME="$(docker inspect -f '{{.Config.User}}' "$CONTAINER")"
if [ -z "$USER_NAME" ] || [ "$USER_NAME" = "root" ] || [ "$USER_NAME" = "0" ]; then
  fail "image user is ${USER_NAME:-unset}, want a non-root user"
fi
PID="$(docker inspect -f '{{.State.Pid}}' "$CONTAINER")"
UID_NUM="$(awk '/^Uid:/{print $2}' "/proc/$PID/status")"
if [ "$UID_NUM" = "0" ]; then
  fail "container process runs as uid 0"
fi
echo "smoke-image: user $USER_NAME (uid $UID_NUM)"

echo "smoke-image: checking the license files in /licenses"
LICENSE_FILES="$(docker cp "$CONTAINER:/licenses" - | tar -t)"
for want in licenses/LICENSE licenses/third_party_licenses/go/LICENSE \
  licenses/third_party_licenses/modernc.org/sqlite/LICENSE \
  licenses/third_party_licenses/github.com/jackc/pgx/v5/LICENSE; do
  if ! grep -qx "$want" <<<"$LICENSE_FILES"; then
    fail "the image has no /$want"
  fi
done

echo "smoke-image: PASS"
