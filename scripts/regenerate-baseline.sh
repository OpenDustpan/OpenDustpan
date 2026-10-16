#!/bin/sh
# Write the baseline migration of each store dialect from the numbered
# migrations of a Git ref, and delete those migrations from the work tree.
#
# The baseline replaces every migration before it, as a one-time exception
# to the rule that an applied migration never changes. Run this script again
# when a migration from before the baseline merges into the ref.
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
# version that CI runs. After the script, update the hashes of
# TestFrozenMigrations with the lines it prints, then run make generate.
set -eu

REF="${1:-origin/main}"
: "${DUSTPAN_TEST_POSTGRES_URL:?set DUSTPAN_TEST_POSTGRES_URL to a PostgreSQL database}"

ROOT="$(git rev-parse --show-toplevel)"
IN="$(mktemp -d)"
trap 'rm -rf "$IN"' EXIT

git -C "$ROOT" archive "$REF" pkg/store/sqlite/migrations pkg/store/postgres/migrations | tar -x -C "$IN"

SHA="$(git -C "$ROOT" rev-parse --short=12 "$REF^{commit}")"

DUSTPAN_TEST_BASELINE_IN="$IN" DUSTPAN_TEST_BASELINE_OUT="$ROOT" DUSTPAN_TEST_BASELINE_REF="$REF ($SHA)" \
	go -C "$ROOT" test -count=1 -run '^TestWrite(SQLite|Postgres)Baseline$' ./pkg/store/internal/baseline/

# The baseline has version 0100. Delete each migration file with a lower
# version, and keep the files after the baseline.
for dialect in sqlite postgres; do
	for file in "$ROOT/pkg/store/$dialect/migrations/"*.sql; do
		name="$(basename "$file")"
		version="$(printf '%s' "$name" | cut -d_ -f1 | sed 's/^0*//')"
		if [ "${version:-0}" -lt 100 ]; then
			rm "$file"
		fi
	done
done

for dialect in sqlite postgres; do
	sum="$(sha256sum "$ROOT/pkg/store/$dialect/migrations/0100_baseline.sql" | cut -d' ' -f1)"
	echo "$dialect: \"0100_baseline.sql\": \"$sum\","
done
