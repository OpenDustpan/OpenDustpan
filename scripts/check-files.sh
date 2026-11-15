#!/usr/bin/env bash
# Check the files that a branch adds or changes against the merge base with main.
# Usage: scripts/check-files.sh [base-ref]   (default origin/main)
#
# The script fails on:
#   - a file over 1 MiB (1048576 bytes) that .large-files-allowlist does not list;
#   - a binary file (a NUL byte in its first 8000 bytes) outside a testdata/
#     directory that the allowlist does not list.
#
# .large-files-allowlist has one line for each allowed path: the path, then a
# reason. Blank lines and lines that start with "#" are comments. A line with a
# path and no reason, or a path that starts with "/" or "-", has a ".." part or
# a glob character, fails the check.
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
  # The allowlist is branch content, so the messages show each control
  # character as "?", as printable_path does for a file name.
  if LC_ALL=C awk '
    function printable(s) { gsub(/[[:cntrl:]]/, "?", s); return s }
    !/^#/ && NF {
      if (NF == 1) {
        bad = 1
        print "check-files: .large-files-allowlist:" FNR " lists " printable($1) " with no reason" > "/dev/stderr"
      }
      if ($1 ~ /^[\/-]/ || $1 ~ /(^|\/)\.\.(\/|$)/ || $1 ~ /[*?\[\]]/) {
        bad = 1
        print "check-files: .large-files-allowlist:" FNR " lists invalid path " printable($1) > "/dev/stderr"
      }
    }
    END { exit bad }
  ' "$allow"; then
    :
  else
    exit 1
  fi
fi

# allowed reports whether the allowlist lists a path.
allowed() {
  [[ -f "$allow" ]] || return 1
  # Defense in depth: matches still exclude comments and require a reason after validation.
  # The environment preserves backslashes. The prefix prevents numeric comparison.
  FILES_CHECK_PATH="$1" awk '!/^#/ && NF >= 2 && ("x" $1) == ("x" ENVIRON["FILES_CHECK_PATH"]) { found = 1 } END { exit !found }' "$allow"
}

# Defense in depth: log output does not rely on Git path parsing or allowlist validation.
# The allowlist and file checks use the raw path, not this display value.
printable_path() {
  printf '%s' "$1" | LC_ALL=C tr '[:cntrl:]' '?'
}

# has_nul reports whether the first 8000 bytes of the file at HEAD hold a NUL
# byte. It reads the blob itself, so a .gitattributes line (diff, text) that
# changes how git diffs the file cannot hide a binary file. A failed read
# stops the script; only SIGPIPE (exit 141) from the early close of head is
# not an error. The caller runs this in an if condition, where set -e does not
# apply, so the failure exits here explicitly.
has_nul() {
  local nuls
  if ! nuls="$( { git cat-file blob "HEAD:$1" || [[ $? -eq 141 ]]; } | head -c 8000 | LC_ALL=C tr -dc '\000' | wc -c)"; then
    echo "check-files: cannot read $(printable_path "$1") at HEAD" >&2
    exit 1
  fi
  (( nuls > 0 ))
}

problems=0

# The list goes to a file first, so that a failed git command stops the script.
# A pipe into the loop would hide the failure and pass the check.
changes="$(mktemp)"
trap 'rm -f "$changes"' EXIT

# --name-only -z prints each path with a NUL after it. The filter keeps added,
# copied, modified and renamed files, and --no-renames reports each one with
# its new path.
git diff --name-only -z --no-renames --diff-filter=ACMR "$base...HEAD" > "$changes"

while IFS= read -r -d '' path; do
  if allowed "$path"; then
    continue
  fi

  display_path="$(printable_path "$path")"
  size="$(git cat-file -s "HEAD:$path")"
  if (( size > limit )); then
    echo "check-files: $display_path is $size bytes, over $limit; list it in .large-files-allowlist with a reason, or do not commit it" >&2
    problems=$((problems + 1))
  fi

  if has_nul "$path"; then
    case "$path" in
      testdata/*|*/testdata/*) ;;
      *)
        echo "check-files: $display_path is a binary file outside testdata/; do not commit build output, or list it in .large-files-allowlist with a reason" >&2
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
