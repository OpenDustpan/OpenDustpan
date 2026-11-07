#!/usr/bin/env bash
# Merge main into the current branch. When the merge stops on a generated
# file, take the side of main for that file, then run make regen and commit.
# The script never rebases, resets, stashes or pushes.
#
# Usage: scripts/merge-main.sh   (no arguments)
set -eu

# The generated files. Keep this list in step with the generators in the
# regen target of the Makefile. A directory entry ends with a slash.
GENERATED=(
	internal/api/apidesc/openapi.json
	clients/typescript/
	internal/clientgen/golang/testdata/golden/
	internal/clientgen/typescript/testdata/golden/
	internal/db/
	docs/adr/INDEX.md
	.repowise/decisions.yaml
)

MAIN_URL="https://github.com/OpenDustpan/OpenDustpan.git"

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
	if is_generated "$path"; then
		git checkout FETCH_HEAD -- "$path"
	else
		others+=("$path")
	fi
done < <(git diff --name-only --diff-filter=U)

if [ "${#others[@]}" -gt 0 ]; then
	echo "merge-main: conflicts in files that are not generated; resolve them:" >&2
	printf '  %s\n' "${others[@]}" >&2
	exit 1
fi

make regen
git add -u
git commit -m "Merge main; regenerate generated files"
