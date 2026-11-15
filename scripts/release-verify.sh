#!/usr/bin/env bash
# Check a set of release archives.
# Usage: scripts/release-verify.sh <version> <dir>   (for example v0.1.0)
#
# The script reads <dir> and changes nothing. It refuses the set, with the
# first error that it finds, when:
#   - the version is not vMAJOR.MINOR.PATCH (bad version);
#   - an archive or SHA256SUMS is absent, or the directory holds another file
#     (missing file, unexpected file);
#   - SHA256SUMS lacks the entry of an archive, or holds another entry
#     (SHA256SUMS entry);
#   - the SHA-256 of an archive differs from its entry (checksum mismatch);
#   - an archive holds an entry outside the allowlist of the binary, LICENSE,
#     README.md and third_party_licenses/, a link, a repeated name, a name
#     with an absolute path or a dot dot part, or lacks LICENSE or a license
#     file of third_party_licenses/ (archive content);
#   - the binary does not carry the version as its stamp (stamp mismatch).
#
# The script never runs the binary and never extracts a file to disk. It
# reads the stamp from the bytes of the binary, so that it can check the
# binary of a platform that is not the platform of the host.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: release-verify.sh <version> <dir>" >&2
  exit 1
fi

version="$1"
dir="$2"

# Allowlist of the version. Defense in depth: release-build.sh checks the
# version too; this check stays so that the script never uses a value that it
# did not check, for example in a pattern.
if [[ ! "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "release-verify: bad version; use v followed by three dot separated numbers, for example v0.1.0: run make release-verify VERSION=v0.1.0" >&2
  exit 1
fi

fail() {
  echo "release-verify: $1; rebuild the set, then run make release-verify VERSION=$version" >&2
  exit 1
}

[[ -d "$dir" ]] || fail "missing file: $dir is not a directory"

platforms=(linux_amd64 linux_arm64 darwin_amd64 darwin_arm64)
expected=(SHA256SUMS)
for platform in "${platforms[@]}"; do
  expected+=("opendustpan_${version}_${platform}.tar.gz")
done

# Missing files first, then files that the set does not name.
for name in "${expected[@]}"; do
  [[ -f "$dir/$name" && ! -L "$dir/$name" ]] || fail "missing file: $name"
done

for path in "$dir"/* "$dir"/.[!.]* "$dir"/..?*; do
  [[ -e "$path" || -L "$path" ]] || continue
  name="${path##*/}"
  known=0
  for want in "${expected[@]}"; do
    [[ "$name" == "$want" ]] && known=1
  done
  ((known)) || fail "unexpected file: $name"
done

# SHA256SUMS: one line for each archive, "<64 hex>  <name>", and no other line.
declare -A sum_of=()
while IFS= read -r line || [[ -n "$line" ]]; do
  if [[ ! "$line" =~ ^([0-9a-f]{64})\ \ ([^[:space:]/]+)$ ]]; then
    fail "SHA256SUMS has an unexpected entry: line is not '<sha256>  <name>'"
  fi
  entry_name="${BASH_REMATCH[2]}"
  known=0
  for want in "${expected[@]:1}"; do
    [[ "$entry_name" == "$want" ]] && known=1
  done
  ((known)) || fail "SHA256SUMS has an unexpected entry: $entry_name"
  [[ -z "${sum_of[$entry_name]:-}" ]] || fail "SHA256SUMS has an unexpected entry: $entry_name twice"
  sum_of[$entry_name]="${BASH_REMATCH[1]}"
done <"$dir/SHA256SUMS"

for want in "${expected[@]:1}"; do
  [[ -n "${sum_of[$want]:-}" ]] || fail "SHA256SUMS has no entry for $want"
done

for want in "${expected[@]:1}"; do
  got="$(sha256sum -- "$dir/$want")"
  [[ "${got%% *}" == "${sum_of[$want]}" ]] || fail "checksum mismatch: $want"
done

# The names are listed with the escape quoting style, so that a name with a
# newline or a backslash cannot pass for two names or for an allowed name.
name_ok='^[A-Za-z0-9._+@~=!-]+(/[A-Za-z0-9._+@~=!-]+)*$'

for want in "${expected[@]:1}"; do
  archive="$dir/$want"

  gzip -t -- "$archive" 2>/dev/null || fail "archive content: $want is not a gzip file"
  names="$(tar --quoting-style=escape -tzf "$archive" 2>/dev/null)" || fail "archive content: $want is not a tar file"
  types="$(tar --quoting-style=escape -tvzf "$archive" 2>/dev/null | cut -c1)" || fail "archive content: $want is not a tar file"

  [[ -n "$names" ]] || fail "archive content: $want is empty"
  [[ "$(printf '%s\n' "$names" | wc -l)" == "$(printf '%s\n' "$types" | wc -l)" ]] ||
    fail "archive content: $want has a name that the check cannot read"

  has_binary=0 has_license=0 has_third_party=0
  declare -A seen=()
  seen=()
  while IFS= read -r entry && IFS= read -r type <&3; do
    name="${entry%/}"
    [[ "$type" == "-" || "$type" == "d" ]] || fail "archive content: $want holds $name, which is not a file or a directory"
    [[ "$name" =~ $name_ok ]] || fail "archive content: $want holds the name $entry, which is not allowed"
    [[ "$entry" != /* ]] || fail "archive content: $want holds the absolute path $entry"
    case "/$name/" in
      */../* | */./*) fail "archive content: $want holds the path $entry, which has a dot part" ;;
    esac
    [[ -z "${seen[$name]:-}" ]] || fail "archive content: $want holds $name twice"
    seen[$name]=1

    case "$name" in
      opendustpan)
        [[ "$type" == "-" ]] || fail "archive content: $want holds a directory named opendustpan"
        has_binary=1
        ;;
      LICENSE)
        [[ "$type" == "-" ]] || fail "archive content: $want holds a directory named LICENSE"
        has_license=1
        ;;
      README.md)
        [[ "$type" == "-" ]] || fail "archive content: $want holds a directory named README.md"
        ;;
      third_party_licenses) [[ "$type" == "d" ]] || fail "archive content: $want holds a file named third_party_licenses" ;;
      third_party_licenses/*) [[ "$type" == "-" ]] && has_third_party=1 ;;
      *) fail "archive content: $want holds $name, which is not on the list of allowed files" ;;
    esac
  done < <(printf '%s\n' "$names") 3< <(printf '%s\n' "$types")

  ((has_binary)) || fail "archive content: $want has no opendustpan binary"
  ((has_license)) || fail "archive content: $want has no LICENSE"
  ((has_third_party)) || fail "archive content: $want has no file in third_party_licenses"

  # The stamp: the linker stores the value of -X main.version as a string in
  # the binary. The pattern is the version, not followed by a digit or by a
  # dot and a digit, so v0.1.0 does not match v0.1.00 or v0.1.0.1. The binary
  # goes from tar to grep and is never run.
  pattern="${version//./\\.}([^0-9.]|\\.[^0-9]|\\.\$|\$)"
  count="$(tar -xzOf "$archive" opendustpan 2>/dev/null | LC_ALL=C grep -a -c -E -- "$pattern" || true)"
  [[ "$count" -ge 1 ]] || fail "stamp mismatch: $want does not carry the version $version"
done

echo "release-verify: $version: four archives and SHA256SUMS are good"
