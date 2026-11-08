#!/usr/bin/env bash
# Run make regen and fail when it writes a file that is not in
# scripts/generated-paths.txt, or when a listed path does not exist.
# Needs a clean work tree. Never resets or stashes.
#
# Usage: scripts/generated-list-check.sh   (no arguments)
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
# A read loop, not mapfile: the bash of macOS (3.2) has no mapfile.
LIST=()
while IFS= read -r line; do
	LIST+=("$line")
done < <(grep -v -e '^#' -e '^$' scripts/generated-paths.txt)

missing=0
for entry in "${LIST[@]}"; do
	if [ ! -e "$entry" ]; then
		echo "generated-list-check: listed path does not exist: $entry" >&2
		missing=1
	fi
done

if [ -n "$(git status --porcelain)" ]; then
	echo "generated-list-check: the work tree is not clean; commit your changes first" >&2
	exit 2
fi

marker="$(mktemp)"
trap 'rm -f "$marker"' EXIT
make regen

extra=()
while IFS= read -r path; do
	listed=0
	for entry in "${LIST[@]}"; do
		case "$entry" in
		*/) [[ "$path" == "$entry"* ]] && listed=1 ;;
		*) [[ "$path" == "$entry" ]] && listed=1 ;;
		esac
	done
	[ "$listed" -eq 1 ] || extra+=("$path")
done < <(find . -type f -newer "$marker" -not -path './.git/*' | sed 's|^\./||' | LC_ALL=C sort)

if [ "${#extra[@]}" -gt 0 ]; then
	echo "generated-list-check: make regen wrote files that are not in scripts/generated-paths.txt:" >&2
	printf '  %s\n' "${extra[@]}" >&2
	exit 1
fi
[ "$missing" -eq 0 ] || exit 1
echo "generated-list-check: the list and the files that make regen writes are the same"
