#!/usr/bin/env bash
# Move the built-in SQL store adapters from pkg/store to internal/store.
#
# Implements PR B of the store-move change: a scripted move with no
# hand-written change. It moves pkg/store/sqlite, pkg/store/postgres,
# pkg/store/schema and pkg/store/internal to internal/store with the same
# names, keeps pkg/store/storetest where it is, rewrites every other
# reference of pkg/store/ to internal/store/, and runs make generate so
# the sqlc query code lands at its new path. A conflict is never resolved
# by hand: run this script again on the new main instead.
#
# Modes:
#   move-store.sh move            Run the move on the current checkout, which
#                                 is a fresh main. Check: go build ./... passes
#                                 and git diff --stat shows only renames,
#                                 import lines and generated files.
#   move-store.sh --branch NAME   Port the open branch NAME: check it out and
#                                 run the same move steps, then review with
#                                 git status and commit. Check: go build ./...
#                                 passes on the branch. Merge main into the
#                                 branch first when the branch also carries
#                                 main-side changes; the steps are idempotent,
#                                 so a branch that already holds the move only
#                                 gets its stragglers rewritten.
#
# The script does not touch openspec/: the history of past proposals keeps
# its paths. Task 3.1 of the change reviews the mechanical rewrites in
# scripts/copy-tag-migrations.sh and scripts/regenerate-baseline.sh by
# hand: both read old tags, whose trees still hold pkg/store/.
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

# MOVE_DIRS holds the directories that move. pkg/store/storetest stays: it
# is a conformance suite and part of the public SDK.
MOVE_DIRS=(sqlite postgres schema internal)

move_dirs() {
	local dir
	for dir in "${MOVE_DIRS[@]}"; do
		if [ -d "pkg/store/$dir" ]; then
			git mv "pkg/store/$dir" "internal/store/$dir"
		fi
	done
}

# sed_inplace applies the move expressions to one file on Linux and macOS.
# GNU sed takes -i alone; BSD sed needs -i with an empty backup suffix.
sed_inplace() {
	local file="$1"
	if [ "$(uname)" = "Darwin" ]; then
		sed -i '' \
			-e 's|pkg/store/|internal/store/|g' \
			-e 's|internal/store/storetest|pkg/store/storetest|g' \
			"$file"
	else
		sed -i \
			-e 's|pkg/store/|internal/store/|g' \
			-e 's|internal/store/storetest|pkg/store/storetest|g' \
			"$file"
	fi
}

rewrite_refs() {
	# Rewrite every pkg/store/ reference except the storetest package path
	# itself. The second expression repairs the storetest path on lines that
	# the first one rewrote; import lines hold one path each. openspec/
	# stays out: past proposals keep their paths. This script stays out
	# too: its header documents the old paths on purpose.
	local file list
	list="$(git grep -l --fixed-strings 'pkg/store/' -- . ':!openspec' ':!scripts/move-store.sh' || true)"
	[ -n "$list" ] || return 0

	while IFS= read -r file; do
		sed_inplace "$file"
	done <<< "$list"
}

run_checks() {
	make generate
	go build ./...
}

move_steps() {
	move_dirs
	rewrite_refs
	run_checks
}

usage() {
	echo "usage: move-store.sh [move|--branch NAME]" >&2
	exit 2
}

MODE="${1-move}"

case "$MODE" in
	move)
		move_steps
		;;
	--branch)
		[ "${2-}" != "" ] || usage
		git checkout "$2"
		move_steps
		echo "move-store: review with git status, then commit the port."
		;;
	*)
		usage
		;;
esac
