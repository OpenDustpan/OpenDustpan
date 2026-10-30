#!/usr/bin/env bash
# agent-preflight checks the container of a session before it builds or
# tests anything. Run it with make agent-preflight at the start of a session
# and after a container restart.
#
# Checks, in order:
#   tools       go, git and psql are on PATH
#   lint        golangci-lint has the version that the Makefile pins
#   postgres    DUSTPAN_TEST_POSTGRES_URL is set and PostgreSQL 18 answers;
#               the script starts the local cluster with pg_ctlcluster if it
#               does not answer
#   main        origin/main is the current main of the upstream repository,
#               and the report gives the number of main commits that HEAD
#               does not hold
#   disk        the file system of the Go build cache has enough free space
#
# Each check prints one line with PASS or FAIL. A FAIL line names the one
# command that helps. The exit code is 1 when one check fails.
#
# Environment:
#   UPSTREAM_URL      the repository to fetch main from
#                     (default https://github.com/OpenDustpan/OpenDustpan.git)
#   REQUIRE_CURRENT   1 makes the main check fail when HEAD does not hold
#                     the current main (default 0: report only)
#   MIN_FREE_GIB      the free space that the disk check needs (default 10)
#   PG_START          0 stops the script from starting PostgreSQL (default 1)
#
# Usage: agent-preflight.sh [--selftest]
#
# The script never prints the PostgreSQL URL, because it can hold a password.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPSTREAM_URL="${UPSTREAM_URL:-https://github.com/OpenDustpan/OpenDustpan.git}"
REQUIRE_CURRENT="${REQUIRE_CURRENT:-0}"
MIN_FREE_GIB="${MIN_FREE_GIB:-10}"
PG_START="${PG_START:-1}"
PG_MAJOR=18

FAILED=0

report() {
  local detail
  # Remove CR and LF from the detail, so that one check gives one line.
  detail="$(printf '%s' "$3" | tr '\r\n' '  ')"
  echo "preflight: $1: $2 $detail"
  if [ "$2" = "FAIL" ]; then
    FAILED=1
  fi
}

# pinned_lint_version prints the golangci-lint version that the Makefile
# pins, for example v2.15.0. It prints nothing when it finds no pin.
pinned_lint_version() {
  sed -n 's/^#[[:space:]]*golangci-lint[[:space:]]\{1,\}\(v[0-9]\{1,\}\.[0-9]\{1,\}\.[0-9]\{1,\}\)[[:space:]].*/\1/p' "$1" | head -n 1
}

# lint_version_matches returns 0 when the output of golangci-lint version
# names exactly the pinned version. A pin that is not of the form vX.Y.Z
# never matches.
lint_version_matches() {
  local pin="$1" out="$2" want got
  case "$pin" in
    v[0-9]*.[0-9]*.[0-9]*) ;;
    *) return 1 ;;
  esac
  want="${pin#v}"
  case "$want" in
    *[!0-9.]*) return 1 ;;
  esac
  got="$(printf '%s\n' "$out" | head -n 1 | sed -n 's/^golangci-lint has version v\{0,1\}\([^ ]*\) .*/\1/p')"
  [ -n "$got" ] && [ "$got" = "$want" ]
}

# pg_version_ok returns 0 when server_version_num is of major version 18.
pg_version_ok() {
  case "$1" in
    '' | *[!0-9]*) return 1 ;;
  esac
  [ "$(($1 / 10000))" = "$PG_MAJOR" ]
}

# free_space_ok returns 0 when the free KiB in $1 is at least $2 GiB.
free_space_ok() {
  case "$1" in
    '' | *[!0-9]*) return 1 ;;
  esac
  case "$2" in
    '' | *[!0-9]*) return 1 ;;
  esac
  [ "$1" -ge $(($2 * 1024 * 1024)) ]
}

check_tools() {
  local missing="" tool
  for tool in go git psql; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      missing="$missing $tool"
    fi
  done
  if [ -n "$missing" ]; then
    report tools FAIL "missing:$missing. Install the missing tools, then run make agent-preflight again."
    return
  fi
  report tools PASS "go, git and psql are on PATH."
}

check_lint() {
  local pin out got
  pin="$(pinned_lint_version "$ROOT/Makefile")"
  if [ -z "$pin" ]; then
    report lint FAIL "the Makefile pins no golangci-lint version."
    return
  fi
  if ! command -v golangci-lint >/dev/null 2>&1; then
    report lint FAIL "golangci-lint is not on PATH. Run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$pin"
    return
  fi
  out="$(golangci-lint version 2>&1)"
  if ! lint_version_matches "$pin" "$out"; then
    got="$(printf '%s\n' "$out" | head -n 1 | cut -c1-60)"
    report lint FAIL "want $pin, got '$got'. Run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$pin"
    return
  fi
  report lint PASS "golangci-lint $pin."
}

pg_answers() {
  psql "$DUSTPAN_TEST_POSTGRES_URL" -XAtqc 'show server_version_num' 2>/dev/null
}

check_postgres() {
  local num i
  if [ -z "${DUSTPAN_TEST_POSTGRES_URL:-}" ]; then
    report postgres FAIL "DUSTPAN_TEST_POSTGRES_URL is not set. Ask the PM for the environment that sets it."
    return
  fi
  num="$(pg_answers)"
  if [ -z "$num" ] && [ "$PG_START" = 1 ] && command -v pg_ctlcluster >/dev/null 2>&1; then
    pg_ctlcluster "$PG_MAJOR" main start >/dev/null 2>&1 || true
    for ((i = 0; i < 30; i++)); do
      num="$(pg_answers)"
      [ -n "$num" ] && break
      sleep 1
    done
  fi
  if [ -z "$num" ]; then
    report postgres FAIL "PostgreSQL does not answer at DUSTPAN_TEST_POSTGRES_URL. Run: pg_ctlcluster $PG_MAJOR main start"
    return
  fi
  if ! pg_version_ok "$num"; then
    report postgres FAIL "server_version_num is '$(printf '%s' "$num" | cut -c1-12)', want PostgreSQL $PG_MAJOR."
    return
  fi
  report postgres PASS "PostgreSQL $PG_MAJOR answers."
}

check_main() {
  local main_sha behind
  if ! git -C "$ROOT" fetch -q "$UPSTREAM_URL" "+refs/heads/main:refs/remotes/origin/main" 2>/dev/null; then
    report main FAIL "git fetch of main failed. Run: git fetch $UPSTREAM_URL +refs/heads/main:refs/remotes/origin/main"
    return
  fi
  main_sha="$(git -C "$ROOT" rev-parse refs/remotes/origin/main)"
  behind="$(git -C "$ROOT" rev-list --count HEAD..refs/remotes/origin/main 2>/dev/null)"
  if [ -z "$behind" ]; then
    report main FAIL "cannot count the main commits that HEAD does not hold."
    return
  fi
  if [ "$behind" = 0 ]; then
    report main PASS "HEAD holds main $main_sha."
    return
  fi
  if [ "$REQUIRE_CURRENT" = 1 ]; then
    report main FAIL "HEAD does not hold $behind commits of main $main_sha. Run: git merge refs/remotes/origin/main"
    return
  fi
  report main PASS "main is $main_sha. HEAD does not hold $behind of its commits. Merge main only for a real conflict or when the PM asks."
}

check_disk() {
  local cache avail size
  cache="$(go env GOCACHE 2>/dev/null)"
  if [ -z "$cache" ] || [ ! -d "$cache" ]; then
    cache="$ROOT"
  fi
  avail="$(df -Pk "$cache" 2>/dev/null | awk 'NR == 2 { print $4 }')"
  size="$(du -sh "$cache" 2>/dev/null | cut -f1)"
  if ! free_space_ok "$avail" "$MIN_FREE_GIB"; then
    report disk FAIL "free space is ${avail:-unknown} KiB, want $MIN_FREE_GIB GiB. The Go build cache holds ${size:-unknown}. Run: go clean -cache"
    return
  fi
  report disk PASS "$((avail / 1024 / 1024)) GiB free. The Go build cache holds ${size:-unknown}."
}

selftest() {
  local pass=0 fail=0 tmp good marker url out
  expect() {
    local want="$1" got
    shift
    if "$@"; then got=0; else got=1; fi
    if [ "$got" = "$want" ]; then
      pass=$((pass + 1))
    else
      fail=$((fail + 1))
      echo "selftest: FAIL: $* gave $got, want $want" >&2
    fi
  }

  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  printf '# Tool versions\n#   golangci-lint v2.15.0  https://example.org/\n' >"$tmp/pin"
  printf '# golangci-lint latest https://example.org/\n' >"$tmp/nopin"
  if [ "$(pinned_lint_version "$tmp/pin")" = "v2.15.0" ]; then pass=$((pass + 1)); else
    fail=$((fail + 1))
    echo "selftest: FAIL: pin not read" >&2
  fi
  if [ -z "$(pinned_lint_version "$tmp/nopin")" ]; then pass=$((pass + 1)); else
    fail=$((fail + 1))
    echo "selftest: FAIL: a pin that is not vX.Y.Z was read" >&2
  fi
  if [ "$(pinned_lint_version "$ROOT/Makefile")" != "" ]; then pass=$((pass + 1)); else
    fail=$((fail + 1))
    echo "selftest: FAIL: the Makefile pin is not read" >&2
  fi

  good='golangci-lint has version 2.15.0 built with go1.27.3 from (unknown)'
  expect 0 lint_version_matches v2.15.0 "$good"
  expect 0 lint_version_matches v2.15.0 'golangci-lint has version v2.15.0 built with go1.27.3'
  expect 1 lint_version_matches v2.15.0 'golangci-lint has version 2.14.0 built with go1.27.3'
  expect 1 lint_version_matches v2.15.0 'golangci-lint has version 2.15.0-rc1 built with go1.27.3'
  expect 1 lint_version_matches v2.15.0 'golangci-lint has version 2.15.01 built with go1.27.3'
  expect 1 lint_version_matches v2.15.0 'golangci-lint has version 12.15.0 built with go1.27.3'
  expect 1 lint_version_matches v2.15.0 ''
  expect 1 lint_version_matches v2.15.0 'command not found'
  expect 1 lint_version_matches '' "$good"
  expect 1 lint_version_matches 'v2.*.0' "$good"
  expect 1 lint_version_matches latest "$good"

  expect 0 pg_version_ok 180000
  expect 0 pg_version_ok 180004
  expect 1 pg_version_ok 170009
  expect 1 pg_version_ok 190000
  expect 1 pg_version_ok ''
  expect 1 pg_version_ok '18.0'
  expect 1 pg_version_ok '180000; drop table x'

  expect 0 free_space_ok $((10 * 1024 * 1024)) 10
  expect 1 free_space_ok $((10 * 1024 * 1024 - 1)) 10
  expect 1 free_space_ok '' 10
  expect 1 free_space_ok 'unknown' 10
  expect 1 free_space_ok 99999999 ''
  expect 1 free_space_ok 99999999 'x'

  # A failed PostgreSQL check must not print the URL, because the URL can
  # hold a password. Port 1 on the loopback address refuses at once. The URL
  # is built from parts, so that the file holds no literal URL with a password.
  marker="selftest-hidden-value"
  url="$(printf '%s://%s:%s@%s/%s' postgres agent "$marker" 127.0.0.1:1 none)"
  out="$(DUSTPAN_TEST_POSTGRES_URL="$url" PG_START=0 FAILED=0 check_postgres 2>&1)"
  case "$out" in
    *"$marker"*)
      fail=$((fail + 1))
      echo "selftest: FAIL: the postgres check printed the URL" >&2
      ;;
    *"postgres: FAIL"*) pass=$((pass + 1)) ;;
    *)
      fail=$((fail + 1))
      echo "selftest: FAIL: the postgres check did not fail on a closed port" >&2
      ;;
  esac
  out="$(DUSTPAN_TEST_POSTGRES_URL='' FAILED=0 check_postgres 2>&1)"
  case "$out" in
    *"postgres: FAIL DUSTPAN_TEST_POSTGRES_URL is not set"*) pass=$((pass + 1)) ;;
    *)
      fail=$((fail + 1))
      echo "selftest: FAIL: an empty URL did not fail" >&2
      ;;
  esac

  # One check gives one line, also when a detail holds CR or LF.
  out="$(report probe FAIL "$(printf 'a\nb\rc')")"
  if [ "$(printf '%s\n' "$out" | wc -l)" = 1 ]; then pass=$((pass + 1)); else
    fail=$((fail + 1))
    echo "selftest: FAIL: a detail with CR or LF gave more than one line" >&2
  fi

  echo "agent-preflight selftest: $pass cases passed, $fail failed"
  [ "$fail" = 0 ]
}

if [ "${1:-}" = "--selftest" ]; then
  selftest
  exit $?
fi
if [ $# -gt 0 ]; then
  echo "preflight: unknown argument '$(printf '%s' "$1" | tr -d '\r\n' | cut -c1-40)'. Usage: agent-preflight.sh [--selftest]" >&2
  exit 2
fi

check_tools
check_lint
check_postgres
check_main
check_disk
exit "$FAILED"
