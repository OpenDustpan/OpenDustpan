#!/usr/bin/env bash
# Create one draft release that holds the four release archives and SHA256SUMS.
# Usage: scripts/release-upload.sh <version> <dir>   (for example v0.1.0)
# Environment: GH_REPO, owner/name. GH_TOKEN is read by gh only; this script
# never reads, prints or passes it.
#
# The script refuses, and makes no release, when:
#   - the version is not vMAJOR.MINOR.PATCH (bad version);
#   - GH_REPO is not owner/name;
#   - the set fails scripts/release-verify.sh, or the shape and the checksums
#     of the set fail the checks in this script;
#   - a release of the repository, draft or published, has the tag, or the
#     list of releases cannot be read.
# It creates a draft and never publishes it. It never overwrites, edits or
# deletes a release. A person reads the draft and publishes it with
# gh release edit <version> --draft=false.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: release-upload.sh <version> <dir>" >&2
  exit 1
fi

version="$1"
dir="$2"

# refuse prints one line and stops. It never prints a value that the caller
# gave before its allowlist has passed, so a hostile value cannot reach a log.
refuse() {
  echo "release-upload: $1" >&2
  exit 1
}

# Allowlist of the version. Defense in depth: release-build.sh and
# release-verify.sh check the version too; this check stays so that the script
# never uses a value that it did not check, for example in a file name or in a
# gh argument. It relies on no other layer.
if [[ ! "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  refuse "bad version; use v followed by three dot separated numbers, for example v0.1.0: run make release-verify VERSION=v0.1.0"
fi
[[ ${#version} -le 64 ]] || refuse "bad version; use v followed by three dot separated numbers, for example v0.1.0: run make release-verify VERSION=v0.1.0"

# Allowlist of the repository. Defense in depth: gh checks the value too; this
# check stays so that the repository name in a message or in an API path never
# holds an unchecked value. It relies on no other layer.
repo="${GH_REPO:-}"
if [[ ! "$repo" =~ ^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$ ]]; then
  refuse "GH_REPO is not owner/name; set GH_REPO to owner/name"
fi

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
help="rebuild the set, then run make release-verify VERSION=$version"

[[ -d "$dir" ]] || refuse "missing file: $dir is not a directory; $help"

# The first check: the verify script of the build. Defense in depth: the
# verify job runs it too, and the checks below do not rely on it; this call
# stays so that this script never uploads a set that the verify script
# refuses.
# The message of the verify script is printed with its control characters
# removed, so that a file name in the set cannot add a line to a log.
if ! verify_message="$(bash "$here/release-verify.sh" "$version" "$dir" 2>&1 >/dev/null)"; then
  refuse "$(printf '%s' "${verify_message#release-verify: }" | LC_ALL=C tr -d '[:cntrl:]')"
fi

# The second check, made with other tools than the verify script: the shape
# of the set, then sha256sum. Defense in depth: it relies on neither
# release-verify.sh nor the verify job. A caller that replaces the verify
# script by one that passes still meets this check.
archives=()
for platform in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
  archives+=("opendustpan_${version}_${platform}.tar.gz")
done
files=(SHA256SUMS "${archives[@]}")

for name in "${files[@]}"; do
  [[ -f "$dir/$name" && ! -L "$dir/$name" ]] || refuse "SHA256SUMS does not match the files: $name is missing or is a link; $help"
done

for path in "$dir"/* "$dir"/.[!.]* "$dir"/..?*; do
  [[ -e "$path" || -L "$path" ]] || continue
  known=0
  for name in "${files[@]}"; do
    [[ "${path##*/}" == "$name" ]] && known=1
  done
  ((known)) || refuse "SHA256SUMS does not match the files: the directory holds a file that is not in the set; $help"
done

# The lines of SHA256SUMS are an allowlist: exactly one line for each archive,
# in the form <64 lowercase hex>, two spaces, the archive name, and no other
# line. sha256sum --check --strict checks only the listed files, so a line for
# a path outside the set would pass it. A refused line is never repaired.
mapfile -t sum_lines <"$dir/SHA256SUMS"
[[ ${#sum_lines[@]} -eq ${#archives[@]} ]] || refuse "SHA256SUMS does not match the files: it must hold exactly one line for each of the four archives; $help"
for line in "${sum_lines[@]}"; do
  ok=0
  for name in "${archives[@]}"; do
    [[ "$line" =~ ^[0-9a-f]{64}\ \ ${name//./\.}$ ]] && ok=1
  done
  ((ok)) || refuse "SHA256SUMS does not match the files: a line is not <64 hex>, two spaces and an archive name; $help"
done
for name in "${archives[@]}"; do
  count=0
  for line in "${sum_lines[@]}"; do
    [[ "${line:66}" == "$name" ]] && count=$((count + 1))
  done
  [[ "$count" -eq 1 ]] || refuse "SHA256SUMS does not match the files: $name has no line or more than one; $help"
done

if ! (cd "$dir" && sha256sum --check --strict --quiet SHA256SUMS >/dev/null 2>&1); then
  refuse "SHA256SUMS does not match the files; $help"
fi

# No overwrite. The list holds drafts too. A failed call is a refusal and
# never an empty list. Defense in depth: gh release create refuses a tag that
# has a published release; this check stays so that a draft with the tag is
# found too, and it relies on no other layer.
if ! tags="$(gh api "repos/$repo/releases" --paginate --jq '.[].tag_name')"; then
  refuse "could not list the releases of $repo; check the token, then run: gh release list --repo $repo"
fi
if printf '%s\n' "$tags" | grep -Fxq -- "$version"; then
  refuse "a release for $version exists; list releases with: gh release list --repo $repo"
fi

notes="Archives for $version: ${archives[*]}.
SHA256SUMS is not a signature.
Check one archive: grep ' <archive>\$' SHA256SUMS | sha256sum -c -"

# The five names are written out, with no glob. A draft only: there is no
# publish flag and no --clobber.
cd -- "$dir"
if ! gh release create "$version" --draft --verify-tag --title "$version" --notes "$notes" -- "${files[@]}" >/dev/null; then
  refuse "the draft may exist with some files; delete it with: gh release delete $version --repo $repo, then re-run the failed job"
fi

echo "release-upload: $version: draft release created with four archives and SHA256SUMS; review it, then publish it with: gh release edit $version --draft=false"
