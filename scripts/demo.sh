#!/bin/sh
# Local demo of OpenDustpan: run `make demo` from the repository root.
#
# It builds the console at CONSOLE_REF into a temporary directory, starts
# the server from bin/opendustpan with a SQLite database in the same
# directory, seeds demo namespaces, stacks, updates and one backup, prints
# the URL with the sign-in token, and serves until Ctrl-C. Ctrl-C stops the
# server and removes the temporary directory.
#
# Environment:
#   CONSOLE_REPO  console repository to fetch (default https://github.com/OpenDustpan/console)
#   CONSOLE_REF   console commit to build (default ff5da1cb541f2d7966ded86a2199f72561e92491)
#   DEMO_PORT     port to listen on (default 8080)
#
# It needs Go, Node and npm. It needs no Pulumi or OpenTofu CLI.
set -eu

: "${CONSOLE_REPO:=https://github.com/OpenDustpan/console}"
: "${CONSOLE_REF:=ff5da1cb541f2d7966ded86a2199f72561e92491}"
: "${DEMO_PORT:=8080}"

for tool in git go node npm; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "demo: missing required tool: $tool" >&2
    exit 1
  fi
done

DEMO_TMP="$(mktemp -d)"
server_pid=""

cleaned=0
cleanup() {
  if [ "$cleaned" -eq 1 ]; then
    return 0
  fi
  cleaned=1
  if [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$DEMO_TMP" 2>/dev/null || true
}
trap 'cleanup' EXIT INT TERM

ADMIN_TOKEN="$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')"
if [ "${#ADMIN_TOKEN}" -lt 32 ]; then
  echo "demo: generated admin token is too short" >&2
  exit 1
fi

echo "demo: fetching the console at $CONSOLE_REF"
git init -q "$DEMO_TMP/console"
git -C "$DEMO_TMP/console" remote add origin "$CONSOLE_REPO"
git -C "$DEMO_TMP/console" fetch --depth 1 origin "$CONSOLE_REF"
git -C "$DEMO_TMP/console" checkout --detach FETCH_HEAD

echo "demo: building the console"
(cd "$DEMO_TMP/console" && npm ci && npm run build)
if [ ! -f "$DEMO_TMP/console/dist/index.html" ]; then
  echo "demo: console build did not write dist/index.html" >&2
  exit 1
fi

mkdir -p "$DEMO_TMP/data/blobs" "$DEMO_TMP/backups"

DEMO_URL="http://127.0.0.1:$DEMO_PORT"
SERVER_LOG="$DEMO_TMP/server.log"

echo "demo: starting the server on $DEMO_URL"
DUSTPAN_ADMIN_TOKEN="$ADMIN_TOKEN" ./bin/opendustpan serve \
  --database "sqlite:$DEMO_TMP/data/dustpan.db" \
  --listen "127.0.0.1:$DEMO_PORT" \
  --blob-dir "$DEMO_TMP/data/blobs" \
  --backup-target "dir:$DEMO_TMP/backups" \
  --console-dir "$DEMO_TMP/console/dist" \
  --key-encryption-key-file "$DEMO_TMP/data/dustpan.key" \
  --generate-key-encryption-key=true \
  >"$SERVER_LOG" 2>&1 &
server_pid=$!

i=0
while [ "$i" -lt 60 ]; do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "demo: server exited before it was ready: read $SERVER_LOG" >&2
    exit 1
  fi
  if node -e 'fetch(process.argv[1]).then(async (r) => { const b = await r.text(); if (r.status !== 200) { process.exit(1); } const d = JSON.parse(b); if (!d || d.status !== "ok") { process.exit(1); } }).catch(() => process.exit(1));' "$DEMO_URL/health/ready" 2>/dev/null; then
    break
  fi
  sleep 1
  i=$((i + 1))
done
if ! node -e 'fetch(process.argv[1]).then(async (r) => { const b = await r.text(); if (r.status !== 200) { process.exit(1); } const d = JSON.parse(b); if (!d || d.status !== "ok") { process.exit(1); } }).catch(() => process.exit(1));' "$DEMO_URL/health/ready" 2>/dev/null; then
  echo "demo: server did not answer $DEMO_URL/health/ready in time: read $SERVER_LOG" >&2
  exit 1
fi

echo "demo: seeding demo data"
DUSTPAN_ADMIN_TOKEN="$ADMIN_TOKEN" go run ./cmd/demo-seed -url "$DEMO_URL"

printf 'OpenDustpan demo is ready at %s/\n' "$DEMO_URL"
printf 'Sign-in token for the sign-in page: %s\n' "$ADMIN_TOKEN"

if command -v xdg-open >/dev/null 2>&1; then
  xdg-open "$DEMO_URL/" >/dev/null 2>&1 || true
elif command -v open >/dev/null 2>&1; then
  open "$DEMO_URL/" >/dev/null 2>&1 || true
fi

wait "$server_pid"
