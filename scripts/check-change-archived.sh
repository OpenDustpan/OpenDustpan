#!/usr/bin/env bash
# Fail when a pull request completes an OpenSpec change and does not archive it.
# A change is complete when it has at least one task and no open task. The
# pinned OpenSpec CLI counts the tasks (nested tasks, the markers "-", "*", "+"
# and "1.", and odd markers such as "[~]"), so that this check and the CLI
# agree. The check looks only at the changes that the pull request touches. A
# change in openspec/changes/archive is archived.
# usage: check-change-archived.sh <base ref>
set -euo pipefail

base="${1:-}"
if [ -z "$base" ]; then
  echo "usage: scripts/check-change-archived.sh <base ref, for example origin/main>" >&2
  exit 2
fi
# Allowlist check before any use of the argument. A ref that starts with "-"
# could be read as an option by git.
case "$base" in
  -*)
    echo "error: the base ref must not start with \"-\": $base" >&2
    exit 2
    ;;
esac
if ! printf '%s' "$base" | grep -Eq '^[A-Za-z0-9._/@^~+-]+$'; then
  echo "error: the base ref has a character that is not allowed: $base" >&2
  exit 2
fi
if ! git rev-parse --verify --quiet --end-of-options "$base^{commit}" >/dev/null; then
  echo "error: the base ref is not a commit: $base" >&2
  exit 2
fi

changes=$(git diff --name-only "$base"...HEAD -- openspec/changes |
  awk -F/ '$3 != "archive" && $3 != "" {print $3}' | sort -u)
if [ -z "$changes" ]; then
  exit 0
fi

# Fail closed: when the CLI or jq is not usable, the check fails and never passes.
if ! command -v jq >/dev/null 2>&1; then
  echo "error: jq is not installed, so the check cannot read the task counts." >&2
  exit 2
fi
root=$(git rev-parse --show-toplevel)
if ! listing=$(cd "$root" && OPENSPEC_TELEMETRY=0 DO_NOT_TRACK=1 OPENSPEC_NO_UPDATE_CHECK=1 \
  DISABLE_TELEMETRY=1 npx --yes @fission-ai/openspec@1.15.0 list --json); then
  echo "error: \"npx @fission-ai/openspec@1.15.0 list --json\" failed, so the check cannot read the task counts." >&2
  exit 2
fi

fail=0
for change in $changes; do
  [ -f "openspec/changes/$change/tasks.md" ] || continue
  if ! counts=$(printf '%s' "$listing" | jq -r --arg n "$change" \
    '.changes[] | select(.name == $n) | "\(.completedTasks) \(.totalTasks)"'); then
    echo "error: the OpenSpec CLI output is not valid JSON, so the check cannot read the task counts." >&2
    exit 2
  fi
  if [ -z "$counts" ]; then
    echo "error: the OpenSpec CLI does not list the change $change." >&2
    exit 2
  fi
  read -r completed total <<<"$counts"
  if [ "$total" -ge 1 ] && [ "$completed" -eq "$total" ]; then
    echo "error: the change $change has $total of $total tasks done and is not archived." >&2
    echo "run: npx @fission-ai/openspec@1.15.0 archive $change --yes" >&2
    fail=1
  fi
done
exit "$fail"
