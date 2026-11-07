#!/usr/bin/env bash
# Merge main into the current branch. When the merge stops on a generated
# file, take the side of main for that file, then run make regen and commit.
# The script never rebases, resets, stashes or pushes. It refuses to start
# when the work tree is not clean.
#
# Usage: scripts/merge-main.sh   (no arguments)
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

# The generated files. The list is in scripts/generated-paths.txt and
# scripts/generated-list-check.sh checks it against make regen.
mapfile -t GENERATED < <(grep -v -e '^#' -e '^$' scripts/generated-paths.txt)

MAIN_URL="https://github.com/OpenDustpan/OpenDustpan.git"

if [ -n "$(git status --porcelain)" ]; then
	echo "merge-main: the work tree is not clean; commit or remove your changes first" >&2
	exit 2
fi

git fetch "$MAIN_URL" main

if git merge --no-edit FETCH_HEAD; then
	echo "merge-main: merged with no conflict; run make regen if main changed a generator input"
	exit 0
fi

is_generated() {
	local path="$1" entry
	for entry in "${GENERATED[@]}"; do
		case "$entry" in
		*/) [[ "$path" == "$entry"* ]] && return 0 ;;
		*) [[ "$path" == "$entry" ]] && return 0 ;;
		esac
	done
	return 1
}

others=()
while IFS= read -r path; do
	[ -n "$path" ] || continue
	if ! is_generated "$path"; then
		others+=("$path")
	elif git cat-file -e "FETCH_HEAD:$path" 2>/dev/null; then
		git checkout FETCH_HEAD -- "$path"
	else
		echo "merge-main: main deleted $path; taking the deletion"
		git rm -q -f -- "$path"
	fi
done < <(git diff --name-only --diff-filter=U)

if [ "${#others[@]}" -gt 0 ]; then
	echo "merge-main: conflicts in files that are not generated; resolve them:" >&2
	printf '  %s\n' "${others[@]}" >&2
	exit 1
fi

make regen

# Stage the generated paths by name, and any new file under them.
# Never use git add -u: it stages unrelated tracked changes.
# A path that main deleted does not exist now, so skip it: git rm staged it.
for entry in "${GENERATED[@]}"; do
	if [ -e "$entry" ]; then
		git add -A -- "$entry"
	fi
done
git commit -m "Merge main; regenerate generated files"
