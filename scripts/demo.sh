#!/bin/sh
# Local demo of OpenDustpan: run `make demo` from the repository root.
#
# It builds the console at CONSOLE_REF into a temporary directory, starts
# the dev server from bin/opendustpan (opendustpan dev --demo) with a SQLite
# database in the same directory, seeds demo namespaces, stacks and updates,
# prints the URL with the sign-in of the demo person and the sign-in token, and
# serves until Ctrl-C. The dev server has no backup target, so the demo has no
# backup. Ctrl-C stops the
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

DEMO_URL="http://127.0.0.1:$DEMO_PORT"

# The administrator token goes to a file that only the owner can read. dev reads
# it from the file, so the token is not in the process list.
mkdir -p "$DEMO_TMP/data"
(umask 077 && printf '%s\n' "$ADMIN_TOKEN" >"$DEMO_TMP/admin.token")

echo "demo: starting the server on $DEMO_URL"
# dev prints the demo sign-in once to its stderr. The server log holds it, so the
# script reads it from there and prints it with the URL.
./bin/opendustpan dev --demo \
  --data-dir "$DEMO_TMP/data" \
  --listen "127.0.0.1:$DEMO_PORT" \
  --console-dir "$DEMO_TMP/console/dist" \
  --admin-token-file "$DEMO_TMP/admin.token" \
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
DUSTPAN_ADMIN_TOKEN="$ADMIN_TOKEN" go run ./cmd/demo-seed -skip-backup -url "$DEMO_URL"

# check_console_read sends one GET of the console's reads with the admin token
# and fails when the answer is not 2xx. A 404 means that the console at
# CONSOLE_REF calls a route that this server does not serve. The token goes
# through the environment and the output holds only the status code.
check_console_read() {
  path="$1"
  status="$(DUSTPAN_ADMIN_TOKEN="$ADMIN_TOKEN" node -e '
fetch(process.argv[1], {
  headers: { Authorization: "Bearer " + process.env.DUSTPAN_ADMIN_TOKEN },
}).then((r) => process.stdout.write(String(r.status)), () => process.stdout.write("0"));
' "$DEMO_URL$path")"
  case "$status" in
    2??) ;;
    404)
      echo "demo: the console at CONSOLE_REF calls routes that this server does not serve; update CONSOLE_REF: GET $path answered 404" >&2
      exit 1
      ;;
    *)
      echo "demo: GET $path answered $status, not 2xx: the server log is printed above" >&2
      exit 1
      ;;
  esac
}

echo "demo: checking the console reads"
check_console_read "/api/dustpan/v1/namespaces/demo-platform/units"

printf 'OpenDustpan demo is ready at %s/\n' "$DEMO_URL"
demo_line="$(grep -m 1 '^Demo user: ' "$SERVER_LOG" || true)"
if [ -z "$demo_line" ]; then
  echo "demo: the server did not print the demo sign-in: the server log is printed above" >&2
  exit 1
fi
printf '%s\n' "$demo_line"
printf 'Sign-in token for the sign-in page: %s\n' "$ADMIN_TOKEN"

if command -v xdg-open >/dev/null 2>&1; then
  xdg-open "$DEMO_URL/" >/dev/null 2>&1 || true
elif command -v open >/dev/null 2>&1; then
  open "$DEMO_URL/" >/dev/null 2>&1 || true
fi

wait "$server_pid"
