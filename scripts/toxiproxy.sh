#!/bin/sh
# Download the pinned Toxiproxy server binary for local fault tests.
#
# The fault tests run locally only with `make faults`. Toxiproxy is a
# test-only tool and never ships in the server binary.
#
# It downloads toxiproxy-server 2.12.0 for linux/amd64, checks its SHA-256,
# makes it executable, and prints its path.
#
# Environment:
#   TOXIPROXY_CACHE_DIR  cache directory (default $HOME/.cache/opendustpan/toxiproxy-2.12.0)
set -eu

VERSION="2.12.0"
URL="https://github.com/Shopify/toxiproxy/releases/download/v2.12.0/toxiproxy-server-linux-amd64"
WANT_SHA="556d891134a3c582dc1e1a3f7335fd55142e5965769855a00b944e13e48302fc"

CACHE_DIR="${TOXIPROXY_CACHE_DIR:-${HOME:-/tmp}/.cache/opendustpan/toxiproxy-2.12.0}"
BIN="$CACHE_DIR/toxiproxy-server"

mkdir -p "$CACHE_DIR"

if [ ! -f "$BIN" ]; then
	if ! command -v curl >/dev/null 2>&1; then
		echo "toxiproxy: curl is not installed; install curl and run make faults again" >&2
		exit 1
	fi
	tmp="$CACHE_DIR/.toxiproxy-server.download"
	rm -f "$tmp"
	curl -fsSL --proto '=https' --tlsv1.2 -o "$tmp" "$URL"
	mv "$tmp" "$BIN"
fi

if ! command -v sha256sum >/dev/null 2>&1; then
	echo "toxiproxy: sha256sum is not installed; install coreutils and run make faults again" >&2
	exit 1
fi

got="$(sha256sum "$BIN" | cut -d' ' -f1)"
if [ "$got" != "$WANT_SHA" ]; then
	echo "toxiproxy: checksum mismatch for $BIN: got $got, want $WANT_SHA" >&2
	exit 1
fi

chmod +x "$BIN"
printf '%s\n' "$BIN"
