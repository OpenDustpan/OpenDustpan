#!/usr/bin/env bash
# Copy the migration set of one release tag into the test data.
# Usage: scripts/copy-tag-migrations.sh <tag>   (for example v0.1.0)
# For each store dialect it copies pkg/store/<dialect>/migrations/*.sql as
# they were at the tag into pkg/store/<dialect>/testdata/tag-migrations/<tag>/.
# It then prints the SHA-256 of each copied file, as frozen_test.go records it.
# Record each printed hash in frozen_test.go of each adapter in the same change.
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
  dest="$root/pkg/store/$dialect/testdata/tag-migrations/$tag"
  mkdir -p "$dest"
  cp "$staging/pkg/store/$dialect/migrations/"*.sql "$dest/"
done

for dialect in sqlite postgres; do
  dest="$root/pkg/store/$dialect/testdata/tag-migrations/$tag"
  echo "$dialect:"
  for file in "$dest/"*.sql; do
    name="$(basename "$file")"
    sum="$(sha256sum "$file" | cut -d' ' -f1)"
    echo "  \"$tag/$name\": \"$sum\","
  done
done
