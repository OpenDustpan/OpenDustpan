#!/usr/bin/env bash
# Build the release archives for one version.
# Usage: scripts/release-build.sh <version> <outdir>
# The version is stamped into the binary with -ldflags. The script
# cross-builds the server for five platform pairs, packs each binary with
# LICENSE, README.md and third_party_licenses/ (the license files of the code
# linked into that binary, see scripts/third-party-licenses.sh), and writes
# SHA256SUMS in the output directory.
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
mkdir -p "$outdir"
outdir_abs="$(cd "$outdir" && pwd)"
cd "$root"
staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT

platforms=(
  "linux amd64 tar"
  "linux arm64 tar"
  "darwin amd64 tar"
  "darwin arm64 tar"
  "windows amd64 zip"
)

for entry in "${platforms[@]}"; do
  read -r os arch format <<<"$entry"
  bindir="$staging/$os-$arch"
  mkdir -p "$bindir"
  binary="opendustpan"
  archive="opendustpan_${version}_${os}_${arch}"
  if [[ "$os" == "windows" ]]; then
    binary="opendustpan.exe"
  fi
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
    -ldflags "-X main.version=${version}" \
    -o "$bindir/$binary" ./cmd/opendustpan
  "$root/scripts/third-party-licenses.sh" "$os" "$arch" "$bindir/third_party_licenses"
  cp "$root/LICENSE" "$root/README.md" "$bindir/"
  if [[ "$format" == "zip" ]]; then
    rm -f "$outdir_abs/$archive.zip"
    (cd "$bindir" && zip -q -r "$outdir_abs/$archive.zip" "$binary" LICENSE README.md third_party_licenses)
  else
    rm -f "$outdir_abs/$archive.tar.gz"
    (cd "$bindir" && tar -czf "$outdir_abs/$archive.tar.gz" "$binary" LICENSE README.md third_party_licenses)
  fi
done

(cd "$outdir_abs" && sha256sum -- *.tar.gz *.zip > SHA256SUMS)
