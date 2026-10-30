#!/usr/bin/env bash
# checks-agent runs the required suites on the head of a branch.
#
# It runs scripts/agent-preflight.sh (tools, lint version, PostgreSQL 18,
# main, disk), can merge main into the branch, runs make generate, make check
# and make e2e, scans the branch commits with TruffleHog, then removes the
# test schemas and the temporary files it made. Run it with make checks-agent.
# The summary names the HEAD that the suites ran on. The run fails when the
# tree has uncommitted changes to tracked files, because then the result is
# not the result of HEAD.
#
# Environment:
#   SYNC          how the branch takes in main: none (default) or merge. An
#                 author merges main only for a real conflict or when the PM
#                 asks. There is no rebase: a rebase needs a force-push.
#   SKIP          space-separated step names to skip, for example "e2e trufflehog"
#   CLEAN_CACHE   1 also deletes the e2e CLI download cache (default 0)
#   UPSTREAM_URL  the repository to fetch main from
#                 (default https://github.com/OpenDustpan/OpenDustpan.git)
#   DUSTPAN_TEST_POSTGRES_URL  PostgreSQL URL for the suites. It must be set.
#                              The preflight starts the local cluster with
#                              pg_ctlcluster when it does not answer.
set -euo pipefail

export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.27.3}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

SYNC="${SYNC:-none}"
UPSTREAM_URL="${UPSTREAM_URL:-https://github.com/OpenDustpan/OpenDustpan.git}"
export UPSTREAM_URL
SKIP="${SKIP:-}"
CLEAN_CACHE="${CLEAN_CACHE:-0}"

# STEPS holds the run order. A new suite is one entry here and one step
# function named step_<entry> below.
STEPS=(sync preflight generate check e2e trufflehog)

declare -A STATUS
declare -A DURATION

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
  if [ -n "${DUSTPAN_TEST_POSTGRES_URL:-}" ]; then
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
    merge) ;;
    *)
      fail_step "sync: unknown SYNC value '$SYNC', want none or merge"
      return 1
      ;;
  esac

  if ! git fetch "$UPSTREAM_URL" "+refs/heads/main:refs/remotes/origin/main"; then
    fail_step "sync: git fetch of main failed"
    return 1
  fi
  if ! git merge --no-edit refs/remotes/origin/main; then
    log "sync: merge conflict in:"
    print_conflicts
    return 1
  fi
  return 0
}

step_preflight() {
  if ! REQUIRE_CURRENT=0 "$ROOT/scripts/agent-preflight.sh"; then
    fail_step "preflight: a check failed, see the FAIL line above"
    return 1
  fi
  return 0
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
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  log "summary: FAIL the tree has uncommitted changes to tracked files, so this result is not the result of HEAD. Commit them, then run make checks-agent again."
  git status --short --untracked-files=no >&2
  failed=1
fi
for step in "${STEPS[@]}"; do
  log "summary: $step ${STATUS[$step]} (${DURATION[$step]}s)"
  if [ "${STATUS[$step]}" = "FAIL" ]; then
    failed=1
  fi
done
exit "$failed"
