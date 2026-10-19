#!/usr/bin/env bash
# Builds the container image from this checkout and tags it opendustpan:local,
# the tag that deploy/compose/compose.yaml uses by default.
#
# No console release exists yet, so the image holds a one-page placeholder
# console that this script builds. Set CONSOLE_URL and CONSOLE_SHA256 to use
# a console release tarball instead.
#
# Environment:
#   IMAGE           image tag to build (default opendustpan:local)
#   CONSOLE_URL     https: URL of a console release tarball (default: placeholder)
#   CONSOLE_SHA256  tarball checksum (required with CONSOLE_URL)
#   EXTRA_CA_FILE   optional PEM bundle file with one more CA to trust during
#                   the build, for hosts behind a TLS-intercepting proxy
#   DOCKER_BUILD_NETWORK  optional network mode for the build (the --network
#                   option of docker build), for example host
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE="${IMAGE:-opendustpan:local}"
CONSOLE_URL="${CONSOLE_URL:-}"
CONSOLE_SHA256="${CONSOLE_SHA256:-}"
TARBALL="$ROOT/console-local.tgz"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"; rm -f "$TARBALL"' EXIT

if [ -z "$CONSOLE_URL" ]; then
  mkdir -p "$WORK/console"
  printf '%s\n' '<!doctype html><title>OpenDustpan</title><p>Placeholder console.</p>' >"$WORK/console/index.html"
  tar -czf "$TARBALL" -C "$WORK/console" .
  CONSOLE_URL="file:console-local.tgz"
  if command -v sha256sum >/dev/null 2>&1; then
    CONSOLE_SHA256="$(sha256sum "$TARBALL" | awk '{print $1}')"
  else
    CONSOLE_SHA256="$(shasum -a 256 "$TARBALL" | awk '{print $1}')"
  fi
elif [ -z "$CONSOLE_SHA256" ]; then
  echo "build-image-local: CONSOLE_SHA256 is required with CONSOLE_URL" >&2
  exit 1
fi

BUILD_ARGS=()
if [ -n "${EXTRA_CA_FILE:-}" ]; then
  BUILD_ARGS+=(--secret "id=extra_ca,src=$EXTRA_CA_FILE")
fi
if [ -n "${DOCKER_BUILD_NETWORK:-}" ]; then
  BUILD_ARGS+=(--network "$DOCKER_BUILD_NETWORK")
fi

docker build -t "$IMAGE" \
  --build-arg VERSION=dev \
  --build-arg CONSOLE_URL="$CONSOLE_URL" \
  --build-arg CONSOLE_SHA256="$CONSOLE_SHA256" \
  ${BUILD_ARGS[@]+"${BUILD_ARGS[@]}"} \
  "$ROOT"
echo "build-image-local: built $IMAGE"
