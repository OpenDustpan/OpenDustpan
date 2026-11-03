#!/usr/bin/env bash
# Build the release archives for one version.
# Usage: scripts/release-build.sh <version> <outdir>
# The version is stamped into the binary with -ldflags. The script
# cross-builds the server for four platform pairs, packs each binary with
# LICENSE, README.md and third_party_licenses/ (the license files of the code
# linked into that binary, see scripts/third-party-licenses.sh), and writes
# SHA256SUMS in the output directory. It first runs
# scripts/check-release-record.sh, which refuses a version that the release
# record does not list with the migration files of the work tree.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: release-build.sh <version> <outdir>" >&2
  exit 1
fi

version="$1"
outdir="$2"

if [[ -z "$version" || -z "$outdir" ]]; then
  echo "usage: release-build.sh <version> <outdir>" >&2
  exit 1
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The release record must list the migrations of this version before any
# build. Without its lines the checks of the shipped migrations stay off.
# Defense in depth: TestTagCopiesHaveTheirLines and
# TestMigrationFilesFollowTheRecord check the record after the tag; this
# check stays so that a release with an empty record fails before its tag.
(cd "$root" && "$root/scripts/check-release-record.sh" "$version")

mkdir -p "$outdir"
outdir_abs="$(cd "$outdir" && pwd)"
cd "$root"
staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT

platforms=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
)

for entry in "${platforms[@]}"; do
  read -r os arch <<<"$entry"
  bindir="$staging/$os-$arch"
  mkdir -p "$bindir"
  binary="opendustpan"
  archive="opendustpan_${version}_${os}_${arch}"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
    -ldflags "-X main.version=${version}" \
    -o "$bindir/$binary" ./cmd/opendustpan
  "$root/scripts/third-party-licenses.sh" "$os" "$arch" "$bindir/third_party_licenses"
  cp "$root/LICENSE" "$root/README.md" "$bindir/"
  rm -f "$outdir_abs/$archive.tar.gz"
  (cd "$bindir" && tar -czf "$outdir_abs/$archive.tar.gz" "$binary" LICENSE README.md third_party_licenses)
done

(cd "$outdir_abs" && sha256sum -- *.tar.gz > SHA256SUMS)
