#!/usr/bin/env bash
# Check that a pull request body names an issue.
# Usage: scripts/check-pr-body.sh <file with the PR body>
#
# The script passes when one line outside a fenced code block matches, case
# insensitively, one of these forms:
#   Closes #<digits>, Fixes #<digits>, Resolves #<digits>, Part of #<digits>
#   No issue, or No issue followed by a colon or a dash and a reason
# GitHub closes an issue when a merged pull request body says "Closes #N", so
# the body decides whether the issue stays open.
#
# CI writes the body to a file and passes the file name. The body never goes on
# the command line, so no shell reads its text.
#
# Defense in depth: the body is untrusted input. The script reads it as data
# with awk and grep only, and it never runs a line of it. The pattern is an
# allowlist, and a body that matches nothing fails.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: check-pr-body.sh <file with the PR body>" >&2
  exit 1
fi

body_file="$1"

if [[ ! -f "$body_file" || ! -r "$body_file" ]]; then
  echo "check-pr-body: $body_file is not a readable file" >&2
  exit 1
fi

# Keep only the lines outside a fenced code block. A fence opens and closes
# with a line that starts with three backticks, after optional spaces.
prose="$(awk '
  /^[[:space:]]*```/ { fenced = !fenced; next }
  !fenced { print }
' "$body_file")"

# Defense in depth: this check does not rely on the fence handling above. A
# line that names an issue must still match the whole pattern, and a closing
# keyword without a number does not match.
if printf '%s\n' "$prose" | grep -Eiq '(^|[^[:alnum:]_])(closes|fixes|resolves|part of)[[:space:]]+#[0-9]+'; then
  exit 0
fi

if printf '%s\n' "$prose" | grep -Eiq '^[[:space:]]*no issue[[:space:]]*([:-].*)?[[:space:]]*$'; then
  exit 0
fi

echo "check-pr-body: the PR body names no issue. Add 'Closes #N', 'Part of #N' or 'No issue: <reason>'." >&2
exit 1
