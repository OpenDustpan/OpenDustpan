#!/usr/bin/env bash
# Fail when the index holds a file that a .gitignore file of the repository
# ignores. Such a file got in with "git add -f" or before its ignore rule.
# Usage: scripts/check-ignored-files.sh
#
# The check reads only the .gitignore files of the repository. It does not
# read .git/info/exclude or the global excludes file of the user, so the result
# is the same on each machine and in CI.
set -euo pipefail

if [[ $# -ne 0 ]]; then
  echo "usage: check-ignored-files.sh" >&2
  exit 2
fi

cd "$(git rev-parse --show-toplevel)"

# -z keeps each path whole. The message shows each control character as "?".
count=0
while IFS= read -r -d '' path; do
  if [[ $count -eq 0 ]]; then
    echo "check-ignored-files: tracked files match a .gitignore rule:" >&2
  fi
  count=$((count + 1))
  printf '  %s\n' "${path//[[:cntrl:]]/?}" >&2
done < <(git ls-files -z --cached --ignored --exclude-per-directory=.gitignore)

if [[ $count -eq 0 ]]; then
  exit 0
fi

echo "check-ignored-files: $count file(s). Remove each from the index with: git rm --cached <path>" >&2
exit 1
