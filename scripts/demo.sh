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
#   CONSOLE_REF   console commit to build (set by make demo; default in the Makefile)
#   DEMO_PORT     port to listen on (default 8080)
#
# It needs Git, Go, Node and npm. It needs no Pulumi or OpenTofu CLI.
set -eu

: "${CONSOLE_REPO:=https://github.com/OpenDustpan/console}"
: "${CONSOLE_REF:?demo: CONSOLE_REF is not set; run make demo}"
: "${DEMO_PORT:=8080}"

for tool in git go node npm; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "demo: missing required tool: $tool: install it and run make demo again" >&2
    exit 1
  fi
done

DEMO_TMP="$(mktemp -d)"
server_pid=""
SERVER_LOG="$DEMO_TMP/server.log"

cleaned=0
interrupted=0
cleanup() {
  status=$?
  if [ "$cleaned" -eq 1 ]; then
    return 0
  fi
  cleaned=1
  if [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if [ "$interrupted" -eq 0 ] && [ "$status" -ne 0 ] && [ -f "$SERVER_LOG" ]; then
    echo "demo: server log (last 60 lines):" >&2
    tail -n 60 "$SERVER_LOG" >&2 || true
  fi
  rm -rf "$DEMO_TMP" 2>/dev/null || true
}
trap 'cleanup' EXIT
trap 'interrupted=1; exit 130' INT TERM

ADMIN_TOKEN="$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')"
if [ "${#ADMIN_TOKEN}" -lt 32 ]; then
  echo "demo: generated admin token is too short: run make demo again" >&2
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
  echo "demo: console build did not write dist/index.html: run make demo again" >&2
  exit 1
fi

mkdir -p "$DEMO_TMP/data/blobs" "$DEMO_TMP/backups"

DEMO_URL="http://127.0.0.1:$DEMO_PORT"

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
    echo "demo: server exited before it was ready: the server log is printed above" >&2
    exit 1
  fi
  if ./bin/opendustpan health --listen "127.0.0.1:$DEMO_PORT" >/dev/null 2>&1; then
    break
  fi
  sleep 1
  i=$((i + 1))
done
if ! ./bin/opendustpan health --listen "127.0.0.1:$DEMO_PORT" >/dev/null 2>&1; then
  echo "demo: server did not answer $DEMO_URL/health/ready in time: the server log is printed above" >&2
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
