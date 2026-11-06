#!/usr/bin/env bash
# Check the release record for one release version.
# Usage: scripts/check-release-record.sh <version>   (for example v0.1.0)
#
# The script refuses when internal/releaserecord/shipped.txt has no line for
# the version, or when the lines of the version and the migration files of
# the work tree differ in a file name or a SHA-256, in either store dialect.
# scripts/release-build.sh runs it before it builds anything, so a release
# cannot go out with an empty record.
#
# Defense in depth: the record turns the checks of the shipped migrations on,
# TestMigrationFilesFollowTheRecord holds the migration files to it, and
# TestTagCopiesHaveTheirLines fails when the lines of a copied tag go. This
# check stays so that a release with no lines fails before its tag, even
# when each of those layers sees an empty record.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: check-release-record.sh <version>" >&2
  exit 1
fi

version="$1"

if [[ ! "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "check-release-record: version $version is not vMAJOR.MINOR.PATCH" >&2
  exit 1
fi

root="$(git rev-parse --show-toplevel)"
record="$root/internal/releaserecord/shipped.txt"

if [[ ! -f "$record" ]]; then
  echo "check-release-record: $record is missing" >&2
  exit 1
fi

# A CR or a NUL byte can hide a difference from awk and from the shell, so the
# record that holds one is not valid. Defense in depth: this check does not
# rely on the later comparison of the lines, and that comparison does not rely
# on this check.
if ! LC_ALL=C tr -d '\000\r' < "$record" | cmp -s - "$record"; then
  echo "check-release-record: $record has a CR or NUL byte; remove it" >&2
  exit 1
fi

for dialect in sqlite postgres; do
  dir="$root/pkg/store/$dialect/migrations"
  want="$(cd "$dir" && sha256sum -- [0-9]*.sql | awk '{print $2, $1}' | sort)"
  got="$(awk -v v="$version" -v d="$dialect" '$1 == v && $2 == d {print $3, $4}' "$record" | sort)"

  if [[ -z "$got" ]]; then
    echo "check-release-record: internal/releaserecord/shipped.txt has no $dialect line for $version; add the lines of the tag as the file says" >&2
    exit 1
  fi

  if [[ "$got" != "$want" ]]; then
    echo "check-release-record: the $dialect lines of $version in internal/releaserecord/shipped.txt differ from pkg/store/$dialect/migrations:" >&2
    diff <(printf '%s\n' "$got") <(printf '%s\n' "$want") >&2 || true
    exit 1
  fi
done

echo "check-release-record: the record lists the migrations of $version"
