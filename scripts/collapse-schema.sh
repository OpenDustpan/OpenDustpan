#!/usr/bin/env bash
# Collapse the numbered migrations of each store dialect into the schema file
# of that dialect, pkg/store/<dialect>/schema.sql, from the numbered
# migrations of a Git ref, and delete the numbered migrations from the work
# tree.
#
# Until the first tag, each dialect has one schema file and no numbered
# migration. Run this script to make the schema files from a ref that still
# holds numbered migrations, for example origin/main before the change that
# removes them merges.
#
# The script takes the migration files of the ref, not of the work tree, so
# it gives the same schema files when the work tree holds none. A Go test
# applies the old migrations to an empty database, writes its schema and rows
# as the schema file, and fails unless the schema file gives the same schema
# and rows.
#
# Usage: scripts/collapse-schema.sh [ref]   (default origin/main)
#
# Environment:
#   DUSTPAN_TEST_POSTGRES_URL  PostgreSQL database for the dump (required)
#
# Needs git, tar, go and pg_dump. The major version of pg_dump must be the
# major version of the server, because the output of pg_dump can change
# between major versions. Use the major version that CI runs. After the
# script, run make generate.
set -eu

REF="${1:-origin/main}"
: "${DUSTPAN_TEST_POSTGRES_URL:?set DUSTPAN_TEST_POSTGRES_URL to a PostgreSQL database}"

ROOT="$(git rev-parse --show-toplevel)"

for dialect in sqlite postgres; do
	if [ -n "$(find "$ROOT/pkg/store/$dialect/testdata/tag-migrations" -mindepth 1 -maxdepth 1 -type d 2>/dev/null)" ]; then
		echo "collapse-schema: tagged migrations exist; a collapse after a tag is not implemented" >&2
		exit 1
	fi
done

IN="$(mktemp -d)"
trap 'rm -rf "$IN"' EXIT

for dialect in sqlite postgres; do
	if [ -z "$(git -C "$ROOT" ls-tree --name-only "$REF" "pkg/store/$dialect/migrations/")" ]; then
		echo "collapse-schema: $REF holds no numbered $dialect migrations; the schema files are the schema" >&2
		exit 1
	fi
done

git -C "$ROOT" archive "$REF" pkg/store/sqlite/migrations pkg/store/postgres/migrations | tar -x -C "$IN"

SHA="$(git -C "$ROOT" rev-parse --short=12 "$REF^{commit}")"

DUSTPAN_TEST_BASELINE_IN="$IN" DUSTPAN_TEST_BASELINE_OUT="$ROOT" DUSTPAN_TEST_BASELINE_REF="$REF ($SHA)" \
	go -C "$ROOT" test -count=1 -v -run '^TestWrite(SQLite|Postgres)SchemaFile$' ./pkg/store/internal/baseline/

# Delete the numbered migrations and their hash files of each dialect, and
# the name stubs of the shared list except the stub of the schema file.
STUB="$(sed -n 's/^const BaselineName = "\(.*\)"$/\1/p' "$ROOT/pkg/store/schema/schema.go")"
if [ -z "$STUB" ]; then
	echo "collapse-schema: no BaselineName constant in pkg/store/schema/schema.go" >&2
	exit 1
fi

for dialect in sqlite postgres; do
	rm -rf "$ROOT/pkg/store/$dialect/migrations"
done

for file in "$ROOT/pkg/store/schema/migrations/"*.sql; do
	[ -e "$file" ] || continue
	if [ "$(basename "$file")" != "$STUB" ]; then
		rm "$file"
	fi
done

printf '%s\n%s\n' \
	'-- Name record for the shared migration list. Each store dialect applies its' \
	'-- schema file, pkg/store/<dialect>/schema.sql, under this name.' \
	>"$ROOT/pkg/store/schema/migrations/$STUB"

echo "collapse-schema: wrote pkg/store/sqlite/schema.sql and pkg/store/postgres/schema.sql from $REF ($SHA)"
