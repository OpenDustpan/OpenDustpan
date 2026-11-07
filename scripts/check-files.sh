#!/usr/bin/env bash
# Check the files that a branch adds or changes against the merge base with main.
# Usage: scripts/check-files.sh [base-ref]   (default origin/main)
#
# The script fails on:
#   - a file over 1 MiB (1048576 bytes) that .large-files-allowlist does not list;
#   - a binary file outside a testdata/ directory that the allowlist does not list.
#
# .large-files-allowlist has one line for each allowed path: the path, then a
# reason. Blank lines and lines that start with "#" are comments. A line with a
# path and no reason fails the check.
#
# Sizes come from the commit at HEAD, not from the work tree.
set -euo pipefail

if [[ $# -gt 1 ]]; then
  echo "usage: check-files.sh [base-ref]" >&2
  exit 2
fi

base="${1:-origin/main}"

# A ref that starts with "-" would read as a Git option. Refuse it.
case "$base" in
  -*) echo "check-files: base ref $base starts with -" >&2; exit 2 ;;
esac

limit=1048576
root="$(git rev-parse --show-toplevel)"
allow="$root/.large-files-allowlist"

cd "$root"

if [[ -f "$allow" ]]; then
  if awk 'NF == 1 && !/^#/ { bad = 1; print "check-files: " FILENAME ":" FNR " lists " $1 " with no reason" > "/dev/stderr" } END { exit bad }' "$allow"; then
    :
  else
    exit 1
  fi
fi

# allowed reports whether the allowlist lists a path.
allowed() {
  [[ -f "$allow" ]] || return 1
  awk -v p="$1" '!/^#/ && NF >= 2 && $1 == p { found = 1 } END { exit !found }' "$allow"
}

problems=0

# The diff goes to a file first, so that a failed git command stops the script.
# A pipe into the loop would hide the failure and pass the check.
changes="$(mktemp)"
trap 'rm -f "$changes"' EXIT

# --numstat -z prints "added<TAB>deleted<TAB>path<NUL>" for each file. A binary
# file has "-" for both counts. The filter keeps added, copied, modified and
# renamed files, and --no-renames reports each one with its new path.
git diff --numstat -z --no-renames --diff-filter=ACMR "$base...HEAD" > "$changes"

while IFS= read -r -d '' rec; do
  added="${rec%%$'\t'*}"
  rest="${rec#*$'\t'}"
  deleted="${rest%%$'\t'*}"
  path="${rest#*$'\t'}"

  if allowed "$path"; then
    continue
  fi

  size="$(git cat-file -s "HEAD:$path")"
  if (( size > limit )); then
    echo "check-files: $path is $size bytes, over $limit; list it in .large-files-allowlist with a reason, or do not commit it" >&2
    problems=$((problems + 1))
  fi

  if [[ "$added" == "-" && "$deleted" == "-" ]]; then
    case "$path" in
      testdata/*|*/testdata/*) ;;
      *)
        echo "check-files: $path is a binary file outside testdata/; do not commit build output, or list it in .large-files-allowlist with a reason" >&2
        problems=$((problems + 1))
        ;;
    esac
  fi
done < "$changes"

if (( problems > 0 )); then
  echo "check-files: $problems problem(s) against $base" >&2
  exit 1
fi

echo "check-files: no file over $limit bytes and no binary file outside testdata/ against $base"
