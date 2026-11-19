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
#   IMAGE_VERSION   version that the binary prints for --version, passed as the
#                   build argument VERSION (default dev); letters, digits, dot,
#                   plus and dash, 1 to 64 characters, for example v0.1.0
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

# The version of the image. It goes into a shell line of the Dockerfile, so the
# script refuses a value outside the allowlist before any docker call. The
# check refuses and never edits the value. Defense in depth: the Dockerfile
# checks VERSION too; this check stays so that no docker call runs for a bad
# value, and it does not rely on the Dockerfile check.
IMAGE_VERSION="${IMAGE_VERSION-dev}"
if ! (LC_ALL=C; [[ "$IMAGE_VERSION" =~ ^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$ ]]); then
  echo "build-image-local: IMAGE_VERSION must be 1 to 64 characters: letters, digits, dot, plus and dash, and it must start with a letter or a digit; for example IMAGE_VERSION=v0.1.0" >&2
  exit 1
fi

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
  --build-arg VERSION="$IMAGE_VERSION" \
  --build-arg CONSOLE_URL="$CONSOLE_URL" \
  --build-arg CONSOLE_SHA256="$CONSOLE_SHA256" \
  ${BUILD_ARGS[@]+"${BUILD_ARGS[@]}"} \
  "$ROOT"
echo "build-image-local: built $IMAGE"
