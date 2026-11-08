#!/usr/bin/env bash
# Check that the lines a branch adds carry no nolint or nosemgrep marker.
# Usage: scripts/check-nolint.sh [base-ref]   (default origin/main)
#
# The script reads the added lines of git diff -U0 <base>...HEAD and prints
# each file:line that adds a marker. A moved line counts as added. It exits 1
# when it finds a marker, 0 when it finds none, and 2 when the base ref does
# not exist. A marker is allowed only with a specific linter or rule and a
# real reason, and a reviewer judges the reason. This script does not judge it.
#
# The script skips its own file, because the marker text is in its code.
#
# Defense in depth: an empty diff finds no marker and passes. A failing
# git diff stops the script with an error through set -e, so it never
# passes. The base ref is checked before the diff runs.
set -euo pipefail

base="${1:-origin/main}"

if ! git rev-parse --verify --quiet "${base}^{commit}" >/dev/null; then
  echo "check-nolint: base ref ${base} does not exist; fetch it first" >&2
  exit 2
fi

diff_text="$(git diff -U0 "${base}...HEAD" -- . ':(exclude)scripts/check-nolint.sh')"

hits="$(printf '%s\n' "$diff_text" | awk '
  /^\+\+\+ / { file = substr($0, 7); next }
  /^@@ /     { split($3, range, ","); line = substr(range[1], 2) - 1; next }
  /^\+/      { line++; if ($0 ~ /\/\/nolint|nosemgrep/) print file ":" line }
')"

if [[ -n "$hits" ]]; then
  printf '%s\n' "$hits"
  echo "check-nolint: the lines above add a nolint or nosemgrep marker since ${base}" >&2
  echo "check-nolint: a marker needs a specific linter or rule and a real reason; a pull request with the label nolint-approved skips this check in CI" >&2
  exit 1
fi

echo "check-nolint: no added line carries a nolint or nosemgrep marker since ${base}"
