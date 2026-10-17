#!/usr/bin/env bash
# Copy the migration set of one release tag into the test data.
# Usage: scripts/copy-tag-migrations.sh <tag>   (for example v0.1.0)
# For each store dialect it copies pkg/store/<dialect>/migrations/*.sql and the
# matching *.sql.sha256 hash files, as they were at the tag, into
# pkg/store/<dialect>/testdata/tag-migrations/<tag>/. frozen_test.go checks
# each copy against its hash file, so the copies need no hash list of their own.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: copy-tag-migrations.sh <tag>" >&2
  exit 1
fi

tag="$1"

if [[ -z "$tag" ]]; then
  echo "usage: copy-tag-migrations.sh <tag>" >&2
  exit 1
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT

git -C "$root" archive "$tag" pkg/store/sqlite/migrations pkg/store/postgres/migrations | tar -x -C "$staging"

for dialect in sqlite postgres; do
  src="$staging/pkg/store/$dialect/migrations"
  dest="$root/pkg/store/$dialect/testdata/tag-migrations/$tag"
  if ! compgen -G "$src/*.sql" > /dev/null; then
    echo "no migrations for $dialect at $tag" >&2
    exit 1
  fi
  mkdir -p "$dest"
  cp "$src/"*.sql "$dest/"
  cp "$src/"*.sql.sha256 "$dest/"
done
