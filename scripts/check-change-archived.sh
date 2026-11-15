#!/usr/bin/env bash
# Fail when a pull request completes an OpenSpec change and does not archive it.
# A change is complete when its tasks.md has at least one task and no open task
# (a line that starts with "- [ ]"). The check looks only at the changes that
# the pull request touches. A change in openspec/changes/archive is archived.
# usage: check-change-archived.sh <base ref>
set -euo pipefail

base="${1:-}"
if [ -z "$base" ]; then
  echo "usage: scripts/check-change-archived.sh <base ref, for example origin/main>" >&2
  exit 2
fi

fail=0
changes=$(git diff --name-only "$base"...HEAD -- openspec/changes |
  awk -F/ '$3 != "archive" && $3 != "" {print $3}' | sort -u)
for change in $changes; do
  tasks="openspec/changes/$change/tasks.md"
  [ -f "$tasks" ] || continue
  total=$(grep -cE '^- \[[ xX]\] ' "$tasks" || true)
  open=$(grep -cE '^- \[ \] ' "$tasks" || true)
  if [ "$total" -gt 0 ] && [ "$open" -eq 0 ]; then
    echo "error: the change $change has $total of $total tasks done and is not archived." >&2
    echo "run: npx @fission-ai/openspec@1.15.0 archive $change --yes" >&2
    fail=1
  fi
done
exit "$fail"
