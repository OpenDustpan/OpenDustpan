#!/usr/bin/env bash
# checks-agent runs the required suites of a branch after it takes in main.
#
# It syncs with origin/main, starts a PostgreSQL container when
# DUSTPAN_TEST_POSTGRES_URL is not set, runs make generate, make check and
# make e2e, scans the branch commits with TruffleHog, then removes the
# container and the temporary files it made. Run it with make checks-agent.
#
# Environment:
#   SYNC          how the branch takes in main: merge (default), rebase, none
#   SKIP          space-separated step names to skip, for example "e2e trufflehog"
#   CLEAN_CACHE   1 also deletes the e2e CLI download cache (default 0)
#   DUSTPAN_TEST_POSTGRES_URL  PostgreSQL URL for the suites (default: start
#                              a container and export its URL for the run)
set -euo pipefail

export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.27.4}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

SYNC="${SYNC:-merge}"
SKIP="${SKIP:-}"
CLEAN_CACHE="${CLEAN_CACHE:-0}"

# STEPS holds the run order. A new suite is one entry here and one step
# function named step_<entry> below.
STEPS=(sync postgres generate check e2e trufflehog)

declare -A STATUS
declare -A DURATION

CONTAINER=""
STARTED_PG=0
USED_EXISTING_PG=0
ABORT=0
TMP_ROOT="$(mktemp -d)"
E2E_CACHE="${E2E_CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/opendustpan-e2e}"

log() {
  echo "checks-agent: $1" >&2
}

fail_step() {
  log "$1"
  return 1
}

cleanup() {
  rc=$?
  trap - EXIT INT TERM
  if [ "$STARTED_PG" = 1 ] && [ -n "${DUSTPAN_TEST_POSTGRES_URL:-}" ]; then
    schemas="$(psql "$DUSTPAN_TEST_POSTGRES_URL" -Atc "select schema_name from information_schema.schemata where schema_name like 'test\_%'" 2>/dev/null || true)"
    for schema in $schemas; do
      case "$schema" in
        test_*) ;;
        *) continue ;;
      esac
      psql "$DUSTPAN_TEST_POSTGRES_URL" -c "drop schema \"$schema\" cascade" >/dev/null 2>&1 || true
    done
  elif [ "$USED_EXISTING_PG" = 1 ] && [ -n "${DUSTPAN_TEST_POSTGRES_URL:-}" ]; then
    count="$(psql "$DUSTPAN_TEST_POSTGRES_URL" -Atc "select count(*) from pg_stat_activity where datname = current_database() and pid <> pg_backend_pid()" 2>/dev/null || true)"
    count="$(printf '%s' "$count" | tr -d '[:space:]')"
    if [ "$count" = "0" ]; then
      schemas="$(psql "$DUSTPAN_TEST_POSTGRES_URL" -Atc "select schema_name from information_schema.schemata where schema_name like 'test\_%'" 2>/dev/null || true)"
      for schema in $schemas; do
        case "$schema" in
          test_*) ;;
          *) continue ;;
        esac
        psql "$DUSTPAN_TEST_POSTGRES_URL" -c "drop schema \"$schema\" cascade" >/dev/null 2>&1 || true
      done
    else
      echo "skipped test schema cleanup: $count other clients connected" >&2
    fi
  fi
  if [ -n "$CONTAINER" ]; then
    docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  fi
  if [ -n "${TMP_ROOT:-}" ] && [ -d "$TMP_ROOT" ]; then
    rm -rf "$TMP_ROOT"
  fi
  if [ "$CLEAN_CACHE" = 1 ] && [ -n "${E2E_CACHE:-}" ]; then
    rm -rf "$E2E_CACHE"
  fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

skipped() {
  case " $SKIP " in
    *" $1 "*) return 0 ;;
  esac
  return 1
}

print_conflicts() {
  git diff --name-only --diff-filter=U
}

step_sync() {
  case "$SYNC" in
    none)
      return 2
      ;;
    merge | rebase)
      ;;
    *)
      fail_step "sync: unknown SYNC value '$SYNC', want merge, rebase or none"
      return 1
      ;;
  esac

  if ! git fetch origin main; then
    fail_step "sync: git fetch origin main failed"
    return 1
  fi

  if [ "$SYNC" = "merge" ]; then
    if ! git merge --no-edit origin/main; then
      log "sync: merge conflict in:"
      print_conflicts
      return 1
    fi
    return 0
  fi

  upstream="$(git rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null || true)"
  if [ -n "$upstream" ] && git merge-base --is-ancestor HEAD "$upstream" 2>/dev/null; then
    fail_step "sync: refuse rebase: upstream $upstream already holds this branch, a rebase needs a force-push"
    return 1
  fi
  if ! git rebase origin/main; then
    log "sync: rebase conflict in:"
    print_conflicts
    git rebase --abort || true
    return 1
  fi
  return 0
}

ensure_docker() {
  if docker info >/dev/null 2>&1; then
    return 0
  fi
  log "postgres: docker is not running, starting dockerd"
  dockerd >/dev/null 2>&1 &
  for ((i = 0; i < 60; i++)); do
    if docker info >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  fail_step "postgres: dockerd did not start in 60 seconds"
  return 1
}

step_postgres() {
  if [ -n "${DUSTPAN_TEST_POSTGRES_URL:-}" ]; then
    USED_EXISTING_PG=1
    log "postgres: DUSTPAN_TEST_POSTGRES_URL is set, keep it"
    return 0
  fi
  for tool in docker psql python3; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      fail_step "postgres: missing required tool: $tool"
      return 1
    fi
  done
  ensure_docker || return 1
  port="$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')"
  CONTAINER="checks-agent-pg-$$"
  if ! docker run -d --rm --name "$CONTAINER" -e POSTGRES_PASSWORD=pg --shm-size=1g \
    -p "$port":5432 --tmpfs /var/lib/postgresql \
    mirror.gcr.io/library/postgres:18 >/dev/null; then
    fail_step "postgres: docker run failed"
    return 1
  fi
  DUSTPAN_TEST_POSTGRES_URL="postgres://postgres:pg@127.0.0.1:$port/postgres?sslmode=disable"
  export DUSTPAN_TEST_POSTGRES_URL
  for ((i = 0; i < 60; i++)); do
    if psql "$DUSTPAN_TEST_POSTGRES_URL" -Atc 'select 1' >/dev/null 2>&1; then
      STARTED_PG=1
      log "postgres: container $CONTAINER answers"
      return 0
    fi
    sleep 1
  done
  fail_step "postgres: container $CONTAINER did not answer in 60 seconds, see docker logs $CONTAINER"
  return 1
}

step_generate() {
  if ! make generate; then
    fail_step "generate: make generate failed"
    return 1
  fi
  changes="$(git status --porcelain | grep -v '^??' || true)"
  if [ -n "$changes" ]; then
    log "generate: make generate changed a tracked file:"
    echo "$changes" >&2
    return 1
  fi
  return 0
}

step_check() {
  if ! make check; then
    fail_step "check: make check failed"
    return 1
  fi
  return 0
}

step_e2e() {
  if ! make e2e; then
    fail_step "e2e: make e2e failed"
    return 1
  fi
  return 0
}

step_trufflehog() {
  th="$(command -v trufflehog 2>/dev/null || echo "$HOME/go/bin/trufflehog")"
  if [ ! -x "$th" ]; then
    fail_step "trufflehog: no trufflehog binary, install trufflehog v3.99.2"
    return 1
  fi
  base="$(git merge-base HEAD origin/main)"
  branch="$(git rev-parse --abbrev-ref HEAD)"
  out="$TMP_ROOT/trufflehog.log"
  if ! "$th" git "file://$ROOT" --since-commit "$base" --branch "$branch" \
    --results=verified,unverified,unknown --fail --json >"$out" 2>&1; then
    log "trufflehog: scan failed or found a secret, see $out:"
    tail -n 20 "$out" >&2 || true
    return 1
  fi
  if [ "$(git rev-parse HEAD)" != "$base" ] && grep -q '"chunks":0' "$out"; then
    fail_step "trufflehog: scanned 0 chunks while the branch holds commits past $base"
    return 1
  fi
  return 0
}

run_step() {
  name="$1"
  if [ "$ABORT" = 1 ]; then
    STATUS[$name]="SKIP"
    DURATION[$name]=0
    return 0
  fi
  if skipped "$name"; then
    log "step $name: SKIP"
    STATUS[$name]="SKIP"
    DURATION[$name]=0
    return 0
  fi
  log "step $name: start"
  start="$(date +%s)"
  set +e
  "step_$name"
  rc=$?
  set -e
  end="$(date +%s)"
  DURATION[$name]=$((end - start))
  if [ "$rc" = 2 ]; then
    STATUS[$name]="SKIP"
    log "step $name: SKIP (${DURATION[$name]}s)"
    return 0
  fi
  if [ "$rc" = 0 ]; then
    STATUS[$name]="PASS"
    log "step $name: PASS (${DURATION[$name]}s)"
    return 0
  fi
  STATUS[$name]="FAIL"
  log "step $name: FAIL (${DURATION[$name]}s)"
  ABORT=1
  return 0
}

for step in "${STEPS[@]}"; do
  run_step "$step"
done

log "summary: HEAD $(git rev-parse HEAD)"
failed=0
for step in "${STEPS[@]}"; do
  log "summary: $step ${STATUS[$step]} (${DURATION[$step]}s)"
  if [ "${STATUS[$step]}" = "FAIL" ]; then
    failed=1
  fi
done
exit "$failed"
