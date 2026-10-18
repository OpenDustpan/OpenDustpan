#!/bin/sh
# Collapse the migrations of each store dialect into one new baseline, from
# the numbered migrations of a Git ref, and delete the replaced migrations
# from the work tree.
#
# Run it before each tag, so the migrations that no tag shipped collapse into
# one file. The new baseline gets a version above every migration of the ref
# in both dialects, and at least 100. So a database from an untagged build
# records versions that the new binary does not embed, and the server
# refuses it. The script also sets the baseline constants of the Go code to
# the new version.
#
# The script takes the migration files of the ref, not of the work tree, so
# it gives the same baseline when the work tree already holds one. A Go test
# applies the old migrations and testdata/after-<dialect>.sql of
# pkg/store/internal/baseline to an empty database, writes its schema and
# rows as the baseline, and checks that the baseline gives the same schema
# and rows.
#
# Usage: scripts/regenerate-baseline.sh [ref]   (default origin/main)
#
# Environment:
#   DUSTPAN_TEST_POSTGRES_URL  PostgreSQL database for the dump (required)
#
# Needs git, tar, go and pg_dump. The major version of pg_dump must be the
# major version of the server, because the output of pg_dump can change
# between major versions; the PostgreSQL baseline records it. Use the major
# version that CI runs. The baseline tests write the hash file of each
# baseline next to it. After the script, run make generate.
set -eu

REF="${1:-origin/main}"
: "${DUSTPAN_TEST_POSTGRES_URL:?set DUSTPAN_TEST_POSTGRES_URL to a PostgreSQL database}"

ROOT="$(git rev-parse --show-toplevel)"

if [ -n "$(git -C "$ROOT" status --porcelain -- pkg/store/sqlite/migrations pkg/store/postgres/migrations)" ]; then
	echo "regenerate-baseline: commit or stash the migration changes first" >&2
	exit 1
fi

IN="$(mktemp -d)"
trap 'rm -rf "$IN"' EXIT

git -C "$ROOT" archive "$REF" pkg/store/sqlite/migrations pkg/store/postgres/migrations | tar -x -C "$IN"

SHA="$(git -C "$ROOT" rev-parse --short=12 "$REF^{commit}")"

DUSTPAN_TEST_BASELINE_IN="$IN" DUSTPAN_TEST_BASELINE_OUT="$ROOT" DUSTPAN_TEST_BASELINE_REF="$REF ($SHA)" \
	go -C "$ROOT" test -count=1 -run '^TestWrite(SQLite|Postgres)Baseline$' ./pkg/store/internal/baseline/

# The new baseline is the baseline file with the highest version. Both
# dialects must have the same one.
BASELINE=""
for dialect in sqlite postgres; do
	newest="$(ls "$ROOT/pkg/store/$dialect/migrations/" | grep '^[0-9]*_baseline\.sql$' | sort | tail -n 1)"
	if [ -z "$newest" ]; then
		echo "regenerate-baseline: no baseline file in pkg/store/$dialect/migrations" >&2
		exit 1
	fi
	if [ -n "$BASELINE" ] && [ "$newest" != "$BASELINE" ]; then
		echo "regenerate-baseline: the baselines differ: $BASELINE and $newest" >&2
		exit 1
	fi
	BASELINE="$newest"
done
VERSION="$(printf '%s' "$BASELINE" | cut -d_ -f1 | sed 's/^0*//')"

# Delete each migration file with a lower version than the new baseline.
for dialect in sqlite postgres; do
	for file in "$ROOT/pkg/store/$dialect/migrations/"*.sql "$ROOT/pkg/store/$dialect/migrations/"*.sql.sha256; do
		[ -e "$file" ] || continue
		name="$(basename "$file" .sha256)"
		version="$(printf '%s' "$name" | cut -d_ -f1 | sed 's/^0*//')"
		if [ "${version:-0}" -lt "$VERSION" ]; then
			rm "$file"
		fi
	done
done

# Set the baseline constants of the Go code to the new baseline.
set_constant() {
	file="$1"
	pattern="$2"
	line="$3"
	if ! grep -q "$pattern" "$file"; then
		echo "regenerate-baseline: no line that matches $pattern in $file" >&2
		exit 1
	fi
	sed "s|$pattern|$line|" "$file" >"$file.tmp"
	mv "$file.tmp" "$file"
}
set_constant "$ROOT/pkg/store/schema/schema.go" '^const BaselineVersion int64 = [0-9]*$' "const BaselineVersion int64 = $VERSION"
set_constant "$ROOT/pkg/store/schema/schema.go" '^const BaselineName = "[0-9]*_baseline\.sql"$' "const BaselineName = \"$BASELINE\""
set_constant "$ROOT/pkg/backup/backup.go" '^const BaselineSchemaVersion = [0-9]*$' "const BaselineSchemaVersion = $VERSION"

echo "regenerate-baseline: wrote $BASELINE in each dialect"
